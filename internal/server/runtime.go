package server

import (
	"context"
	"strings"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/gateway"
	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/session"
)

// Runtime connects gateway requests to the engine and the session store.
type Runtime struct {
	Engine *engine.Engine
	Store  session.Store
}

// Handle runs one request. The error is only for storage failures; flow problems come back as END replies.
func (rt *Runtime) Handle(ctx context.Context, gw string, req *gateway.Request) (engine.Reply, bool, error) {
	key := gw + ":" + req.SessionID
	// Gateways disagree on "+233..." vs "233...". Use digits only, so {{phone}} and action calls look the same everywhere.
	req.Phone = strings.TrimPrefix(strings.TrimSpace(req.Phone), "+")
	if req.Release {
		return engine.Reply{End: true, Kind: "end"}, false, rt.Store.Delete(ctx, key)
	}

	var (
		s     *engine.Session
		reply engine.Reply
		isNew bool
	)
	if req.New {
		s, isNew = engine.NewSession(req.SessionID, gw, req.Phone, req.Network), true
		reply = rt.Engine.Start(ctx, s)
	} else {
		var err error
		if s, err = rt.Store.Get(ctx, key); err != nil {
			return engine.Reply{}, false, err
		}
		switch {
		case req.History:
			s, reply, isNew = rt.history(ctx, s, gw, req)
		case s == nil:
			reply = engine.Reply{End: true, Message: engine.SessionExpired, Kind: "end"}
		default:
			reply = rt.Engine.Continue(ctx, s, req.Input)
		}
	}

	if reply.End || s == nil {
		return reply, isNew, rt.Store.Delete(ctx, key)
	}
	return reply, isNew, rt.Store.Put(ctx, key, s)
}

// history handles Africa's Talking, which sends every key pressed so far ("1*2*0244...")
// rather than just the latest one. The new input is whatever follows what was already handled.
func (rt *Runtime) history(ctx context.Context, s *engine.Session, gw string, req *gateway.Request) (*engine.Session, engine.Reply, bool) {
	text := req.Input
	if s != nil {
		switch {
		case text == s.History:
			return s, rt.Engine.Current(s), false // the gateway repeated a request
		case s.History == "":
			s.History = text
			return s, rt.Engine.Continue(ctx, s, text), false
		case strings.HasPrefix(text, s.History+"*"):
			input := text[len(s.History)+1:]
			s.History = text
			return s, rt.Engine.Continue(ctx, s, input), false
		}
	}
	// No stored session (it expired, or another instance without shared storage had it).
	// A flow with no inputs or actions can be replayed safely from the keys alone,
	// exactly as USSD Flow Studio's exported servers do. Anything else could repeat a backend call.
	if !rt.Engine.Flow.Pure() {
		return nil, engine.Reply{End: true, Message: engine.SessionExpired, Kind: "end"}, false
	}
	s = engine.NewSession(req.SessionID, gw, req.Phone, req.Network)
	reply := rt.Engine.Start(ctx, s)
	for _, key := range strings.Split(text, "*") {
		if reply.End {
			break
		}
		reply = rt.Engine.Continue(ctx, s, key)
	}
	s.History = text
	return s, reply, true
}
