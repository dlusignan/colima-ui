package profiles

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
)

// Values of Profile.Source.
const (
	SourceBoth     = "both"
	SourceCLIOnly  = "cli_only"
	SourceFileOnly = "file_only"
)

// Values of Profile.Status.
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusUnknown = "unknown"
)

// Warning codes.
const (
	WarnColimaUnavailable        = "colima_unavailable"
	WarnConfigUnreadable         = "config_unreadable"
	WarnConfigInvalid            = "config_invalid"
	WarnColimaVersionUnsupported = "colima_version_unsupported"
)

// Warning is a non-fatal problem attached to a profile.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Profile is the merged view of one profile (docs/api.md 3.2 and 3.3).
// CPUs, MemoryBytes and DiskBytes are 0 and Runtime is "" when unknown.
type Profile struct {
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	Runtime       string    `json:"runtime"`
	CPUs          int       `json:"cpus"`
	MemoryBytes   int64     `json:"memoryBytes"`
	DiskBytes     int64     `json:"diskBytes"`
	ColimaVersion string    `json:"colimaVersion"`
	Source        string    `json:"source"`
	ActiveJobID   *string   `json:"activeJobId"`
	Warnings      []Warning `json:"warnings,omitempty"`
}

// BusyFunc reports the running job of a profile. operations.Manager.Busy has
// this signature.
type BusyFunc func(profile string) (jobID string, running bool)

// Service merges the two sources on every call. It holds no state.
type Service struct {
	colima ColimaSource
	cfg    ConfigSource
	busy   BusyFunc
}

// New builds a Service. A nil busy means no profile ever has an active job.
func New(colima ColimaSource, cfg ConfigSource, busy BusyFunc) *Service {
	return &Service{colima: colima, cfg: cfg, busy: busy}
}

// live is the result of the two Colima calls.
type live struct {
	instances []colima.Instance
	listErr   error
	version   colima.VersionInfo
	verErr    error
}

func (s *Service) fetch(ctx context.Context) live {
	var l live
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); l.instances, l.listErr = s.colima.List(ctx) }()
	go func() { defer wg.Done(); l.version, l.verErr = s.colima.Version(ctx) }()
	wg.Wait()
	return l
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ctxErr returns a context error from any of errs, or ctx.Err().
func ctxErr(ctx context.Context, errs ...error) error {
	for _, e := range errs {
		if e != nil && isCtxErr(e) {
			return e
		}
	}
	return ctx.Err()
}

func validName(name string) bool {
	return colima.ValidateProfile(name) == nil && config.ValidateProfileName(name) == nil
}

