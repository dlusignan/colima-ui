package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// loopbackHost is the only host the server ever binds to. It is deliberately
// an unexported constant so no configuration can widen the bind address.
const loopbackHost = "127.0.0.1"

// Server wraps an http.Server that listens on loopback only.
type Server struct {
	srv *http.Server
}

// NewServer builds a Server with its routes registered.
func NewServer() *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return &Server{srv: &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}}
}

// Listen binds 127.0.0.1:port. Port 0 picks a free port (used by tests).
func (s *Server) Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d out of range", port)
	}
	return net.Listen("tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
}

// Serve accepts connections on ln until Shutdown is called.
func (s *Server) Serve(ln net.Listener) error { return s.srv.Serve(ln) }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
