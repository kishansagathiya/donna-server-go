package localagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
)

type IPCMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type TokenPayload struct {
	AccessToken string `json:"access_token"`
}

type WorkspacePayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type WorkspacesPayload struct {
	Workspaces []WorkspacePayload `json:"workspaces"`
}

type StatusPayload struct {
	Worker   string `json:"worker"`
	DeviceID string `json:"device_id"`
	Paused   bool   `json:"paused"`
	Active   string `json:"active_run_id,omitempty"`
	Error    string `json:"error,omitempty"`
}

type IPC struct {
	conn   net.Conn
	secret string
	mu     sync.Mutex
	enc    *json.Encoder
}

func DialIPC(socketPath, secret string) (*IPC, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, err
	}
	ipc := &IPC{conn: conn, secret: secret, enc: json.NewEncoder(conn)}
	if err := ipc.Write(IPCMessage{
		Type:    "auth",
		Payload: mustJSON(map[string]string{"secret": secret}),
	}); err != nil {
		conn.Close()
		return nil, err
	}
	return ipc, nil
}

func ListenIPC(socketPath, secret string, handle func(IPCMessage) IPCMessage) error {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = os.Chmod(socketPath, 0o600)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go serveIPC(conn, secret, handle)
	}
}

func serveIPC(conn net.Conn, secret string, handle func(IPCMessage) IPCMessage) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	authed := false
	enc := json.NewEncoder(conn)
	for scanner.Scan() {
		var msg IPCMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if !authed {
			var body struct {
				Secret string `json:"secret"`
			}
			_ = json.Unmarshal(msg.Payload, &body)
			if msg.Type != "auth" || body.Secret != secret {
				_ = enc.Encode(IPCMessage{Type: "error", Payload: mustJSON(map[string]string{"error": "unauthorized"})})
				return
			}
			authed = true
			_ = enc.Encode(IPCMessage{Type: "ok"})
			continue
		}
		resp := handle(msg)
		if resp.Type == "" {
			resp.Type = "ok"
		}
		_ = enc.Encode(resp)
	}
}

func (i *IPC) Write(msg IPCMessage) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.enc.Encode(msg)
}

func (i *IPC) Close() error {
	if i == nil || i.conn == nil {
		return nil
	}
	return i.conn.Close()
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func ParsePayload[T any](msg IPCMessage) (T, error) {
	var out T
	if len(msg.Payload) == 0 {
		return out, fmt.Errorf("empty_payload")
	}
	err := json.Unmarshal(msg.Payload, &out)
	return out, err
}
