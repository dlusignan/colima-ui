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
	srv   *http.Server
	token string
	api   http.Handler
}

// NewServer builds a Server that protects api with token. The handler is
// assembled in Serve, once the real listener port is known.
func NewServer(token string, api http.Handler) *Server {
	return &Server{
		token: token,
		api:   api,
		srv:   &http.Server{ReadHeaderTimeout: 5 * time.Second},
	}
}

// NewHandler is a pure function that builds the full handler. port is the
// real listener port. Every request passes the Host and Origin checks;
// /healthz needs no token; every /api route requires the bearer token.
func NewHandler(port int, token string, api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	guarded := requireToken(token, api)
	mux.Handle("/api", guarded)
	mux.Handle("/api/", guarded)
	return hostOriginGuard(port, mux)
}

// NewPlaceholderAPI returns a stub /api handler so the middleware has
// something to protect. Remove it once real routes land.
func NewPlaceholderAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

// Listen binds 127.0.0.1:port. Port 0 picks a free port (used by tests).
func (s *Server) Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d out of range", port)
	}
	return net.Listen("tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
}

// Serve accepts connections on ln until Shutdown is called.
func (s *Server) Serve(ln net.Listener) error {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("listener address %T is not TCP", ln.Addr())
	}
	s.srv.Handler = NewHandler(addr.Port, s.token, s.api)
	return s.srv.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
