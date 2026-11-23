// Command colima-ui runs the Colima UI backend on the loopback interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/web"
)

const (
	defaultPort     = 8787
	shutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, getenv func(string) string) error {
	port := defaultPort
	if v := getenv("COLIMA_UI_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("COLIMA_UI_PORT: %w", err)
		}
		port = p
	}
	fs := flag.NewFlagSet("colima-ui", flag.ContinueOnError)
	fs.IntVar(&port, "port", port, "TCP port to listen on (127.0.0.1 only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range 1-65535", port)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	token, err := httpapi.NewToken()
	if err != nil {
		return err
	}
	s := httpapi.NewServer(token, httpapi.NewPlaceholderAPI(), web.UI())
	ln, err := s.Listen(port)
	if err != nil {
		return err
	}
	log.Printf("listening on http://%s", ln.Addr())
	// The token is printed with fmt, never the logger, as a URL fragment:
	// browsers do not send fragments to the server, so it stays out of access
	// logs and Referer. This is the one place it leaves the process; if the
	// binary ever runs as a login item, stdout capture needs care.
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		fmt.Printf("open http://127.0.0.1:%d/#token=%s\n", tcp.Port, token)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Print("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.Shutdown(sctx); err != nil {
		return err
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
