package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// ConfigService is the Config Service as the config routes see it.
// *config.Service satisfies it.
type ConfigService interface {
	Read(name string) (*config.Profile, error)
	Apply(name string, ch config.Changes) ([]config.Change, error)
}

// ConfigBusy looks up the job holding a profile, so a 409 can name it.
// *operations.Manager satisfies it (it is the same lookup JobsDeps has).
type ConfigBusy interface {
	Busy(profile string) (jobID string, running bool)
}

// Colima's own defaults for keys a profile file leaves out, so the editor
// always has a number to show.
const (
	defaultCPU       = 2
	defaultMemoryGiB = 2.0
	defaultDiskGiB   = 100
)

// wireFieldNames maps the Config Service's field names to the contract's. It is
// the only place the two vocabularies meet: restartRequired and the 422
// details.fields both go through it.
var wireFieldNames = map[string]string{
	"cpu":    "cpu",
	"memory": "memoryGiB",
	"disk":   "diskGiB",
}

func wireField(name string) string {
	if w, ok := wireFieldNames[name]; ok {
		return w
	}
	return name // fixed names from config.Validate, never request text
}

// WithConfig registers the configuration routes:
//
//	GET /api/profiles/{name}/config
//	PUT /api/profiles/{name}/config
func WithConfig(svc ConfigService, busy ConfigBusy) Option {
	c := &configRoutes{svc: svc, busy: busy}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/config", c.get)
		rt.Handle("PUT /api/profiles/{name}/config", c.put)
	}
}

type configRoutes struct {
	svc  ConfigService
	busy ConfigBusy
}

type configJSON struct {
	Name      string  `json:"name"`
	CPU       int     `json:"cpu"`
	MemoryGiB float64 `json:"memoryGiB"`
	DiskGiB   int     `json:"diskGiB"`
}

type configUpdate struct {
	CPU       *int     `json:"cpu"`
	MemoryGiB *float64 `json:"memoryGiB"`
	DiskGiB   *int     `json:"diskGiB"`
}

type configSaved struct {
	Config          configJSON `json:"config"`
	RestartRequired []string   `json:"restartRequired"`
}

func toConfigJSON(name string, p *config.Profile) configJSON {
	out := configJSON{Name: name, CPU: defaultCPU, MemoryGiB: defaultMemoryGiB, DiskGiB: defaultDiskGiB}
	if p == nil {
		return out
	}
	if p.CPU != nil {
		out.CPU = *p.CPU
	}
	if p.Memory != nil {
		out.MemoryGiB = *p.Memory
	}
	if p.Disk != nil {
		out.DiskGiB = *p.Disk
	}
	return out
}

func (c *configRoutes) get(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	p, err := c.svc.Read(name)
	if err != nil {
		return c.mapError(name, "read", err)
	}
	writeJSON(w, http.StatusOK, toConfigJSON(name, p))
	return nil
}

func (c *configRoutes) put(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	body, err := decodeConfigUpdate(w, r)
	if err != nil {
		return err
	}
	// Read before writing so that, if the re-read after a successful write
	// fails, the response can still be built (the write already happened). An
	// error here is ignored: Apply reports the real one.
	before, _ := c.svc.Read(name)
	changes, err := c.svc.Apply(name, config.Changes{CPU: body.CPU, Memory: body.MemoryGiB, Disk: body.DiskGiB})
	if err != nil {
		return c.mapError(name, "apply", err)
	}
	saved := toConfigJSON(name, before)
	if body.CPU != nil {
		saved.CPU = *body.CPU
	}
	if body.MemoryGiB != nil {
		saved.MemoryGiB = *body.MemoryGiB
	}
	if body.DiskGiB != nil {
		saved.DiskGiB = *body.DiskGiB
	}
	if p, rerr := c.svc.Read(name); rerr == nil {
		saved = toConfigJSON(name, p)
	} else {
		slog.Warn("config re-read after write failed", "errorType", errTypeName(rerr))
	}
	restart := make([]string, 0, len(changes))
	for _, ch := range changes {
		restart = append(restart, wireField(ch.Field))
	}
	writeJSON(w, http.StatusOK, configSaved{Config: saved, RestartRequired: restart})
	return nil
}

// configKeys are the only accepted body keys, matched exactly.
var configKeys = map[string]bool{"cpu": true, "memoryGiB": true, "diskGiB": true}

// decodeConfigUpdate is a stricter decodeBody for the PUT body. encoding/json
// matches keys case-insensitively, lets a repeated key win and takes null as
// an empty struct, so the top-level object is walked by hand: only exact keys,
// each at most once, and the body must be an object.
func decodeConfigUpdate(w http.ResponseWriter, r *http.Request) (configUpdate, error) {
	bad := errBadRequest("Request body is not valid.")
	var out configUpdate
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, defaultBodyLimit))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out, bad
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok || !configKeys[key] || seen[key] {
			return out, bad
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, bad
		}
		var target any
		switch key {
		case "cpu":
			target = &out.CPU
		case "memoryGiB":
			target = &out.MemoryGiB
		default:
			target = &out.DiskGiB
		}
		sub := json.NewDecoder(bytes.NewReader(raw))
		sub.DisallowUnknownFields()
		if err := sub.Decode(target); err != nil {
			return out, bad
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return out, bad
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return out, bad
	}
	return out, nil
}

// mapError turns a Config Service error into the contract's answer. Anything
// not listed is logged by type only and sent as a fixed 500, because the text
// of those errors can describe file structure.
func (c *configRoutes) mapError(name, op string, err error) error {
	var verr *config.ValidationError
	switch {
	case errors.Is(err, config.ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, config.ErrProfileNotFound):
		return profileNotFound(name)
	case errors.Is(err, config.ErrProfileBusy):
		id, _ := c.busy.Busy(name)
		if id == "" {
			// A concurrent config write holds the slot; no job exists.
			return newAPIError(http.StatusConflict, "profile_busy",
				"The profile is being changed, try again.", map[string]any{})
		}
		return busyError(name, id)
	case errors.As(err, &verr):
		fields := make(map[string]any, len(verr.Fields))
		for _, f := range verr.Fields {
			fields[wireField(f.Field)] = f.Message
		}
		return newAPIError(http.StatusUnprocessableEntity, "validation_failed",
			"Some values are not allowed.", map[string]any{"fields": fields})
	case errors.Is(err, operations.ErrClosed):
		return newAPIError(http.StatusServiceUnavailable, "shutting_down", "The backend is shutting down.", nil)
	}
	slog.Error("config request failed", "op", op, "errorType", errTypeName(err))
	return errInternal()
}

// errTypeName names an error's concrete type without its text.
func errTypeName(err error) string {
	switch {
	case errors.Is(err, config.ErrAliasedValue):
		return "ErrAliasedValue"
	case errors.Is(err, config.ErrMultiDocument):
		return "ErrMultiDocument"
	case errors.Is(err, config.ErrUnwritableTarget):
		return "ErrUnwritableTarget"
	case errors.Is(err, config.ErrConflict):
		return "ErrConflict"
	case errors.Is(err, config.ErrNoGate):
		return "ErrNoGate"
	}
	var re *config.ReadError
	var pe *config.ParseError
	var te *config.TypeError
	var we *config.WriteError
	switch {
	case errors.As(err, &re):
		return "ReadError"
	case errors.As(err, &pe):
		return "ParseError"
	case errors.As(err, &te):
		return "TypeError"
	case errors.As(err, &we):
		return "WriteError"
	}
	return "unknown"
}
