// A backend for flows/school-fees.json. It shows what an action endpoint receives and returns.
// The students are made up and /ussd/pay doesn't charge anyone.
//
//	ACTION_SECRET=dev-secret go run ./examples/demo-backend          # :9090
//	BACKEND_URL=http://localhost:9090 ACTION_SECRET=dev-secret FLOW_FILE=flows/school-fees.json SIMULATOR=on go run ./cmd/ussd-runtime
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type actionRequest struct {
	SessionID string            `json:"session_id"`
	Phone     string            `json:"phone"`
	Screen    string            `json:"screen"`
	Vars      map[string]string `json:"vars"`
}

type student struct{ Name, Class, Balance string }

var students = map[string]student{
	"1001": {"Ama Mensah", "JHS 2", "1,250.00"},
	"1002": {"Kofi Boateng", "Primary 5", "0.00"},
	"2040": {"Esi Owusu", "JHS 1", "480.50"},
}

var secret = []byte(os.Getenv("ACTION_SECRET"))

func main() {
	http.HandleFunc("POST /ussd/student", handle(func(req actionRequest) any {
		s, ok := students[req.Vars["student_id"]]
		if !ok {
			return map[string]any{"next": "not_found"}
		}
		return map[string]any{"vars": map[string]string{"student_name": s.Name, "class_name": s.Class, "balance": s.Balance}}
	}))
	http.HandleFunc("POST /ussd/pay", handle(func(req actionRequest) any {
		// A real backend would start a mobile money collection through a licensed provider here.
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		ref := "SUN-" + hex.EncodeToString(b)
		log.Printf("payment request %s: GHS %s for student %s from %s", ref, req.Vars["amount"], req.Vars["student_id"], req.Phone)
		return map[string]any{"vars": map[string]string{"reference": ref}}
	}))
	addr := ":" + envOr("PORT", "9090")
	log.Printf("demo backend on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// handle checks the runtime's signature before running f.
func handle(f func(actionRequest) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil || !verify(r.Header.Get("X-USSD-Timestamp"), r.Header.Get("X-USSD-Signature"), body) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		var req actionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f(req))
	}
}

// verify: v1=hex(HMAC-SHA256(secret, timestamp + "." + body)), at most 5 minutes old.
func verify(ts, sig string, body []byte) bool {
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(t, 0)).Abs() > 5*time.Minute {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hmac.Equal([]byte(sig), []byte("v1="+hex.EncodeToString(m.Sum(nil))))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
