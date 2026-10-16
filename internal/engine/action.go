package engine

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/flow"
)

// HTTPCaller posts the session to the action's URL and reads back variables and an
// optional next screen. Requests are signed the same way Webhook Relay signs deliveries:
// X-USSD-Signature: v1=hex(HMAC-SHA256(secret, timestamp + "." + body)).
type HTTPCaller struct {
	Client *http.Client
	Secret string
	Now    func() time.Time
}

type actionRequest struct {
	SessionID string            `json:"session_id"`
	Gateway   string            `json:"gateway"`
	Phone     string            `json:"phone"`
	Network   string            `json:"network"`
	Screen    string            `json:"screen"`
	Vars      map[string]string `json:"vars"`
}

func (c *HTTPCaller) Call(ctx context.Context, a *flow.Action, s *Session) (*ActionResult, error) {
	body, err := json.Marshal(actionRequest{s.ID, s.Gateway, s.Phone, s.Network, s.Screen, s.Vars})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	ts := strconv.FormatInt(now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ussd-runtime")
	req.Header.Set("X-USSD-Timestamp", ts)
	req.Header.Set("X-USSD-Signature", Sign(c.Secret, ts, body))

	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("action returned HTTP %d", resp.StatusCode)
	}
	var res ActionResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res); err != nil {
		return nil, fmt.Errorf("action returned invalid JSON: %w", err)
	}
	return &res, nil
}

// Sign returns the X-USSD-Signature value for a request body.
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "v1=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a signature on your backend's side (exported for Go backends and tests).
func Verify(secret, ts, sig string, body []byte, now time.Time, tolerance time.Duration) bool {
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(t, 0)); d > tolerance || d < -tolerance {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(Sign(secret, ts, body)))
}
