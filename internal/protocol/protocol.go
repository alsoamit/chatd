// Package protocol mirrors the wire format defined in
// github.com/cedrx/chatrelay/internal/protocol. We duplicate the
// definitions verbatim so the two modules can be developed and shipped
// independently without a shared package or git submodule. Keep the
// fields in lock-step.
package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TypeAuth            = "auth"
	TypeAuthOK          = "auth_ok"
	TypeAuthError       = "auth_error"
	TypeHeartbeat       = "heartbeat"
	TypePresence        = "presence"
	TypeUserList        = "user_list"
	TypeMessageSend     = "message_send"
	TypeMessageRecv     = "message_recv"
	TypeMessageAck      = "message_ack"
	TypeHistoryRequest  = "history_request"
	TypeHistoryResponse = "history_response"
	TypeTyping          = "typing"
	TypeTypingRecv      = "typing_recv"
	TypeError           = "error"
)

const MaxBodyBytes = 8 * 1024
const MaxUsernameBytes = 64

type Envelope struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"`
}

func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Envelope{}, err
	}
	if head.Type == "" {
		return Envelope{}, errors.New("protocol: missing type")
	}
	return Envelope{Type: head.Type, Raw: append([]byte(nil), data...)}, nil
}

type Auth struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

type AuthOK struct {
	Type  string `json:"type"`
	Users []User `json:"users"`
	Self  string `json:"self"`
}

type AuthError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type User struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type Heartbeat struct {
	Type string `json:"type"`
}

type Presence struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Online   bool   `json:"online"`
}

type UserList struct {
	Type  string `json:"type"`
	Users []User `json:"users"`
}

type MessageSend struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	To   string `json:"to"`
	Body string `json:"body"`
}

type MessageRecv struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Body string `json:"body"`
	TS   int64  `json:"ts"`
}

type MessageAck struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Delivered bool   `json:"delivered"`
}

type HistoryRequest struct {
	Type  string `json:"type"`
	Peer  string `json:"peer"`
	Limit int    `json:"limit"`
}

type HistoryResponse struct {
	Type     string        `json:"type"`
	Peer     string        `json:"peer"`
	Messages []MessageRecv `json:"messages"`
}

type Typing struct {
	Type   string `json:"type"`
	To     string `json:"to"`
	Active bool   `json:"active"`
}

type TypingRecv struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	Active bool   `json:"active"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func ValidateUsername(name string) error {
	if name == "" {
		return errors.New("username empty")
	}
	if len(name) > MaxUsernameBytes {
		return errors.New("username too long")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return errors.New("username contains disallowed character")
		}
	}
	return nil
}

func ValidateBody(body string) (string, error) {
	if body == "" {
		return "", errors.New("body empty")
	}
	if len(body) > MaxBodyBytes {
		return "", errors.New("body too large")
	}
	if !utf8.ValidString(body) {
		return "", errors.New("body not utf-8")
	}
	body = strings.TrimRight(body, " \t\r\n")
	if body == "" {
		return "", errors.New("body whitespace only")
	}
	return body, nil
}

func ConversationKey(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}

func NowMillis() int64 { return time.Now().UnixMilli() }
