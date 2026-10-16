package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/gateway"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/session"
)

const secret = "test-secret"

// backend is a fake school-fees backend that checks the runtime's signature.
func backend(t *testing.T, delay time.Duration, calls *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !engine.Verify(secret, r.Header.Get("X-USSD-Timestamp"), r.Header.Get("X-USSD-Signature"), body, time.Now(), 5*time.Minute) {
			http.Error(w, "bad signature", 401)
			return
		}
		time.Sleep(delay)
		var req struct {
			Vars map[string]string `json:"vars"`
		}
		_ = json.Unmarshal(body, &req)
		switch r.URL.Path {
		case "/ussd/student":
			switch req.Vars["student_id"] {
			case "1001":
				_, _ = w.Write([]byte(`{"vars":{"student_name":"Ama Mensah","class_name":"JHS 2","balance":"1,250.00"}}`))
			case "6666":
				_, _ = w.Write([]byte(`{"next":"main"}`)) // not in can_go_to
			case "7777":
				_, _ = w.Write([]byte(`{"vars":{"phone":"233200000000"}}`)) // reserved variable
			default:
				_, _ = w.Write([]byte(`{"next":"not_found"}`))
			}
		case "/ussd/pay":
			_, _ = w.Write([]byte(`{"vars":{"reference":"SUN-abc123"}}`))
		}
	}))
}

func feesServer(t *testing.T, backendURL string, token string) (*httptest.Server, *Metrics) {
	t.Helper()
	t.Setenv("BACKEND_URL", backendURL)
	f := loadFlow(t, "../../flows/school-fees.json")
	if ps := flow.Validate(f); flow.HasErrors(ps) {
		t.Fatalf("school-fees flow has errors: %+v", ps)
	}
	m := NewMetrics()
	s := &Server{
		Runtime: &Runtime{
			Engine: &engine.Engine{Flow: f, Caller: &engine.HTTPCaller{Secret: secret}, OnAction: func(_ string, d time.Duration, err error) { m.Action(d, err) }},
			Store:  session.NewMemory(time.Minute),
		},
		Adapters: gateway.All(),
		Metrics:  m,
		Token:    token,
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, m
}

func postJSON(t *testing.T, u string, v any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	resp, err := http.Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d: %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSchoolFeesOnEveryGateway(t *testing.T) {
	var calls atomic.Int64
	be := backend(t, 0, &calls)
	defer be.Close()
	ts, _ := feesServer(t, be.URL, "")

	steps := []struct{ input, want string }{
		{"", "Sunrise School fees\n1. Pay fees\n2. Check balance\n3. Exit"},
		{"1", "Enter the student ID"},
		{"12", "Student IDs are 4 to 8 digits.\nEnter the student ID"},
		{"1001", "Pay for Ama Mensah (JHS 2)?\nOutstanding: GHS 1,250.00\n1. Yes\n2. No"},
		{"1", "Enter amount in GHS"},
		{"9000", "The maximum is 5000.00.\nEnter amount in GHS"},
		{"250.5", "Pay GHS 250.50 for Ama Mensah from 233241234567?\n1. Confirm\n2. Cancel"},
		{"1", "Request SUN-abc123 sent. Approve GHS 250.50 on the prompt that follows."},
	}
	last := len(steps) - 1

	t.Run("africastalking", func(t *testing.T) {
		history := ""
		for i, s := range steps {
			if i > 0 {
				if history != "" {
					history += "*"
				}
				history += s.input
			}
			got := atPost(t, ts, "at-1", history)
			want := "CON " + s.want
			if i == last {
				want = "END " + s.want
			}
			if got != want {
				t.Fatalf("step %d (%q):\n got %q\nwant %q", i, history, got, want)
			}
		}
	})

	t.Run("hubtel", func(t *testing.T) {
		for i, s := range steps {
			typ, msg := "Response", s.input
			if i == 0 {
				typ, msg = "Initiation", "*713*1#"
			}
			out := postJSON(t, ts.URL+"/ussd/hubtel", map[string]any{"SessionId": "hub-1", "Mobile": "233241234567", "Type": typ, "Message": msg, "Operator": "mtn", "Sequence": i + 1, "ServiceCode": "713", "Platform": "USSD"})
			wantType := "Response"
			if i == last {
				wantType = "Release"
			}
			if out["Message"] != s.want || out["Type"] != wantType || out["SessionId"] != "hub-1" {
				t.Fatalf("step %d: got %v", i, out)
			}
		}
	})

	t.Run("hubtel input hints", func(t *testing.T) {
		postJSON(t, ts.URL+"/ussd/hubtel", map[string]any{"SessionId": "hub-2", "Mobile": "233241234567", "Type": "Initiation", "Message": "*713#"})
		out := postJSON(t, ts.URL+"/ussd/hubtel", map[string]any{"SessionId": "hub-2", "Mobile": "233241234567", "Type": "Response", "Message": "1"})
		if out["DataType"] != "input" || out["FieldType"] != "number" {
			t.Fatalf("got %v", out)
		}
		out = postJSON(t, ts.URL+"/ussd/hubtel", map[string]any{"SessionId": "hub-2", "Type": "Release"})
		if out["Type"] != "Release" {
			t.Fatalf("release: %v", out)
		}
		// The session is gone after Release.
		out = postJSON(t, ts.URL+"/ussd/hubtel", map[string]any{"SessionId": "hub-2", "Type": "Response", "Message": "1001"})
		if out["Message"] != engine.SessionExpired {
			t.Fatalf("after release: %v", out)
		}
	})

	t.Run("nalo", func(t *testing.T) {
		for i, s := range steps {
			first := any(i == 0)
			if i == 0 {
				first = 1 // the docs describe MSGTYPE as BOOLEAN 1/0
			}
			out := postJSON(t, ts.URL+"/ussd/nalo", map[string]any{"USERID": "NALOTest", "MSISDN": "233241234567", "USERDATA": s.input, "MSGTYPE": first, "NETWORK": "MTN", "SESSIONID": "nalo-1"})
			if out["MSG"] != s.want || out["MSGTYPE"] != (i != last) || out["USERID"] != "NALOTest" {
				t.Fatalf("step %d: got %v", i, out)
			}
		}
	})

	t.Run("arkesel", func(t *testing.T) {
		for i, s := range steps {
			input := s.input
			if i == 0 {
				input = "*928*1#"
			}
			out := postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "ark-1", "userID": "USSD_DOC", "newSession": i == 0, "msisdn": "233241234567", "userData": input, "network": "MTN"})
			if out["message"] != s.want || out["continueSession"] != (i != last) || out["sessionID"] != "ark-1" || out["userID"] != "USSD_DOC" || out["msisdn"] != "233241234567" {
				t.Fatalf("step %d: got %v", i, out)
			}
		}
	})
}

