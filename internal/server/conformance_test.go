package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/gateway"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/session"
)

// These cases are USSD Flow Studio's engine tests (tests/engine.test.ts), so a flow exported
// from the Studio answers here exactly as it does in the Studio simulator and exported servers.

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func loadFlow(t *testing.T, path string) *flow.Flow {
	t.Helper()
	f, err := flow.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mustParse(t *testing.T, js string) *flow.Flow {
	t.Helper()
	f, err := flow.Parse([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// atServer is a runtime behind the Africa's Talking adapter, with in-memory sessions.
func atServer(f *flow.Flow) *httptest.Server {
	s := &Server{
		Runtime:  &Runtime{Engine: &engine.Engine{Flow: f}, Store: session.NewMemory(time.Minute)},
		Adapters: gateway.All(),
	}
	return httptest.NewServer(s.Handler())
}

func atPost(t *testing.T, ts *httptest.Server, session, text string) string {
	t.Helper()
	resp, err := http.PostForm(ts.URL+"/ussd/africastalking", url.Values{
		"sessionId": {session}, "phoneNumber": {"+233241234567"}, "networkCode": {"62001"}, "serviceCode": {"*384#"}, "text": {text},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return string(must(io.ReadAll(resp.Body)))
}

var sessionN int

// replay sends the whole history in one request with no stored session, like a fresh instance.
func replay(t *testing.T, ts *httptest.Server, text string) string {
	sessionN++
	return atPost(t, ts, fmt.Sprintf("replay-%d", sessionN), text)
}

// stepwise dials and sends each key in turn, as a real caller does, and returns the last reply.
func stepwise(t *testing.T, ts *httptest.Server, text string) string {
	sessionN++
	id := fmt.Sprintf("step-%d", sessionN)
	out := atPost(t, ts, id, "")
	if text == "" {
		return out
	}
	keys := strings.Split(text, "*")
	for i := range keys {
		out = atPost(t, ts, id, strings.Join(keys[:i+1], "*"))
	}
	return out
}

func TestStudioConformance(t *testing.T) {
	wallet := loadFlow(t, "../../flows/demo-wallet.json")
	dup := mustParse(t, `{"start":"home","nodes":{
		"home":{"prompt":"Choose","kind":"menu","options":[{"key":"1","label":"Send money","next":"confirm_send"},{"key":"2","label":"Withdraw","next":"confirm_withdraw"}]},
		"confirm_send":{"prompt":"Confirm?","kind":"menu","options":[{"key":"1","label":"Yes","next":"sent"}]},
		"confirm_withdraw":{"prompt":"Confirm?","kind":"menu","options":[{"key":"1","label":"Yes","next":"withdrawn"}]},
		"sent":{"prompt":"Money sent.","kind":"end","options":[]},
		"withdrawn":{"prompt":"Withdrawal approved.","kind":"end","options":[]}}}`)
	endPrompt := mustParse(t, strings.Replace(string(must(os.ReadFile("../../flows/demo-wallet.json"))), `"Welcome to Demo Wallet"`, `"END of month promo"`, 1))
	missing := mustParse(t, strings.Replace(string(must(os.ReadFile("../../flows/demo-wallet.json"))), `"next": "airtime_network" }`, `"next": "deleted_screen" }`, 1))
	unlinked := mustParse(t, strings.Replace(string(must(os.ReadFile("../../flows/demo-wallet.json"))), `"next": "airtime_network" }`, `"next": "" }`, 1))
	ghost := mustParse(t, strings.Replace(string(must(os.ReadFile("../../flows/demo-wallet.json"))),
		`"prompt": "Your Demo Wallet balance is GHS 120.50.",
      "kind": "end",
      "options": []`, `"prompt": "Your Demo Wallet balance is GHS 120.50.",
      "kind": "end",
      "options": [{ "key": "1", "label": "Ghost", "next": "main" }]`, 1))
	noStart := mustParse(t, `{"start":"nope","nodes":{}}`)
	closed := mustParse(t, `{"start":"only","nodes":{"only":{"prompt":"Closed today.","kind":"end","options":[]}}}`)

	menu := "CON Welcome to Demo Wallet\n1. Buy airtime\n2. Check balance\n3. Exit"
	network := "CON Buy airtime for which network?\n1. MTN\n2. Telecel\n3. AT\n0. Back"
	amount := "CON Select amount\n1. GHS 1\n2. GHS 5\n3. GHS 10\n0. Back"
	balance := "END Your Demo Wallet balance is GHS 120.50."

	cases := []struct {
		name string
		f    *flow.Flow
		text string
		want string
	}{
		{"first dial shows the start screen", wallet, "", menu},
		{"nested menu 1", wallet, "1", network},
		{"nested menu 1*2", wallet, "1*2", amount},
		{"nested menu to end", wallet, "1*2*3", "END Request received. You will get an SMS confirmation shortly."},
		{"back option", wallet, "1*0", menu},
		{"back twice then balance", wallet, "1*1*0*0*2", balance},
		{"end screen", wallet, "2", balance},
		{"exit", wallet, "3", "END Thank you for using Demo Wallet."},
		{"input after END is ignored", wallet, "2*9*9", balance},
		{"unknown key", wallet, "9", "END Invalid option."},
		{"unknown nested key", wallet, "1*7", "END Invalid option."},
		{"empty key", wallet, "1*", "END Invalid option."},
		{"key with a space", wallet, " 1", "END Invalid option."},
		{"routes by id, not prompt (send)", dup, "1*1", "END Money sent."},
		{"routes by id, not prompt (withdraw)", dup, "2*1", "END Withdrawal approved."},
		{"prompt starting with END is still a menu", endPrompt, "", "CON END of month promo\n1. Buy airtime\n2. Check balance\n3. Exit"},
		{"option to a missing screen", missing, "1", "END Invalid option."},
		{"unlinked option", unlinked, "1", "END Invalid option."},
		{"missing start screen", noStart, "", "END Service unavailable."},
		{"start screen is an end screen", closed, "", "END Closed today."},
		{"start screen is an end screen, extra keys", closed, "1", "END Closed today."},
		{"options on end screens are hidden", ghost, "2", balance},
		{"options on end screens do nothing", ghost, "2*1", balance},
	}
	servers := map[*flow.Flow]*httptest.Server{}
	for _, c := range cases {
		ts, ok := servers[c.f]
		if !ok {
			ts = atServer(c.f)
			defer ts.Close()
			servers[c.f] = ts
		}
		t.Run(c.name+"/replay", func(t *testing.T) {
			if got := replay(t, ts, c.text); got != c.want {
				t.Errorf("text %q:\n got %q\nwant %q", c.text, got, c.want)
			}
		})
		t.Run(c.name+"/stepwise", func(t *testing.T) {
			if got := stepwise(t, ts, c.text); got != c.want {
				t.Errorf("text %q:\n got %q\nwant %q", c.text, got, c.want)
			}
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
