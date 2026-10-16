// Package engine runs a flow one step at a time. It knows nothing about gateways:
// adapters turn each gateway's request into Start or Continue, and the reply back.
package engine

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
)

// Messages that match USSD Flow Studio, so exported flows behave the same here.
const (
	InvalidOption      = "Invalid option."
	ServiceUnavailable = "Service unavailable."
	TooManyAttempts    = "Too many invalid attempts. Please dial again."
	ActionFailed       = "Sorry, we couldn't complete that right now. Please try again later."
	SessionExpired     = "Your session has expired. Please dial again."
)

const (
	maxRedirects    = 5
	maxInputRetries = 3
	maxVars         = 30
	maxVarLength    = 160
)

// Session is everything the runtime remembers between requests.
type Session struct {
	ID      string            `json:"id"`
	Gateway string            `json:"gateway"`
	Phone   string            `json:"phone"`
	Network string            `json:"network"`
	Screen  string            `json:"screen"`
	Vars    map[string]string `json:"vars"`
	History string            `json:"history,omitempty"` // Africa's Talking: the text already handled
	Retries int               `json:"retries,omitempty"`
	Steps   int               `json:"steps"`
	Started time.Time         `json:"started"`
}

type Reply struct {
	End     bool   `json:"end"`
	Message string `json:"message"`
	Screen  string `json:"screen"` // empty for runtime-level messages
	Kind    string `json:"kind"`   // menu, input or end
	// InputType is set on input screens (text, digits, amount, phone_gh) so adapters can hint the handset.
	InputType string `json:"input_type,omitempty"`
}

// ActionResult is what a backend returns from an action call.
type ActionResult struct {
	Vars map[string]string `json:"vars"`
	Next string            `json:"next"`
}

// Caller makes action calls. The real one is an HTTP client; tests use a fake.
type Caller interface {
	Call(ctx context.Context, a *flow.Action, s *Session) (*ActionResult, error)
}

type Engine struct {
	Flow     *flow.Flow
	Caller   Caller
	OnAction func(screen string, d time.Duration, err error) // optional, for metrics and logs
}

func NewSession(id, gateway, phone, network string) *Session {
	return &Session{
		ID: id, Gateway: gateway, Phone: phone, Network: network,
		Vars:    map[string]string{"phone": phone, "network": network},
		Started: time.Now().UTC(),
	}
}

// Start shows the first screen.
func (e *Engine) Start(ctx context.Context, s *Session) Reply {
	s.Steps = 1
	return e.enter(ctx, s, e.Flow.Start, 0)
}

// Continue handles what the caller typed on the current screen.
func (e *Engine) Continue(ctx context.Context, s *Session, input string) Reply {
	s.Steps++
	sc := e.Flow.Get(s.Screen)
	if sc == nil {
		return end(ServiceUnavailable)
	}
	switch sc.Kind {
	case flow.Menu:
		for _, o := range sc.Options {
			if o.Key == input {
				s.Retries = 0
				return e.enter(ctx, s, o.Next, 0)
			}
		}
		return end(InvalidOption)
	case flow.Input:
		v, reason, ok := flow.CheckInput(sc.Input, input)
		if !ok {
			s.Retries++
			if s.Retries >= maxInputRetries {
				return end(TooManyAttempts)
			}
			msg := sc.Input.Error
			if msg == "" {
				msg = reason
			}
			r := show(sc, s.Vars)
			r.Message = msg + "\n" + r.Message
			return r
		}
		s.Retries = 0
		s.Vars[sc.Input.Name] = v
		return e.enter(ctx, s, sc.Input.Next, 0)
	default: // an end screen: the session is already over
		return show(sc, s.Vars)
	}
}

func (e *Engine) enter(ctx context.Context, s *Session, id string, depth int) Reply {
	sc := e.Flow.Get(id)
	if sc == nil {
		if depth == 0 && id == e.Flow.Start {
			return end(ServiceUnavailable)
		}
		return end(InvalidOption)
	}
	s.Screen = id
	if sc.Action != nil {
		res, err := e.call(ctx, sc, s)
		if err != nil {
			msg := sc.Action.ErrorMessage
			if msg == "" {
				msg = ActionFailed
			}
			return end(msg)
		}
		for k, v := range res.Vars {
			s.Vars[k] = v
		}
		if res.Next != "" && res.Next != id {
			if depth >= maxRedirects {
				return end(ActionFailed)
			}
			return e.enter(ctx, s, res.Next, depth+1)
		}
	}
	return show(sc, s.Vars)
}

// Current shows the screen the caller is on again, e.g. when a gateway repeats a request.
func (e *Engine) Current(s *Session) Reply {
	sc := e.Flow.Get(s.Screen)
	if sc == nil {
		return end(ServiceUnavailable)
	}
	return show(sc, s.Vars)
}

func show(sc *flow.Screen, vars map[string]string) Reply {
	r := Reply{End: sc.Kind == flow.End, Message: flow.Render(sc, vars), Screen: sc.ID, Kind: string(sc.Kind)}
	if sc.Kind == flow.Input && sc.Input != nil {
		r.InputType = sc.Input.Type
	}
	return r
}

var errBadResult = errors.New("action returned something the flow doesn't allow")

func (e *Engine) call(ctx context.Context, sc *flow.Screen, s *Session) (*ActionResult, error) {
	if e.Caller == nil {
		return nil, errors.New("no action caller configured")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(sc.Action.TimeoutMS)*time.Millisecond)
	defer cancel()
	t := time.Now()
	res, err := e.Caller.Call(ctx, sc.Action, s)
	if err == nil {
		err = checkResult(sc, res, len(s.Vars))
	}
	if e.OnAction != nil {
		e.OnAction(sc.ID, time.Since(t), err)
	}
	return res, err
}

// checkResult keeps a backend from steering the caller anywhere the flow author didn't allow,
// or overwriting the caller's phone number.
func checkResult(sc *flow.Screen, res *ActionResult, have int) error {
	if res == nil {
		return errBadResult
	}
	if res.Next != "" && res.Next != sc.ID && !slices.Contains(sc.Action.CanGoTo, res.Next) {
		return errBadResult
	}
	if have+len(res.Vars) > maxVars {
		return errBadResult
	}
	for k, v := range res.Vars {
		if !flow.VarNamePattern.MatchString(k) || k == "phone" || k == "network" || len(v) > maxVarLength {
			return errBadResult
		}
	}
	return nil
}

func end(msg string) Reply { return Reply{End: true, Message: msg, Kind: string(flow.End)} }
