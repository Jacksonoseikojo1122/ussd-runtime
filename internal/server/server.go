package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/gateway"
)

//go:embed simulator.html
var assets embed.FS

var simPage = template.Must(template.ParseFS(assets, "simulator.html"))

type Server struct {
	Runtime   *Runtime
	Adapters  map[string]gateway.Adapter
	Metrics   *Metrics
	Log       *slog.Logger
	Token     string         // if set, gateways must send ?token=... (or X-Gateway-Token)
	Simulator bool           // serve the phone simulator at /
	Problems  []flow.Problem // validation warnings, shown in the simulator
}

func (s *Server) Handler() http.Handler {
	if s.Metrics == nil {
		s.Metrics = NewMetrics()
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ussd/{gateway}", s.ussd)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.Metrics.Write(w)
	})
	if s.Simulator {
		mux.HandleFunc("GET /{$}", s.simulator)
		mux.HandleFunc("POST /sim", s.sim)
	}
	return mux
}

func (s *Server) ussd(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	name := r.PathValue("gateway")
	a, ok := s.Adapters[name]
	if !ok {
		http.Error(w, "unknown gateway; use one of /ussd/africastalking, /ussd/hubtel, /ussd/nalo, /ussd/arkesel", http.StatusNotFound)
		return
	}
	if s.Token != "" {
		got := r.URL.Query().Get("token")
		if got == "" {
			got = r.Header.Get("X-Gateway-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	req, err := a.Parse(r)
	if err != nil {
		s.Metrics.Request(name, time.Since(start), false, false, true)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reply, isNew, err := s.Runtime.Handle(r.Context(), name, req)
	if err != nil {
		// Storage is down. Close the session politely rather than leaving the caller waiting.
		s.Log.Error("session store", "gateway", name, "err", err)
		reply = engine.Reply{End: true, Message: engine.ServiceUnavailable, Kind: "end"}
	}
	a.Write(w, req, reply)
	d := time.Since(start)
	s.Metrics.Request(name, d, isNew, reply.End, err != nil)
	s.Log.Info("ussd", "gateway", name, "session", req.SessionID, "phone", mask(req.Phone),
		"screen", reply.Screen, "end", reply.End, "ms", float64(d.Microseconds())/1000)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Runtime.Store.Ping(r.Context()); err != nil {
		http.Error(w, "session store unreachable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) simulator(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = simPage.Execute(w, map[string]any{"Problems": s.Problems})
}

// sim drives the flow from the simulator page, through the same Runtime as real gateways.
func (s *Server) sim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Input     string `json:"input"`
		New       bool   `json:"new"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.SessionID == "" {
		http.Error(w, "send {session_id, input, new}", http.StatusBadRequest)
		return
	}
	start := time.Now()
	req := &gateway.Request{SessionID: in.SessionID, Phone: "233240000000", Network: "SIM", New: in.New, Input: in.Input}
	reply, isNew, err := s.Runtime.Handle(r.Context(), "simulator", req)
	if err != nil {
		reply = engine.Reply{End: true, Message: engine.ServiceUnavailable, Kind: "end"}
	}
	d := time.Since(start)
	s.Metrics.Request("simulator", d, isNew, reply.End, err != nil)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"reply": reply, "ms": float64(d.Microseconds()) / 1000})
}

// mask keeps logs useful without storing full phone numbers.
func mask(p string) string {
	if len(p) < 7 {
		return p
	}
	return p[:5] + "****" + p[len(p)-3:]
}
