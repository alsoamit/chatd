// Package ipc defines the local NDJSON protocol over a Unix domain
// socket used by the daemon and its UI processes (chat dashboard,
// per-conversation chat-client). Frames are JSON objects, one per line.
package ipc

import (
	"encoding/json"
	"errors"
)

const (
	OpHello       = "hello"
	OpHelloOK     = "hello_ok"
	OpSend        = "send"
	OpHistory     = "history"
	OpHistoryData = "history_data"
	OpUsers       = "users"
	OpUsersData   = "users_data"
	OpOpen        = "open"
	OpPing        = "ping"
	OpPong        = "pong"
	OpMessage     = "message"
	OpPresence    = "presence"
	OpUnread      = "unread"
	OpAck         = "ack"
	OpError       = "error"
	OpReadAll     = "read_all"
	OpStatus      = "status"
	OpStatusData  = "status_data"
	OpTyping      = "typing"
	OpTypingFrom  = "typing_from"
	OpQuit        = "quit"
)

const (
	RoleDashboard    = "dashboard"
	RoleConversation = "conversation"
	RoleControl      = "control"
)

// Hello announces the role of the connecting client. For role
// "conversation" the Peer field selects which conversation to subscribe
// to.
type Hello struct {
	Op   string `json:"op"`
	Role string `json:"role"`
	Peer string `json:"peer,omitempty"`
}

type HelloOK struct {
	Op       string `json:"op"`
	Self     string `json:"self"`
	RelayUp  bool   `json:"relay_up"`
	Username string `json:"username"`
}

type Send struct {
	Op   string `json:"op"`
	Peer string `json:"peer"`
	Body string `json:"body"`
}

type HistoryReq struct {
	Op   string `json:"op"`
	Peer string `json:"peer"`
}

type HistoryData struct {
	Op       string    `json:"op"`
	Peer     string    `json:"peer"`
	Messages []Message `json:"messages"`
}

type UsersReq struct {
	Op string `json:"op"`
}

type UsersData struct {
	Op    string    `json:"op"`
	Users []UserRow `json:"users"`
}

type UserRow struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Unread uint64 `json:"unread"`
}

type OpenReq struct {
	Op   string `json:"op"`
	Peer string `json:"peer"`
}

type Message struct {
	Op   string `json:"op"`
	Peer string `json:"peer"`
	From string `json:"from"`
	Body string `json:"body"`
	TS   int64  `json:"ts"`
	ID   string `json:"id"`
	Self bool   `json:"self"`
}

type Presence struct {
	Op     string `json:"op"`
	User   string `json:"user"`
	Online bool   `json:"online"`
}

type Unread struct {
	Op    string `json:"op"`
	Peer  string `json:"peer"`
	Count uint64 `json:"count"`
}

type Ack struct {
	Op        string `json:"op"`
	ID        string `json:"id"`
	Delivered bool   `json:"delivered"`
}

type Error struct {
	Op      string `json:"op"`
	Message string `json:"message"`
}

type Ping struct {
	Op string `json:"op"`
}
type Pong struct {
	Op string `json:"op"`
}

type ReadAll struct {
	Op   string `json:"op"`
	Peer string `json:"peer"`
}

type StatusReq struct {
	Op string `json:"op"`
}

type StatusData struct {
	Op       string `json:"op"`
	Username string `json:"username"`
	RelayURL string `json:"relay_url"`
	RelayUp  bool   `json:"relay_up"`
	Peers    int    `json:"peers"`
}

type Typing struct {
	Op     string `json:"op"`
	Peer   string `json:"peer"`
	Active bool   `json:"active"`
}

type TypingFrom struct {
	Op     string `json:"op"`
	From   string `json:"from"`
	Active bool   `json:"active"`
}

type Quit struct {
	Op string `json:"op"`
}

// PeekOp returns just the "op" field of a JSON line.
func PeekOp(data []byte) (string, error) {
	var head struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	if head.Op == "" {
		return "", errors.New("ipc: missing op")
	}
	return head.Op, nil
}
