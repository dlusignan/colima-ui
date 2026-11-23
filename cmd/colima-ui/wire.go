package main

import (
	"context"
	"net/http"

	"github.com/dlusignan/colima-ui/internal/httpapi"
)

// app holds the backend components behind the API. Each issue owns one block
// below, marked by its sentinel comment and separated from the next by a blank
// line, so parallel changes touch no adjacent lines. Add a field, its
// construction in newApp, its route option in handler and its teardown in
// shutdown, each under your own sentinel.
type app struct {
	// #15 events

	// #16 profiles

	// #17 jobs

	// #18 config

	// #23 metrics

	// #38 containers

	// #39 actions

	// #50 observers
}

// newApp builds the components. Add your construction under your sentinel.
func newApp() (*app, error) {
	a := &app{}

	// #15 events

	// #16 profiles

	// #17 jobs

	// #18 config

	// #23 metrics

	// #38 containers

	// #39 actions

	// #50 observers

	return a, nil
}

// handler returns the /api handler with every route group registered. Append
// your option under your sentinel.
func (a *app) handler() http.Handler {
	var opts []httpapi.Option

	// #15 events

	// #16 profiles

	// #17 jobs

	// #18 config

	// #23 metrics

	// #38 containers

	// #39 actions

	return httpapi.NewAPI(opts...)
}

// shutdown stops the components, in reverse start order. The HTTP server has
// already stopped when it runs.
func (a *app) shutdown(ctx context.Context) error {
	// #15 events

	// #16 profiles

	// #17 jobs

	// #18 config

	// #23 metrics

	// #38 containers

	// #39 actions

	return nil
}
