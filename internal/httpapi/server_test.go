package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func startServer(t *testing.T) (*Server, net.Listener, chan error) {
	t.Helper()
	s := NewServer("t", NewPlaceholderAPI(), nil)
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	return s, ln, done
}

func TestHealthz(t *testing.T) {
	s, ln, done := startServer(t)
	defer func() {
		_ = s.Shutdown(context.Background())
		<-done
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	s, ln, done := startServer(t)
	defer func() {
		_ = s.Shutdown(context.Background())
		<-done
	}()
	resp, err := http.Post("http://"+ln.Addr().String()+"/healthz", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestListenerIsLoopback(t *testing.T) {
	s, ln, done := startServer(t)
	defer func() {
		_ = s.Shutdown(context.Background())
		<-done
	}()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("addr type %T", ln.Addr())
	}
	if !addr.IP.IsLoopback() || addr.IP.IsUnspecified() {
		t.Fatalf("addr %v is not loopback", addr)
	}
	if addr.Port == 0 {
		t.Fatal("port not assigned")
	}
	_ = strconv.Itoa(addr.Port)
}

func TestListenRejectsBadPort(t *testing.T) {
	s := NewServer("t", NewPlaceholderAPI(), nil)
	for _, p := range []int{-1, 65536} {
		if ln, err := s.Listen(p); err == nil {
			ln.Close()
			t.Fatalf("port %d accepted", p)
		}
	}
}

func TestShutdown(t *testing.T) {
	s, _, done := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve returned %v", err)
	}
}
