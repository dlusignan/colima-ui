// Command colima-ui runs the Colima UI backend on the loopback interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dlusignan/colima-ui/internal/httpapi"
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

	s := httpapi.NewServer()
	ln, err := s.Listen(port)
	if err != nil {
		return err
	}
	log.Printf("listening on http://%s", ln.Addr())

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
