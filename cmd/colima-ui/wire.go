package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/actionlog"
	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/dockerc"
	"github.com/dlusignan/colima-ui/internal/events"
	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/internal/operations"
	"github.com/dlusignan/colima-ui/internal/profiles"
	"github.com/dlusignan/colima-ui/internal/sampler"
	"github.com/dlusignan/colima-ui/internal/store"
)

// colimaRunner is the colima adapter as the wiring needs it: one-shot runs for
// reads and streams for jobs. *colima.ExecRunner satisfies both.
type colimaRunner interface {
	colima.Runner
	colima.Streamer
}

// vmProbeTimeout bounds the `colima list` call behind a config write.
const vmProbeTimeout = 5 * time.Second

// app holds the backend components behind the API. Each issue owns one block
// below, marked by its sentinel comment and separated from the next by a blank
// line, so parallel changes touch no adjacent lines. Add a field, its
// construction in newApp, its route option in handler and its teardown in
// shutdown, each under your own sentinel.
type app struct {
	// #15 events
	hub *events.Hub

	// #22 history
	store *store.Store
	rec   *actionlog.Recorder

	// #16 profiles
	profiles *profiles.Service

	// #17 jobs
	runner colimaRunner
	mgr    *operations.Manager
	gate   httpapi.ProfileGate
	// creator is the #97 create route's backend.
	creator httpapi.ProfileCreator

	// #18 config
	configSvc *config.Service

	// #23 metrics
	sampler *sampler.Sampler
	// refresh asks the sampler to re-read the profile list (a field so tests can observe it).
	refresh func()

	// #38 containers
	containers dockerc.Provider

	// #39 actions

	// #50 observers
}

// newApp builds the components and starts the background loops (the metrics
// sampler and the history cleanup). newAppWith does not start them, so tests
// never tick.
func newApp() (*app, error) {
	runner, err := colima.NewExecRunner("")
	if err != nil {
		return nil, fmt.Errorf("the colima binary is required but could not be found on PATH: %w", err)
	}
	a, err := newAppWith(runner, os.Getenv, os.UserHomeDir, os.UserConfigDir)
	if err != nil {
		return nil, err
	}
	if err := a.start(); err != nil {
		_ = a.shutdown(context.Background())
		return nil, err
	}
	return a, nil
}

// start launches the background loops. They stop in shutdown.
func (a *app) start() error {
	if err := a.store.Start(context.Background()); err != nil {
		return fmt.Errorf("start history cleanup: %w", err)
	}
	a.sampler.Start(context.Background())
	return nil
}

// closeStreams ends every open event stream and rejects new ones. main calls it
// before http.Server.Shutdown, which would otherwise wait for the streams.
func (a *app) closeStreams() { a.hub.Shutdown() }

