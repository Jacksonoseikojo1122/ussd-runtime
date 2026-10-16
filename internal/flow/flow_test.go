package flow

import (
	"strings"
	"testing"
)

func codes(ps []Problem) map[string]Severity {
	m := map[string]Severity{}
	for _, p := range ps {
		m[p.Code] = p.Severity
	}
	return m
}

func TestShippedFlowsAreClean(t *testing.T) {
	t.Setenv("BACKEND_URL", "https://api.example.com")
	for _, path := range []string{"../../flows/demo-wallet.json", "../../flows/school-fees.json"} {
		f, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if ps := Validate(f); len(ps) > 0 {
			t.Errorf("%s: %+v", path, ps)
		}
	}
}

func TestStudioRules(t *testing.T) {
	f, err := Parse([]byte(`{"start":"main","nodes":{
		"main":{"prompt":"","kind":"menu","options":[
			{"key":"1","label":"A","next":"a"},{"key":"1","label":"B","next":"a"},
			{"key":"x","label":"C","next":"a"},{"key":"2","label":"D","next":""},{"key":"3","label":"E","next":"gone"}]},
		"a":{"prompt":"Done","kind":"end","options":[{"key":"1","label":"ghost","next":"main"}]},
		"empty":{"prompt":"Stuck","kind":"menu","options":[]},
		"long":{"prompt":"` + strings.Repeat("x", 200) + `","kind":"end"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := codes(Validate(f))
	want := map[string]Severity{
		"empty-prompt": Warning, "duplicate-key": Error, "invalid-key": Error, "unlinked-option": Error,
		"dangling-next": Error, "end-has-options": Warning, "empty-menu": Error, "unreachable": Warning, "screen-too-long": Warning,
	}
	for c, sev := range want {
		if got[c] != sev {
			t.Errorf("%s: got %q, want %q", c, got[c], sev)
		}
	}
}

func TestInputAndActionRules(t *testing.T) {
	f, err := Parse([]byte(`{"start":"ask","nodes":{
		"ask":{"prompt":"Name?","kind":"input","input":{"name":"Phone","type":"colour","next":"nowhere","min_length":5,"max_length":2}},
		"noinput":{"prompt":"?","kind":"input"},
		"call":{"prompt":"x","kind":"end","action":{"url":"ftp://x","timeout_ms":9000,"can_go_to":["missing"]}},
		"plain":{"prompt":"x","kind":"end","action":{"url":"http://api.example.com/x"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := codes(Validate(f))
	for _, c := range []string{"input-name", "input-type", "dangling-next", "input-length", "input-missing", "action-url", "action-timeout"} {
		if got[c] != Error {
			t.Errorf("%s: got %q, want error", c, got[c])
		}
	}
	if got["action-http"] != Warning {
		t.Errorf("plain http to a remote host should warn")
	}
}

func TestParseRejectsBadStructure(t *testing.T) {
	for _, js := range []string{
		`[]`,
		`{"start":"a"}`,
		`{"start":"a","nodes":{"1bad":{"prompt":"x","kind":"end"}}}`,
		`{"start":"a","nodes":{"a":{"id":"b","prompt":"x","kind":"end"}}}`,
		`{"start":"a","nodes":{"a":{"prompt":"x","kind":"popup"}}}`,
	} {
		if _, err := Parse([]byte(js)); err == nil {
			t.Errorf("expected an error for %s", js)
		}
	}
}

func TestRenderFillsVariables(t *testing.T) {
	s := &Screen{Kind: Menu, Prompt: "Hi {{name}}, owes {{ balance }}", Options: []Option{{Key: "1", Label: "Pay {{balance}}"}}}
	got := Render(s, map[string]string{"name": "Ama", "balance": "10.00"})
	if got != "Hi Ama, owes 10.00\n1. Pay 10.00" {
		t.Fatal(got)
	}
	if got := Render(&Screen{Kind: End, Prompt: "Hi {{missing}}."}, nil); got != "Hi ." {
		t.Fatal(got)
	}
}

func TestCheckInput(t *testing.T) {
	cases := []struct {
		spec  InputSpec
		in    string
		value string
		ok    bool
	}{
		{InputSpec{Type: "text"}, "  Ama  ", "Ama", true},
		{InputSpec{Type: "text"}, "   ", "", false},
		{InputSpec{Type: "text", MaxLength: 3}, "Abena", "", false},
		{InputSpec{Type: "text", MinLength: 3}, "Ab", "", false},
		{InputSpec{Type: "digits"}, "0123", "0123", true},
		{InputSpec{Type: "digits"}, "12a", "", false},
		{InputSpec{Type: "digits", MinLength: 4}, "123", "", false},
		{InputSpec{Type: "amount"}, "5", "5.00", true},
		{InputSpec{Type: "amount"}, "5.5", "5.50", true},
		{InputSpec{Type: "amount"}, "5.555", "", false},
		{InputSpec{Type: "amount"}, "-5", "", false},
		{InputSpec{Type: "amount", Min: "1"}, "0.99", "", false},
		{InputSpec{Type: "amount", Max: "100.50"}, "100.50", "100.50", true},
		{InputSpec{Type: "amount", Max: "100.50"}, "100.51", "", false},
		{InputSpec{Type: "amount"}, "99999999999999999999", "", true}, // capped, not overflowed
		{InputSpec{Type: "phone_gh"}, "024 123 4567", "233241234567", true},
		{InputSpec{Type: "phone_gh"}, "+233-20-123-4567", "233201234567", true},
		{InputSpec{Type: "phone_gh"}, "233551234567", "233551234567", true},
		{InputSpec{Type: "phone_gh"}, "551234567", "233551234567", true},
		{InputSpec{Type: "phone_gh"}, "0012345678", "", false},
		{InputSpec{Type: "phone_gh"}, "12345", "", false},
	}
	for _, c := range cases {
		v, _, ok := CheckInput(&c.spec, c.in)
		if ok != c.ok || (c.value != "" && v != c.value) {
			t.Errorf("%s %q: got (%q, %v), want (%q, %v)", c.spec.Type, c.in, v, ok, c.value, c.ok)
		}
	}
}

func TestPure(t *testing.T) {
	t.Setenv("BACKEND_URL", "https://api.example.com")
	w, _ := Load("../../flows/demo-wallet.json")
	s, _ := Load("../../flows/school-fees.json")
	if !w.Pure() || s.Pure() {
		t.Fatal("demo wallet is pure; school fees is not")
	}
}
