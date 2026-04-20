// Package daemon contains the unix-socket server scaffolding. v1 wires the
// protocol shape (line-delimited JSON, request/response envelope) and a
// minimal in-memory engine handler. The full Phase 4 deliverable (model
// keepalive, multi-client streaming, idle unload) lives in the next iteration.
//
// This package is intentionally small in v1 so the CLI can already speak the
// protocol against a future daemon without changes when it lands.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
)

// Op is the request operation discriminator.
type Op string

const (
	OpComplete   Op = "complete"
	OpToolResult Op = "tool_result"
	OpHealth     Op = "health"
	OpFlushCache Op = "flush_cache"
)

// Request is one daemon request.
type Request struct {
	Op      Op              `json:"op"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is one daemon response. Multiple may be emitted per request,
// terminated by one with Type="final".
type Response struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Final   bool            `json:"final,omitempty"`
}

// Server is the unix-socket server.
type Server struct {
	Socket  string
	Handler Handler
	mu      sync.Mutex
	ln      net.Listener
}

// Handler is the per-request callback.
type Handler interface {
	Handle(ctx context.Context, req Request, emit func(Response) error) error
}

// Listen binds to the socket. Existing socket file is removed (best-effort).
func (s *Server) Listen() error {
	_ = os.Remove(s.Socket)
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	return nil
}

// Serve accepts connections until ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("server not listening")
	}
	go func() { <-ctx.Done(); _ = s.ln.Close() }()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeJSONLine(w, Response{ID: req.ID, Type: "error", Final: true,
			Payload: jsonMessage(err.Error())})
		return
	}
	if s.Handler == nil {
		_ = writeJSONLine(w, Response{ID: req.ID, Type: "error", Final: true,
			Payload: jsonMessage("no handler")})
		return
	}
	emit := func(resp Response) error {
		resp.ID = req.ID
		return writeJSONLine(w, resp)
	}
	if err := s.Handler.Handle(ctx, req, emit); err != nil {
		_ = emit(Response{Type: "error", Final: true, Payload: jsonMessage(err.Error())})
	}
}

func writeJSONLine(w *bufio.Writer, r Response) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

func jsonMessage(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"message": s})
	return b
}
