package macuake

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer accepts one connection, reads one JSON line, returns the canned
// response for that action. Multiple actions can be queued.
type fakeServer struct {
	t        *testing.T
	socket   string
	ln       net.Listener
	requests []map[string]any
	respond  func(map[string]any) map[string]any
}

func newFake(t *testing.T, respond func(map[string]any) map[string]any) *fakeServer {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "macuake.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeServer{t: t, socket: socket, ln: ln, respond: respond}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeServer) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeServer) handle(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req map[string]any
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	f.requests = append(f.requests, req)
	resp := f.respond(req)
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}

func TestNewTabHappyPath(t *testing.T) {
	srv := newFake(t, func(req map[string]any) map[string]any {
		return map[string]any{"ok": true, "session_id": "abc-123"}
	})
	c := New(srv.socket, time.Second)
	sid, err := c.NewTab("/tmp")
	if err != nil {
		t.Fatalf("new-tab: %v", err)
	}
	if sid != "abc-123" {
		t.Fatalf("got %q want abc-123", sid)
	}
	if got := srv.requests[0]["action"]; got != "new-tab" {
		t.Fatalf("action %v", got)
	}
	if got := srv.requests[0]["directory"]; got != "/tmp" {
		t.Fatalf("directory %v", got)
	}
}

func TestCallRejectsOkFalse(t *testing.T) {
	srv := newFake(t, func(req map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": "unknown action: foo"}
	})
	c := New(srv.socket, time.Second)
	err := c.Focus("x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("error %v", err)
	}
}

func TestSocketMissingReturnsErrUnavailable(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "nope.sock"), 100*time.Millisecond)
	_, err := c.NewTab("")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestList(t *testing.T) {
	srv := newFake(t, func(req map[string]any) map[string]any {
		return map[string]any{
			"ok": true,
			"tabs": []any{
				map[string]any{"index": 0.0, "session_id": "s1", "title": "t1", "cwd": "/a", "active": true},
				map[string]any{"index": 1.0, "session_id": "s2", "title": "t2", "cwd": "/b", "active": false},
			},
		}
	})
	c := New(srv.socket, time.Second)
	tabs, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 2 || tabs[0].SessionID != "s1" || !tabs[0].Active || tabs[1].CWD != "/b" {
		t.Fatalf("got %+v", tabs)
	}
}

func TestIsNotFound(t *testing.T) {
	srv := newFake(t, func(req map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": "session not found: xyz"}
	})
	c := New(srv.socket, time.Second)
	err := c.CloseSession("xyz")
	if !IsNotFound(err) {
		t.Fatalf("want IsNotFound true, got %v", err)
	}
}
