// Package flow is the USSD flow model. It reads the JSON that USSD Flow Studio exports
// (menu and end screens) and adds input screens, session variables in prompts, and
// actions that call your backend.
package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type Kind string

const (
	Menu  Kind = "menu"  // shows options, session continues (CON)
	End   Kind = "end"   // shows a message, session ends (END)
	Input Kind = "input" // asks the caller to type something, session continues (CON)
)

type Option struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Next  string `json:"next"`
}

// InputSpec describes what an input screen accepts and where it goes next.
type InputSpec struct {
	Name      string `json:"name"` // session variable the answer is stored in
	Type      string `json:"type"` // text | digits | amount | phone_gh
	Next      string `json:"next"` // screen after a valid answer
	MinLength int    `json:"min_length,omitempty"`
	MaxLength int    `json:"max_length,omitempty"`
	Min       string `json:"min,omitempty"`   // amount only, e.g. "1.00"
	Max       string `json:"max,omitempty"`   // amount only, e.g. "5000"
	Error     string `json:"error,omitempty"` // shown above the prompt when the answer is rejected
}

// Action is an HTTP call to your backend made when the caller reaches a screen.
type Action struct {
	URL          string   `json:"url"`
	TimeoutMS    int      `json:"timeout_ms,omitempty"`
	CanGoTo      []string `json:"can_go_to,omitempty"`     // screens the backend may send the caller to
	ErrorMessage string   `json:"error_message,omitempty"` // END message if the call fails or times out
}

type Screen struct {
	ID      string     `json:"id"`
	Prompt  string     `json:"prompt"`
	Kind    Kind       `json:"kind"`
	Options []Option   `json:"options"`
	Input   *InputSpec `json:"input,omitempty"`
	Action  *Action    `json:"action,omitempty"`
}

type Flow struct {
	Start string             `json:"start"`
	Nodes map[string]*Screen `json:"nodes"`
}

const (
	DefaultActionTimeoutMS = 2000
	MaxActionTimeoutMS     = 4000
)

var (
	ScreenIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,39}$`)
	VarNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	templateVar     = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]{0,31})\s*\}\}`)
)

// Get looks a screen up by id. Returns nil if there is no such screen.
func (f *Flow) Get(id string) *Screen {
	if f == nil || f.Nodes == nil {
		return nil
	}
	return f.Nodes[id]
}

// Pure reports whether the flow has no input screens and no actions. A pure flow's
// reply depends only on the keys pressed, so it can be replayed without stored state.
func (f *Flow) Pure() bool {
	for _, s := range f.Nodes {
		if s.Kind == Input || s.Action != nil {
			return false
		}
	}
	return true
}

// Parse reads a flow from JSON. Structural problems are errors; anything that can
// be described as a mistake in the flow is left to Validate.
func Parse(data []byte) (*Flow, error) {
	var f Flow
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("flow is not valid JSON: %w", err)
	}
	if f.Nodes == nil {
		return nil, fmt.Errorf(`flow needs a "nodes" object keyed by screen id`)
	}
	for id, s := range f.Nodes {
		if s == nil {
			return nil, fmt.Errorf("screen %q is null", id)
		}
		if !ScreenIDPattern.MatchString(id) {
			return nil, fmt.Errorf("screen id %q is invalid: use a letter, then letters, digits, _ or - (max 40)", id)
		}
		if s.ID == "" {
			s.ID = id
		} else if s.ID != id {
			return nil, fmt.Errorf("screen keyed %q declares a different id %q", id, s.ID)
		}
		switch s.Kind {
		case Menu, End, Input:
		default:
			return nil, fmt.Errorf("screen %q has kind %q; expected menu, end or input", id, s.Kind)
		}
		if s.Action != nil {
			s.Action.URL = os.ExpandEnv(s.Action.URL)
			if s.Action.TimeoutMS == 0 {
				s.Action.TimeoutMS = DefaultActionTimeoutMS
			}
		}
	}
	return &f, nil
}

// Load reads and parses a flow file.
func Load(path string) (*Flow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Render returns the text shown on the handset, with {{variables}} filled in.
func Render(s *Screen, vars map[string]string) string {
	fill := func(t string) string {
		return templateVar.ReplaceAllStringFunc(t, func(m string) string {
			return vars[templateVar.FindStringSubmatch(m)[1]]
		})
	}
	if s.Kind != Menu {
		return fill(s.Prompt)
	}
	lines := []string{fill(s.Prompt)}
	for _, o := range s.Options {
		lines = append(lines, o.Key+". "+fill(o.Label))
	}
	return strings.Join(lines, "\n")
}

// TemplateVars lists the {{variables}} a screen uses.
func TemplateVars(s *Screen) []string {
	text := s.Prompt
	for _, o := range s.Options {
		text += "\n" + o.Label
	}
	var out []string
	for _, m := range templateVar.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}
