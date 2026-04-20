// Package daemon implements intentd: a small supervisor that keeps a
// llamafile --server process warm so the CLI doesn't pay a model-load
// cost on every invocation.
//
// Architecture (v1):
//
//   - The daemon spawns llamafile as a subprocess and watches it.
//   - llamafile exposes its OpenAI-compatible HTTP API on the loopback
//     port from config. The CLI talks to that port directly. The daemon
//     does NOT proxy inference traffic through its Unix socket — that
//     would add a hop for no benefit, since the heavy lifting is the
//     model, not the network.
//   - The daemon owns a Unix socket on which it speaks a tiny line-
//     delimited JSON control protocol: ping / status / stop. That's
//     also how `i daemon status` and `i daemon stop` work.
//
// Idle unload (kill the llamafile subprocess after N minutes of HTTP
// inactivity, respawn on next CLI request) is a v1.x follow-up. In v1
// the daemon stays warm until the user stops it.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// Op is the daemon control-protocol operation discriminator.
type Op string

const (
	OpPing   Op = "ping"
	OpStatus Op = "status"
	OpStop   Op = "stop"
)

// Request is one daemon control request.
type Request struct {
	Op Op     `json:"op"`
	ID string `json:"id,omitempty"`
}

// Response is the daemon's reply.
type Response struct {
	ID    string         `json:"id,omitempty"`
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// Server is the Unix-socket control-plane server.
type Server struct {
	Socket    string
	Launcher  *Launcher
	Started   time.Time
	mu        sync.Mutex
	ln        net.Listener
	stopCh    chan struct{}
	stopOnce  sync.Once
	clientCtx context.Context
}

// New constructs a Server bound to socket and supervising launcher.
func New(socket string, l *Launcher) *Server {
	return &Server{
		Socket:   socket,
		Launcher: l,
		stopCh:   make(chan struct{}),
	}
}

// Listen binds the Unix socket. Any pre-existing socket file is removed.
func (s *Server) Listen() error {
	_ = os.Remove(s.Socket)
	if err := os.MkdirAll(parentDir(s.Socket), 0o700); err != nil {
		return fmt.Errorf("mkdir socket parent: %w", err)
	}
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Socket, err)
	}
	if err := os.Chmod(s.Socket, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.mu.Lock()
	s.ln = ln
	s.Started = time.Now()
	s.mu.Unlock()
	return nil
}

// Serve accepts connections until ctx is canceled OR an OpStop is received.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("server not listening")
	}
	s.clientCtx = ctx
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stopCh:
		}
		_ = s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return nil
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

// SignalStop tells Serve to return. Idempotent.
func (s *Server) SignalStop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// Stopped returns a channel closed when SignalStop has been called.
// `i daemon start` blocks on this AND on its OS-signal context, so
// either source can shut the daemon down.
func (s *Server) Stopped() <-chan struct{} { return s.stopCh }

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeJSONLine(w, Response{ID: req.ID, OK: false, Error: "bad json: " + err.Error()})
		return
	}
	resp := s.dispatch(req)
	resp.ID = req.ID
	_ = writeJSONLine(w, resp)
}

func (s *Server) dispatch(req Request) Response {
	switch req.Op {
	case OpPing:
		return Response{OK: true, Data: map[string]any{"pong": true}}
	case OpStatus:
		data := map[string]any{
			"socket":     s.Socket,
			"started_at": s.Started.UTC().Format(time.RFC3339),
			"uptime_sec": int64(time.Since(s.Started).Seconds()),
		}
		if s.Launcher != nil {
			data["llamafile_running"] = s.Launcher.Running()
			data["llamafile_endpoint"] = s.Launcher.Endpoint()
			data["llamafile_pid"] = s.Launcher.PID()
			data["llamafile_restarts"] = s.Launcher.Restarts()
			data["model"] = s.Launcher.ModelPath
		}
		return Response{OK: true, Data: data}
	case OpStop:
		s.SignalStop()
		return Response{OK: true, Data: map[string]any{"stopping": true}}
	default:
		return Response{OK: false, Error: "unknown op: " + string(req.Op)}
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

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