// newAppWith is newApp with its inputs injected, for tests. On error it tears
// down whatever it already opened, so a failed start leaks no goroutine or file.
func newAppWith(runner colimaRunner, getenv func(string) string, homeDir, userConfigDir func() (string, error)) (_ *app, err error) {
	a := &app{}
	defer func() {
		if err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = a.shutdown(ctx)
		}
	}()

	// #15 events
	a.hub = events.New()

	// #22 history
	// The history file is fatal when it cannot be opened, including a schema
	// newer than this binary. It holds history only, so the message says it can
	// be deleted. Opened before anything that writes to it.
	dbPath, err := store.DefaultPath(getenv, userConfigDir)
	if err != nil {
		return nil, fmt.Errorf("locate history database: %w", err)
	}
	a.store, err = store.Open(context.Background(), store.Options{
		Path: dbPath,
		OnCleanupError: func(err error) {
			slog.Warn("history cleanup failed", "error", err)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open history database (it holds history only and can be deleted, or move it aside, to start fresh): %w", err)
	}
	a.rec = actionlog.New(a.store)

	// #16 profiles
	// Built before the #17 block, so Busy is read lazily through a.mgr and the
	// service gets its own config service over the same folder.
	profHome, _ := homeDir()
	profBase, err := config.Locate(getenv, profHome)
	if err != nil {
		return nil, fmt.Errorf("locate colima folder: %w", err)
	}
	profCfg, err := config.New(profBase)
	if err != nil {
		return nil, fmt.Errorf("open colima folder: %w", err)
	}
	a.profiles = profiles.New(profiles.NewColimaSource(runner), profCfg,
		func(p string) (string, bool) { return a.mgr.Busy(p) })

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
	gate := profileGate{runner: runner, files: svc, folders: svc}
	a.gate = gate
	// One set of host caps for the config PUT (its default) and for creation.
	lim := config.DefaultLimits()
	a.mgr = operations.New(operations.Options{
		Launcher: operations.ColimaLauncher{
			S: runner, Limits: colimaLimits(lim),
			// A delete re-reads the live status inside the profile slot.
			VMStatus: gate.ProfileStatusForDelete,
		},
		// Called in order from the job's pump, none of them blocks: the action
		// log queues, the hub never blocks, and Refresh only signals. The
		// sampler is built below, so it is read lazily like a.mgr above.
		Observers: []operations.Observer{
			a.rec,
			newHubObserver(a.hub),
			refreshObserver{refresh: func() { a.refresh() }},
		},
	})

	a.creator = profileCreator{mgr: a.mgr, exists: gate.ProfileExists, limits: lim}

	// #18 config
	// A second Service with the gate: the one above only lists profiles and
	// has no way to write. The probe over-reports on error, by design in Apply.
	a.configSvc, err = config.New(base,
		config.WithGate(a.mgr),
		config.WithVMRunning(func(profile string) (bool, error) {
			ctx, cancel := context.WithTimeout(context.Background(), vmProbeTimeout)
			defer cancel()
			status, err := a.gate.ProfileStatus(ctx, profile)
			return strings.EqualFold(status, "running"), err
		}),
		config.WithOnWritten(a.rec.RecordConfigChange))
	if err != nil {
		return nil, fmt.Errorf("open colima folder for writes: %w", err)
	}

	// #23 metrics
	// a.containers is built here, ahead of its #38 block, because the sources
	// need it. The cache is shared, so one client per profile serves both.
	a.containers = dockerc.NewProvider(dockerc.NewColimaResolver(runner))
	cache := sampler.NewClientCache(a.containers, sampler.RealClock{})
	a.sampler = sampler.New(
		sampler.ColimaLister{Runner: runner},
		[]sampler.Source{sampler.NewContainerSource(cache), sampler.NewDiskSource(cache, sampler.RealClock{})},
		a.store, a.hub, sampler.Options{})

	a.refresh = a.sampler.Refresh

	// #38 containers
	// a.containers is assigned in the #23 block above.

	// #39 actions

	// #50 observers
	// Assembled in the #17 block, where the manager takes them.

	return a, nil
}

// handler returns the /api handler with every route group registered. Append
// your option under your sentinel.
func (a *app) handler() http.Handler {
	var opts []httpapi.Option

	// #15 events
	opts = append(opts, httpapi.WithEvents(a.hub.Handler()))

	// #16 profiles
	opts = append(opts, httpapi.WithProfiles(a.profiles))

	// #17 jobs
	opts = append(opts, httpapi.WithJobs(a.mgr, a.gate))

	// #97 create
	opts = append(opts, httpapi.WithCreate(a.creator))

	// #18 config
	opts = append(opts, httpapi.WithConfig(a.configSvc, a.mgr))

	// #23 metrics
	opts = append(opts, httpapi.WithMetrics(a.store, a.gate))

	// #38 containers
	opts = append(opts, httpapi.WithContainers(a.gate, a.containers))

	// #79 images (#119 adds the write routes, which need the busy lookup and the action log)
	opts = append(opts, httpapi.WithImages(a.gate, a.containers, a.mgr, a.store))

	// #120 image pull (a task job on the manager: it never takes the profile
	// slot, and the manager's observers write its action log row and events)
	opts = append(opts, httpapi.WithImagePull(a.gate, a.containers, a.mgr, a.mgr))

	// #142 cache prune (a task job on the manager that runs the colima adapter:
	// it takes no profile slot, and the manager's observers write its action
	// log row and events)
	opts = append(opts, httpapi.WithCachePrune(a.mgr, func(includeLima bool) operations.TaskFunc {
		return operations.CachePruneTask(a.runner, includeLima)
	}))

	// #80 volumes and networks (#117 and #118 add the write routes, which need
	// the job manager's busy lookup and the action log)
	opts = append(opts, httpapi.WithVolumes(a.gate, a.containers, a.mgr, a.store))
	opts = append(opts, httpapi.WithNetworks(a.gate, a.containers, a.mgr, a.store))

	// #81 engine
	opts = append(opts, httpapi.WithEngine(a.gate, a.containers))

	// #86 logs
	opts = append(opts, httpapi.WithLogs(a.gate, a.containers))

	// #138 ssh-config (a read through the colima adapter: no busy check, no action log)
	opts = append(opts, httpapi.WithSSHConfig(a.gate, sshConfigSource{r: a.runner}))

	// #39 actions
	opts = append(opts, httpapi.WithActions(a.store, a.gate))

	return httpapi.NewAPI(opts...)
}

// shutdown stops the components in reverse start order (the blocks below run
// from #39 back to #15) and joins their errors, so one failure never skips the
// rest. The HTTP server has already stopped when it runs, and main has already
// called closeStreams. The order that matters: the sampler stops before the
// manager and the store (it writes samples), the manager stops before the
// recorder (its Shutdown waits for every pump, so every final job state has
// been queued), and the store closes last. It also copes with a partly built
// app, which newAppWith tears down after a failed start.
func (a *app) shutdown(ctx context.Context) error {
	var errs []error

	// #39 actions

	// #38 containers

	// #23 metrics
	if a.sampler != nil {
		a.sampler.Stop()
	}

	// #18 config

	// #17 jobs
	// Cancels running jobs and waits for their output pumps.
	if a.mgr != nil {
		if err := a.mgr.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("operations: %w", err))
		}
	}

	// #22 history
	// After #17, so the recorder sees every finished job, and last of the
	// writers: the store closes only after the recorder has drained.
	if a.rec != nil {
		if err := a.rec.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("action log: %w", err))
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("history: %w", err))
		}
	}

	// #16 profiles

	// #15 events
	if a.hub != nil {
		a.hub.Shutdown()
	}

	return errors.Join(errs...)
}

// sshConfigSource adapts the colima adapter to httpapi.SSHConfigSource.
type sshConfigSource struct{ r colima.Runner }

func (s sshConfigSource) SSHConfig(ctx context.Context, name string) (httpapi.SSHConfigDoc, error) {
	cfg, err := colima.ReadSSHConfig(ctx, s.r, name)
	var exit *colima.ExitError
	var parse *colima.ParseError
	switch {
	case errors.As(err, &exit):
		return httpapi.SSHConfigDoc{}, httpapi.ErrSSHConfigRefused
	case errors.As(err, &parse):
		return httpapi.SSHConfigDoc{}, httpapi.ErrSSHConfigUnparseable
	case err != nil:
		return httpapi.SSHConfigDoc{}, err
	}
	doc := httpapi.SSHConfigDoc{Raw: cfg.Raw, Entries: make([]httpapi.SSHConfigEntry, 0, len(cfg.Entries))}
	for _, e := range cfg.Entries {
		doc.Entries = append(doc.Entries, httpapi.SSHConfigEntry{Key: e.Key, Value: e.Value})
	}
	return doc, nil
}
