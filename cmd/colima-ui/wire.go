package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// colimaRunner is the colima adapter as the wiring needs it: one-shot runs for
// reads and streams for jobs. *colima.ExecRunner satisfies both.
type colimaRunner interface {
	colima.Runner
	colima.Streamer
}

// app holds the backend components behind the API. Each issue owns one block
// below, marked by its sentinel comment and separated from the next by a blank
// line, so parallel changes touch no adjacent lines. Add a field, its
// construction in newApp, its route option in handler and its teardown in
// shutdown, each under your own sentinel.
type app struct {
	// #15 events

	// #16 profiles

	// #17 jobs
	runner colimaRunner
	mgr    *operations.Manager
	gate   httpapi.ProfileGate

	// #18 config

	// #23 metrics

	// #38 containers
	containers dockerc.Provider

	// #39 actions

	// #50 observers
}

// newApp builds the components. Add your construction under your sentinel.
func newApp() (*app, error) {
	runner, err := colima.NewExecRunner("")
	if err != nil {
		return nil, fmt.Errorf("the colima binary is required but could not be found on PATH: %w", err)
	}
	return newAppWith(runner, os.Getenv, os.UserHomeDir)
}

// newAppWith is newApp with its inputs injected, for tests.
func newAppWith(runner colimaRunner, getenv func(string) string, homeDir func() (string, error)) (*app, error) {
	a := &app{}

	// #15 events

	// #16 profiles

	// #17 jobs
	a.runner = runner
	home, _ := homeDir() // an empty home just narrows Locate to the env vars
	base, err := config.Locate(getenv, home)
	if err != nil {
		return nil, fmt.Errorf("locate colima folder: %w", err)
	}
	svc, err := config.New(base)
	if err != nil {
		return nil, fmt.Errorf("open colima folder: %w", err)
	}
	a.gate = profileGate{runner: runner, files: svc}
	a.mgr = operations.New(operations.Options{
		Launcher: operations.ColimaLauncher{S: runner},
		// #50 fills this single field with the observers.
		Observers: nil,
	})

	// #18 config

	// #23 metrics

	// #38 containers
	a.containers = dockerc.NewProvider(dockerc.NewColimaResolver(runner))

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
	opts = append(opts, httpapi.WithJobs(a.mgr, a.gate))

	// #18 config

	// #23 metrics

	// #38 containers
	opts = append(opts, httpapi.WithContainers(a.gate, a.containers))

	// #39 actions

	return httpapi.NewAPI(opts...)
}

// shutdown stops the components in reverse start order (the blocks below run
// from #39 back to #15) and joins their errors, so one failure never skips the
// rest. The HTTP server has already stopped when it runs.
func (a *app) shutdown(ctx context.Context) error {
	var errs []error

	// #39 actions

	// #38 containers

	// #23 metrics

	// #18 config

	// #17 jobs
	// Cancels running jobs and waits for their output pumps.
	if err := a.mgr.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("operations: %w", err))
	}

	// #16 profiles

	// #15 events

	return errors.Join(errs...)
}
