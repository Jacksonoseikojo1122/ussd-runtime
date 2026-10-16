// Package gateway translates each USSD gateway's HTTP format into one Request,
// and the runtime's Reply back into what that gateway expects.
//
// Formats were taken from each provider's own documentation (see docs/gateways.md):
//   - Africa's Talking: form fields, full input history in "text", reply "CON ..." / "END ..."
//   - Hubtel: JSON, Type Initiation/Response/Release/Timeout, reply Type Response/Release
//   - Nalo: JSON, MSGTYPE true on the first request, reply MSG + MSGTYPE (true = continue)
//   - Arkesel: JSON, newSession, reply message + continueSession
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
)

type Request struct {
	SessionID string
	Phone     string
	Network   string
	New       bool   // first request of a session
	Input     string // what the caller typed (latest input only)
	History   bool   // Input is the whole "*"-joined history (Africa's Talking)
	Release   bool   // the gateway says the session is over (caller hung up, timeout)
	echo      map[string]any
}

type Adapter interface {
	Name() string
	Parse(r *http.Request) (*Request, error)
	Write(w http.ResponseWriter, req *Request, reply engine.Reply)
}

// All returns every adapter, keyed by the name used in the URL: POST /ussd/{name}.
func All() map[string]Adapter {
	m := map[string]Adapter{}
	for _, a := range []Adapter{AfricasTalking{}, Hubtel{}, Nalo{}, Arkesel{}} {
		m[a.Name()] = a
	}
	return m
}

const maxBody = 16 << 10

var ErrBadRequest = errors.New("bad request")

func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- Africa's Talking ------------------------------------------------------

type AfricasTalking struct{}

func (AfricasTalking) Name() string { return "africastalking" }

func (AfricasTalking) Parse(r *http.Request) (*Request, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	id := r.PostForm.Get("sessionId")
	if id == "" {
		return nil, fmt.Errorf("%w: sessionId is required", ErrBadRequest)
	}
	text := r.PostForm.Get("text")
	return &Request{
		SessionID: id,
		Phone:     r.PostForm.Get("phoneNumber"),
		Network:   r.PostForm.Get("networkCode"),
		New:       text == "",
		Input:     text,
		History:   true,
	}, nil
}

func (AfricasTalking) Write(w http.ResponseWriter, _ *Request, reply engine.Reply) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	prefix := "CON "
	if reply.End {
		prefix = "END "
	}
	_, _ = io.WriteString(w, prefix+reply.Message)
}

// --- Hubtel -----------------------------------------------------------------

type Hubtel struct{}

func (Hubtel) Name() string { return "hubtel" }

type hubtelRequest struct {
	SessionID   string `json:"SessionId"`
	Mobile      string `json:"Mobile"`
	ServiceCode string `json:"ServiceCode"`
	Type        string `json:"Type"`
	Message     string `json:"Message"`
	Operator    string `json:"Operator"`
	Sequence    int    `json:"Sequence"`
	ClientState string `json:"ClientState"`
	Platform    string `json:"Platform"`
}

func (Hubtel) Parse(r *http.Request) (*Request, error) {
	var h hubtelRequest
	if err := decodeJSON(r, &h); err != nil {
		return nil, err
	}
	if h.SessionID == "" {
		return nil, fmt.Errorf("%w: SessionId is required", ErrBadRequest)
	}
	t := strings.ToLower(h.Type)
	return &Request{
		SessionID: h.SessionID,
		Phone:     h.Mobile,
		Network:   h.Operator,
		New:       t == "initiation",
		Input:     h.Message,
		Release:   t == "release" || t == "timeout",
		echo:      map[string]any{"SessionId": h.SessionID},
	}, nil
}