func TestActions(t *testing.T) {
	var calls atomic.Int64
	be := backend(t, 0, &calls)
	defer be.Close()
	ts, m := feesServer(t, be.URL, "")
	nalo := func(id, data string, first bool) string {
		out := postJSON(t, ts.URL+"/ussd/nalo", map[string]any{"MSISDN": "233241234567", "USERDATA": data, "MSGTYPE": first, "SESSIONID": id})
		return out["MSG"].(string)
	}

	t.Run("backend can send the caller to an allowed screen", func(t *testing.T) {
		nalo("a1", "", true)
		nalo("a1", "2", false)
		if got := nalo("a1", "9999", false); got != "We couldn't find student 9999. Check the ID and dial again." {
			t.Fatal(got)
		}
	})
	t.Run("backend can't send the caller anywhere else", func(t *testing.T) {
		nalo("a2", "", true)
		nalo("a2", "2", false)
		if got := nalo("a2", "6666", false); got != engine.ActionFailed {
			t.Fatal(got)
		}
	})
	t.Run("backend can't overwrite the caller's phone number", func(t *testing.T) {
		nalo("a3", "", true)
		nalo("a3", "2", false)
		if got := nalo("a3", "7777", false); got != engine.ActionFailed {
			t.Fatal(got)
		}
	})
	t.Run("three wrong inputs end the session", func(t *testing.T) {
		nalo("a4", "", true)
		nalo("a4", "1", false)
		nalo("a4", "x", false)
		nalo("a4", "x", false)
		if got := nalo("a4", "x", false); got != engine.TooManyAttempts {
			t.Fatal(got)
		}
	})
	if !strings.Contains(metricsText(t, ts), `ussd_actions_total{result="error"} 2`) {
		t.Errorf("metrics should count the two refused action results:\n%s", metricsText(t, ts))
	}
	_ = m
}

func TestSlowBackendTimesOut(t *testing.T) {
	var calls atomic.Int64
	be := backend(t, 2500*time.Millisecond, &calls) // longer than the flow's 2000ms
	defer be.Close()
	ts, _ := feesServer(t, be.URL, "")
	postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "slow", "newSession": true, "userData": "*928#"})
	postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "slow", "userData": "2"})
	start := time.Now()
	out := postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "slow", "userData": "1001"})
	if d := time.Since(start); d > 2300*time.Millisecond {
		t.Errorf("took %v; the 2s action budget wasn't enforced", d)
	}
	if out["message"] != engine.ActionFailed || out["continueSession"] != false {
		t.Fatalf("got %v", out)
	}
}

