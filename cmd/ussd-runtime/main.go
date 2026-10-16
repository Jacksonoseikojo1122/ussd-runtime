// Command ussd-runtime serves a USSD flow to Africa's Talking, Hubtel, Nalo and Arkesel.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/gateway"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/server"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/session"
)

const version = "0.1.0"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// actionClient keeps connections to the backend open. Go's default keeps only 2 idle
// connections per host, so under load every call opened a new TCP connection and the
// slowest replies took seconds.
func actionClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 512
	t.MaxIdleConnsPerHost = 256
	t.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: t}
}

func run(log *slog.Logger) error {
	path := env("FLOW_FILE", "flows/demo-wallet.json")
	f, err := flow.Load(path)
	if err != nil {
		return fmt.Errorf("loading %s: %w", path, err)
	}
	problems := flow.Validate(f)
	for _, p := range problems {
		log.Warn("flow", "severity", p.Severity, "code", p.Code, "screen", p.Screen, "msg", p.Message)
	}
	if flow.HasErrors(problems) {
		return fmt.Errorf("%s has errors; fix them before running it", path)
	}

	ttl, err := time.ParseDuration(env("SESSION_TTL", "180s"))
	if err != nil {
		return fmt.Errorf("SESSION_TTL: %w", err)
	}
	var store session.Store
	if url := os.Getenv("REDIS_URL"); url != "" {
		r, err := session.NewRedis(url, ttl)
		if err != nil {
			return fmt.Errorf("REDIS_URL: %w", err)
		}
		store = r
	} else {
		log.Warn("REDIS_URL is not set: sessions are kept in memory, so run one instance only")
		store = session.NewMemory(ttl)
	}

	secret := os.Getenv("ACTION_SECRET")
	if secret == "" && !f.Pure() {
		log.Warn("ACTION_SECRET is not set: action calls are signed with an empty key")
	}
	metrics := server.NewMetrics()
	eng := &engine.Engine{
		Flow:   f,
		Caller: &engine.HTTPCaller{Client: actionClient(), Secret: secret},
		OnAction: func(screen string, d time.Duration, err error) {
			metrics.Action(d, err)
			if err != nil {
				log.Warn("action failed", "screen", screen, "ms", d.Milliseconds(), "err", err)
			}
		},
	}
	srv := &server.Server{
		Runtime:   &server.Runtime{Engine: eng, Store: store},
		Adapters:  gateway.All(),
		Metrics:   metrics,
		Log:       log,
		Token:     os.Getenv("GATEWAY_TOKEN"),
		Simulator: env("SIMULATOR", "off") == "on",
		Problems:  problems,
	}

	addr := ":" + env("PORT", "8080")
	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("ussd-runtime", "version", version, "addr", addr, "flow", path, "screens", len(f.Nodes), "simulator", srv.Simulator)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