func (Hubtel) Write(w http.ResponseWriter, req *Request, reply engine.Reply) {
	typ, data, field := "Response", "menu", "text"
	switch {
	case reply.End:
		typ, data = "Release", "display"
	case reply.Kind == "input":
		data = "input"
		switch reply.InputType {
		case "digits":
			field = "number"
		case "amount":
			field = "decimal"
		case "phone_gh":
			field = "phone"
		}
	}
	writeJSON(w, map[string]any{
		"SessionId": req.echo["SessionId"],
		"Type":      typ,
		"Message":   reply.Message,
		"Label":     reply.Screen,
		"DataType":  data,
		"FieldType": field,
	})
}

// --- Nalo -------------------------------------------------------------------

type Nalo struct{}

func (Nalo) Name() string { return "nalo" }

type naloRequest struct {
	UserID    string   `json:"USERID"`
	MSISDN    string   `json:"MSISDN"`
	UserData  string   `json:"USERDATA"`
	MsgType   flexBool `json:"MSGTYPE"`
	Network   string   `json:"NETWORK"`
	SessionID string   `json:"SESSIONID"`
}

func (Nalo) Parse(r *http.Request) (*Request, error) {
	var n naloRequest
	if err := decodeJSON(r, &n); err != nil {
		return nil, err
	}
	id := n.SessionID
	if id == "" {
		id = n.MSISDN // Nalo's own sample code keys sessions on the phone number
	}
	if id == "" {
		return nil, fmt.Errorf("%w: SESSIONID or MSISDN is required", ErrBadRequest)
	}
	return &Request{
		SessionID: id,
		Phone:     n.MSISDN,
		Network:   n.Network,
		New:       bool(n.MsgType),
		Input:     n.UserData,
		echo:      map[string]any{"USERID": n.UserID, "MSISDN": n.MSISDN, "USERDATA": n.UserData},
	}, nil
}

func (Nalo) Write(w http.ResponseWriter, req *Request, reply engine.Reply) {
	writeJSON(w, map[string]any{
		"USERID":   req.echo["USERID"],
		"MSISDN":   req.echo["MSISDN"],
		"USERDATA": req.echo["USERDATA"],
		"MSG":      reply.Message,
		"MSGTYPE":  !reply.End,
	})
}

// flexBool accepts true/false, 1/0 and "true"/"1": Nalo's docs describe MSGTYPE as "BOOLEAN 1/0".
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch strings.Trim(strings.ToLower(string(data)), `"`) {
	case "true", "1":
		*b = true
	case "false", "0", "", "null":
		*b = false
	default:
		return fmt.Errorf("MSGTYPE must be true/false or 1/0, got %s", data)
	}
	return nil
}

// --- Arkesel ----------------------------------------------------------------

type Arkesel struct{}

func (Arkesel) Name() string { return "arkesel" }

type arkeselRequest struct {
	SessionID  string `json:"sessionID"`
	UserID     string `json:"userID"`
	NewSession bool   `json:"newSession"`
	MSISDN     string `json:"msisdn"`
	UserData   string `json:"userData"`
	Network    string `json:"network"`
}

func (Arkesel) Parse(r *http.Request) (*Request, error) {
	var a arkeselRequest
	if err := decodeJSON(r, &a); err != nil {
		return nil, err
	}
	if a.SessionID == "" {
		return nil, fmt.Errorf("%w: sessionID is required", ErrBadRequest)
	}
	return &Request{
		SessionID: a.SessionID,
		Phone:     a.MSISDN,
		Network:   a.Network,
		New:       a.NewSession,
		Input:     a.UserData,
		echo:      map[string]any{"sessionID": a.SessionID, "userID": a.UserID, "msisdn": a.MSISDN},
	}, nil
}

func (Arkesel) Write(w http.ResponseWriter, req *Request, reply engine.Reply) {
	// Arkesel requires sessionID, userID and msisdn to be returned exactly as received.
	writeJSON(w, map[string]any{
		"sessionID":       req.echo["sessionID"],
		"userID":          req.echo["userID"],
		"msisdn":          req.echo["msisdn"],
		"message":         reply.Message,
		"continueSession": !reply.End,
	})
}
