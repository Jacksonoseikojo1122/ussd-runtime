package flow

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// CheckInput validates what the caller typed on an input screen. It returns the value
// to store (normalised, e.g. "12.50" or "233241234567") or ok=false with the reason.
func CheckInput(in *InputSpec, raw string) (value string, reason string, ok bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", "Please enter a value.", false
	}
	switch in.Type {
	case "digits":
		if !optionKeyPattern.MatchString(v) {
			return "", "Use numbers only.", false
		}
	case "amount":
		if !amountPattern.MatchString(v) {
			return "", "Enter an amount like 10 or 10.50.", false
		}
		p := pesewas(v)
		if in.Min != "" && p < pesewas(in.Min) {
			return "", "The minimum is " + money(pesewas(in.Min)) + ".", false
		}
		if in.Max != "" && p > pesewas(in.Max) {
			return "", "The maximum is " + money(pesewas(in.Max)) + ".", false
		}
		return money(p), "", true
	case "phone_gh":
		n, ok := ghanaPhone(v)
		if !ok {
			return "", "Enter a Ghana number like 024 123 4567.", false
		}
		return n, "", true
	}
	n := utf8.RuneCountInString(v)
	min, max := in.MinLength, in.MaxLength
	if max == 0 {
		max = 60
	}
	if n < min {
		return "", "That's too short.", false
	}
	if n > max {
		return "", "That's too long.", false
	}
	return v, "", true
}

// pesewas parses a string already matched by amountPattern.
func pesewas(s string) int64 {
	whole, frac, _ := strings.Cut(s, ".")
	frac = (frac + "00")[:2]
	var p int64
	for _, c := range whole + frac {
		p = p*10 + int64(c-'0')
		if p > 1e15 {
			return 1e15 // far above any sensible limit
		}
	}
	return p
}

func money(p int64) string { return fmt.Sprintf("%d.%02d", p/100, p%100) }

// ghanaPhone accepts 0241234567, 241234567, 233241234567 or +233 24 123 4567
// (spaces and dashes ignored) and returns 233241234567.
func ghanaPhone(s string) (string, bool) {
	s = strings.NewReplacer(" ", "", "-", "").Replace(s)
	s = strings.TrimPrefix(s, "+")
	switch {
	case len(s) == 12 && strings.HasPrefix(s, "233"):
		s = s[3:]
	case len(s) == 10 && s[0] == '0':
		s = s[1:]
	case len(s) == 9:
	default:
		return "", false
	}
	if !optionKeyPattern.MatchString(s) || s[0] == '0' {
		return "", false
	}
	return "233" + s, true
}
