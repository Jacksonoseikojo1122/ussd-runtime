package flow

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
)

type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

type Problem struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Screen   string   `json:"screen,omitempty"`
}

const (
	// MaxScreenLength: many gateways cut or reject pages longer than this (GSM 7-bit).
	MaxScreenLength = 182
	// NaloMaxLength: Nalo's documented maximum for MSG.
	NaloMaxLength = 120
)

var (
	optionKeyPattern = regexp.MustCompile(`^[0-9]+$`)
	amountPattern    = regexp.MustCompile(`^\d+(\.\d{1,2})?$`)
	inputTypes       = map[string]bool{"text": true, "digits": true, "amount": true, "phone_gh": true}
)

// HasErrors reports whether any problem is an error. Flows with errors aren't run.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == Error {
			return true
		}
	}
	return false
}

// Validate checks a flow. The menu and end rules match USSD Flow Studio's validator;
// the input and action rules are new.
func Validate(f *Flow) []Problem {
	var ps []Problem
	add := func(sev Severity, code, screen, format string, a ...any) {
		ps = append(ps, Problem{Severity: sev, Code: code, Screen: screen, Message: fmt.Sprintf(format, a...)})
	}

	if f.Start == "" {
		add(Error, "missing-start", "", "No start screen is set.")
	} else if f.Get(f.Start) == nil {
		add(Error, "missing-start", "", "Start screen %q does not exist.", f.Start)
	}

	ids := make([]string, 0, len(f.Nodes))
	for id := range f.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids) // stable output

	for _, id := range ids {
		s := f.Nodes[id]
		if s.Prompt == "" {
			add(Warning, "empty-prompt", id, "Screen %q has an empty prompt.", id)
		}
		switch s.Kind {
		case End:
			if len(s.Options) > 0 {
				add(Warning, "end-has-options", id, "End screen %q has %d option(s) that will never be shown.", id, len(s.Options))
			}
		case Menu:
			if len(s.Options) == 0 {
				add(Error, "empty-menu", id, "Menu screen %q has no options, so the caller is stuck.", id)
			}
			seen := map[string]bool{}
			for i, o := range s.Options {
				n := i + 1
				if !optionKeyPattern.MatchString(o.Key) {
					add(Error, "invalid-key", id, "Option #%d on %q has key %q; keys must be digits only.", n, id, o.Key)
				}
				if seen[o.Key] {
					add(Error, "duplicate-key", id, "Key %q is used twice on %q.", o.Key, id)
				}
				seen[o.Key] = true
				if o.Next == "" {
					add(Error, "unlinked-option", id, "Option #%d on %q isn't linked to a screen.", n, id)
				} else if f.Get(o.Next) == nil {
					add(Error, "dangling-next", id, "Option #%d on %q points to %q, which doesn't exist.", n, id, o.Next)
				}
			}
		case Input:
			validateInput(f, s, add)
		}
		if s.Action != nil {
			validateAction(f, s, add)
		}
		if s.Kind != Input && s.Input != nil {
			add(Warning, "input-ignored", id, "Screen %q isn't an input screen, so its input settings are ignored.", id)
		}
		if len(TemplateVars(s)) == 0 {
			if n := len([]rune(Render(s, nil))); n > MaxScreenLength {
				add(Warning, "screen-too-long", id, "Screen %q is %d characters; many networks cut pages at %d.", id, n, MaxScreenLength)
			} else if n > NaloMaxLength {
				add(Warning, "too-long-for-nalo", id, "Screen %q is %d characters; Nalo allows %d.", id, n, NaloMaxLength)
			}
		}
	}

	r := reachable(f)
	for _, id := range ids {
		if !r[id] && f.Get(f.Start) != nil {
			add(Warning, "unreachable", id, "Screen %q can't be reached from the start screen.", id)
		}
	}
	return ps
}

func validateInput(f *Flow, s *Screen, add func(Severity, string, string, string, ...any)) {
	id := s.ID
	in := s.Input
	if in == nil {
		add(Error, "input-missing", id, "Input screen %q needs an \"input\" object (name, type, next).", id)
		return
	}
	if !VarNamePattern.MatchString(in.Name) {
		add(Error, "input-name", id, "Input name %q on %q must be lowercase letters, digits or _ and start with a letter.", in.Name, id)
	}
	if in.Name == "phone" || in.Name == "network" {
		add(Error, "input-name", id, "Input name %q on %q is reserved for the caller's own %s.", in.Name, id, in.Name)
	}
	if !inputTypes[in.Type] {
		add(Error, "input-type", id, "Input type %q on %q isn't supported; use text, digits, amount or phone_gh.", in.Type, id)
	}
	if in.Next == "" {
		add(Error, "unlinked-input", id, "Input screen %q has no next screen.", id)
	} else if f.Get(in.Next) == nil {
		add(Error, "dangling-next", id, "Input screen %q points to %q, which doesn't exist.", id, in.Next)
	}
	if in.MinLength < 0 || in.MaxLength < 0 || (in.MaxLength > 0 && in.MinLength > in.MaxLength) {
		add(Error, "input-length", id, "Input lengths on %q don't make sense (min %d, max %d).", id, in.MinLength, in.MaxLength)
	}
	for _, v := range []string{in.Min, in.Max} {
		if v != "" && !amountPattern.MatchString(v) {
			add(Error, "input-amount", id, "Amount limit %q on %q must look like 10 or 10.50.", v, id)
		}
	}
	if (in.Min != "" || in.Max != "") && in.Type != "amount" {
		add(Warning, "input-ignored", id, "min and max on %q only apply to amount inputs.", id)
	}
}

func validateAction(f *Flow, s *Screen, add func(Severity, string, string, string, ...any)) {
	id := s.ID
	a := s.Action
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		add(Error, "action-url", id, "Action on %q needs a full http(s) URL, got %q.", id, a.URL)
	} else if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		add(Warning, "action-http", id, "Action on %q uses plain http; use https outside your own machine.", id)
	}
	if a.TimeoutMS < 100 || a.TimeoutMS > MaxActionTimeoutMS {
		add(Error, "action-timeout", id, "Action timeout on %q is %dms; it must be 100 to %d so the network doesn't drop the session.", id, a.TimeoutMS, MaxActionTimeoutMS)
	}
	for _, n := range a.CanGoTo {
		if f.Get(n) == nil {
			add(Error, "dangling-next", id, "Action on %q may go to %q, which doesn't exist.", id, n)
		}
	}
}

func reachable(f *Flow) map[string]bool {
	seen := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		s := f.Get(id)
		if s == nil || seen[id] {
			return
		}
		seen[id] = true
		if s.Kind == Menu {
			for _, o := range s.Options {
				visit(o.Next)
			}
		}
		if s.Kind == Input && s.Input != nil {
			visit(s.Input.Next)
		}
		if s.Action != nil {
			for _, n := range s.Action.CanGoTo {
				visit(n)
			}
		}
	}
	visit(f.Start)
	return seen
}