// List returns every profile known to Colima or to a configuration file,
// sorted by name. It returns *ColimaError only when Colima cannot be queried
// and there are no file profiles to show.
func (s *Service) List(ctx context.Context) ([]Profile, error) {
	l := s.fetch(ctx)
	names, listCfgErr := s.cfg.ListProfiles()
	if err := ctxErr(ctx, l.listErr, l.verErr); err != nil {
		return nil, err
	}

	insts := map[string]colima.Instance{}
	if l.listErr == nil {
		for _, in := range l.instances {
			if !validName(in.Name) {
				slog.Warn("dropping colima instance with unusable name")
				continue
			}
			insts[in.Name] = in
		}
	}
	files := map[string]bool{}
	if listCfgErr == nil {
		for _, n := range names {
			if validName(n) {
				files[n] = true
			}
		}
	}
	if l.listErr != nil && len(files) == 0 {
		return nil, &ColimaError{Err: l.listErr}
	}

	all := map[string]bool{}
	for n := range insts {
		all[n] = true
	}
	for n := range files {
		all[n] = true
	}
	out := make([]Profile, 0, len(all))
	for n := range all {
		in, listed := insts[n]
		var inp *colima.Instance
		if listed {
			inp = &in
		}
		var extra []Warning
		if listCfgErr != nil {
			extra = append(extra, Warning{WarnConfigUnreadable, "profile configuration folder could not be read"})
		}
		p, ok := s.build(n, inp, files[n], l, extra)
		if ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one profile. It returns ErrNotFound for an invalid or unknown
// name, and *ColimaError when Colima cannot be queried and no file exists.
func (s *Service) Get(ctx context.Context, name string) (Profile, error) {
	if !validName(name) {
		return Profile{}, ErrNotFound
	}
	l := s.fetch(ctx)
	if err := ctxErr(ctx, l.listErr, l.verErr); err != nil {
		return Profile{}, err
	}
	var inp *colima.Instance
	if l.listErr == nil {
		for _, in := range l.instances {
			if in.Name == name {
				in := in
				inp = &in
				break
			}
		}
	}
	// The file is read inside build; probe existence cheaply by letting build
	// report whether the profile exists on either side.
	p, ok := s.build(name, inp, true, l, nil)
	if !ok {
		if l.listErr != nil {
			return Profile{}, &ColimaError{Err: l.listErr}
		}
		return Profile{}, ErrNotFound
	}
	return p, nil
}

// build merges one profile. mayHaveFile says a file read should be tried. It
// reports false when neither side knows the profile.
func (s *Service) build(name string, in *colima.Instance, mayHaveFile bool, l live, warns []Warning) (Profile, bool) {
	var cp *config.Profile
	var cfgErr bool
	if mayHaveFile {
		var err error
		cp, err = s.cfg.Read(name)
		switch {
		case err == nil:
		case isCtxErr(err), errors.Is(err, config.ErrProfileNotFound), errors.Is(err, config.ErrInvalidProfileName):
			cp = nil
		default:
			cfgErr = true
		}
	}
	hasFile := cp != nil || cfgErr
	if in == nil && !hasFile {
		return Profile{}, false
	}

	p := Profile{Name: name, Warnings: warns}
	switch {
	case in != nil && hasFile:
		p.Source = SourceBoth
	case in != nil:
		p.Source = SourceCLIOnly
	default:
		p.Source = SourceFileOnly
	}

	switch {
	case in != nil:
		switch {
		case strings.EqualFold(in.Status, colima.StatusRunning):
			p.Status = StatusRunning
		case strings.EqualFold(in.Status, colima.StatusStopped):
			p.Status = StatusStopped
		default:
			p.Status = StatusUnknown
		}
	case l.listErr != nil:
		p.Status = StatusUnknown
	default:
		p.Status = StatusStopped
	}

	if in != nil {
		p.Runtime, p.CPUs, p.MemoryBytes, p.DiskBytes = in.Runtime, in.CPUs, in.Memory, in.Disk
	}
	if cp != nil {
		if cp.Runtime != "" && (in == nil || in.Runtime == "") {
			p.Runtime = cp.Runtime
		}
		if cp.CPU != nil {
			p.CPUs = *cp.CPU
		}
		if cp.Memory != nil {
			p.MemoryBytes = int64(math.Round(*cp.Memory * (1 << 30)))
		}
		if cp.Disk != nil {
			p.DiskBytes = int64(*cp.Disk) << 30
		}
	}

	if l.verErr == nil {
		p.ColimaVersion = normalizeVersion(l.version.Number)
		v := l.version
		if !v.Semver || v.Major != 0 || v.Minor != 10 {
			p.Warnings = append(p.Warnings, Warning{WarnColimaVersionUnsupported, "Colima version " + p.ColimaVersion + " is outside the tested 0.10.x line"})
		}
	}
	if l.listErr != nil {
		p.Warnings = append(p.Warnings, Warning{WarnColimaUnavailable, "Colima could not be queried; showing configuration only"})
	}
	if cfgErr {
		p.Warnings = append(p.Warnings, Warning{WarnConfigInvalid, "profile configuration file could not be read"})
	}

	if s.busy != nil {
		if id, running := s.busy(name); running {
			p.ActiveJobID = &id
		}
	}
	return p, true
}

func normalizeVersion(n string) string {
	n = strings.TrimPrefix(n, "v")
	n, _, _ = strings.Cut(n, "+")
	return n
}