func TestBadSignatureIsAnActionFailure(t *testing.T) {
	var calls atomic.Int64
	be := backend(t, 0, &calls)
	defer be.Close()
	t.Setenv("BACKEND_URL", be.URL)
	f := loadFlow(t, "../../flows/school-fees.json")
	s := &Server{
		Runtime:  &Runtime{Engine: &engine.Engine{Flow: f, Caller: &engine.HTTPCaller{Secret: "wrong"}}, Store: session.NewMemory(time.Minute)},
		Adapters: gateway.All(),
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "sig", "newSession": true})
	postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "sig", "userData": "2"})
	if out := postJSON(t, ts.URL+"/ussd/arkesel", map[string]any{"sessionID": "sig", "userData": "1001"}); out["message"] != engine.ActionFailed {
		t.Fatalf("got %v", out)
	}
}

func TestFlowsWithActionsDontReplay(t *testing.T) {
	var calls atomic.Int64
	be := backend(t, 0, &calls)
	defer be.Close()
	ts, _ := feesServer(t, be.URL, "")
	// No stored session for this id: replaying "1*1001*1*250*1" would call /ussd/pay again.
	if got := atPost(t, ts, "lost", "1*1001*1*250*1"); got != "END "+engine.SessionExpired {
		t.Fatal(got)
	}
	if calls.Load() != 0 {
		t.Fatalf("backend was called %d times", calls.Load())
	}
}

func TestGatewayToken(t *testing.T) {
	ts, _ := feesServer(t, "http://localhost:1", "s3cret")
	form := url.Values{"sessionId": {"x"}, "text": {""}}
	resp, _ := http.PostForm(ts.URL+"/ussd/africastalking", form)
	if resp.StatusCode != 403 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	resp, _ = http.PostForm(ts.URL+"/ussd/africastalking?token=s3cret", form)
	if resp.StatusCode != 200 {
		t.Fatalf("with token: %d", resp.StatusCode)
	}
}

func TestBadRequests(t *testing.T) {
	ts, _ := feesServer(t, "http://localhost:1", "")
	for path, body := range map[string]string{
		"/ussd/hubtel":   `{"Type":"Initiation"}`, // no SessionId
		"/ussd/nalo":     `{"MSGTYPE":"maybe"}`,
		"/ussd/arkesel":  `not json`,
		"/ussd/whatever": `{}`,
	} {
		resp, _ := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}
}

func TestStoreDownEndsPolitely(t *testing.T) {
	f := loadFlow(t, "../../flows/demo-wallet.json")
	s := &Server{Runtime: &Runtime{Engine: &engine.Engine{Flow: f}, Store: brokenStore{}}, Adapters: gateway.All()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if got := atPost(t, ts, "x", ""); got != "END "+engine.ServiceUnavailable {
		t.Fatal(got)
	}
	resp, _ := http.Get(ts.URL + "/healthz")
	if resp.StatusCode != 503 {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}
}

func TestMetricsAndHealth(t *testing.T) {
	ts := atServer(loadFlow(t, "../../flows/demo-wallet.json"))
	defer ts.Close()
	atPost(t, ts, "m1", "")
	atPost(t, ts, "m1", "2")
	text := metricsText(t, ts)
	for _, want := range []string{
		`ussd_requests_total{gateway="africastalking"} 2`,
		"ussd_sessions_started_total 1",
		"ussd_sessions_ended_total 1",
		`ussd_request_duration_seconds_count{gateway="africastalking"} 2`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestSimulator(t *testing.T) {
	f := loadFlow(t, "../../flows/demo-wallet.json")
	s := &Server{Runtime: &Runtime{Engine: &engine.Engine{Flow: f}, Store: session.NewMemory(time.Minute)}, Adapters: gateway.All(), Simulator: true}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	page, _ := http.Get(ts.URL + "/")
	if b, _ := io.ReadAll(page.Body); !bytes.Contains(b, []byte("USSD Runtime simulator")) {
		t.Fatal("simulator page missing")
	}
	out := postJSON(t, ts.URL+"/sim", map[string]any{"session_id": "s", "new": true})
	if out["reply"].(map[string]any)["screen"] != "main" {
		t.Fatalf("got %v", out)
	}
	off := &Server{Runtime: s.Runtime, Adapters: gateway.All()}
	ts2 := httptest.NewServer(off.Handler())
	defer ts2.Close()
	if resp, _ := http.Get(ts2.URL + "/"); resp.StatusCode != 404 {
		t.Fatalf("simulator should be off by default, got %d", resp.StatusCode)
	}
}

func metricsText(t *testing.T, ts *httptest.Server) string {
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

type brokenStore struct{}

func (brokenStore) Get(context.Context, string) (*engine.Session, error) {
	return nil, os.ErrDeadlineExceeded
}
func (brokenStore) Put(context.Context, string, *engine.Session) error { return os.ErrDeadlineExceeded }
func (brokenStore) Delete(context.Context, string) error               { return os.ErrDeadlineExceeded }
func (brokenStore) Ping(context.Context) error                         { return os.ErrDeadlineExceeded }
