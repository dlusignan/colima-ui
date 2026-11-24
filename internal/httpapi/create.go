package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/dlusignan/colima-ui/internal/operations"
)

// createBodyLimit bounds the POST /api/profiles body. The largest legitimate
// body is 16 mount paths of 4096 bytes plus 32 variables of 128 + 4096 bytes
// (about 200 KiB before JSON escaping), so the cap is 256 KiB.
const createBodyLimit int64 = 256 << 10

// ErrExistsCheckFailed wraps a failure of the existence check (`colima list` or
// the folder scan), so it is logged apart from a launch failure. It answers 502.
var ErrExistsCheckFailed = errors.New("httpapi: profile exists check failed")

// ErrProfileExists means the name is already in `colima list` or has a folder
// under the Colima home, in any letter case.
var ErrProfileExists = errors.New("httpapi: profile already exists")

// ValidationError carries a field-level answer. Fields maps wire field names
// to fixed messages: never request text.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "httpapi: validation failed" }

// NewProfile is the decoded creation body. Nil pointers, empty strings and
// false mean the setting is left to Colima's default.
type NewProfile struct {
	Name              string
	CPUs              *int
	MemoryGiB         *float64
	DiskGiB           *int
	RootDiskGiB       *int
	Runtime           string
	VMType            string
	Arch              string
	MountType         string
	Kubernetes        bool
	KubernetesVersion string

	// Advanced settings, all optional.
	Hostname         string
	DNS              []string
	DNSHosts         []DNSHost
	SSHAgent         bool
	VZRosetta        bool
	PortForwarder    string
	MountInotify     bool
	NetworkAddress   bool
	NetworkMode      string
	NetworkInterface string
	CPUType          string

	// Mounts and Env, both optional. Env is in the order the keys appeared.
	Mounts []Mount
	Env    EnvVars
}

// Mount is one mounts entry. It decodes only exactly cased "path" (string) and
// "writable" (boolean) keys, each once; null, an unknown key or a repeated key
// is a malformed body.
type Mount struct {
	Path     string
	Writable bool
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *Mount) UnmarshalJSON(b []byte) error {
	var out Mount
	err := decodeFlatObject(b, func(key string, raw json.RawMessage) error {
		switch key {
		case "path":
			return unmarshalNonNull(raw, &out.Path)
		case "writable":
			return unmarshalNonNull(raw, &out.Writable)
		}
		return errors.New("unknown key")
	})
	if err != nil {
		return err
	}
	*m = out
	return nil
}

// EnvVar is one environment variable.
type EnvVar struct{ Key, Value string }

// EnvVars is the decoded env object: a JSON object of string values. A key may
// appear once (keys that differ only in case are different variables), a value
// must be a string, and null counts as absent. The order is the order in the
// body.
type EnvVars []EnvVar

// UnmarshalJSON implements json.Unmarshaler.
func (e *EnvVars) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "null" {
		*e = nil
		return nil
	}
	var out EnvVars
	err := decodeFlatObject(b, func(key string, raw json.RawMessage) error {
		var v string
		if err := unmarshalNonNull(raw, &v); err != nil {
			return err
		}
		out = append(out, EnvVar{Key: key, Value: v})
		return nil
	})
	if err != nil {
		return err
	}
	*e = out
	return nil
}

func unmarshalNonNull(raw json.RawMessage, dst any) error {
	if string(bytes.TrimSpace(raw)) == "null" {
		return errors.New("null value")
	}
	return json.Unmarshal(raw, dst)
}

// decodeFlatObject walks one JSON object, rejecting a repeated key, and calls
// each for every member. It exists because encoding/json takes the last of a
// repeated key and matches struct keys without regard to case.
func decodeFlatObject(b []byte, each func(key string, raw json.RawMessage) error) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("not an object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("bad key")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return errors.New("bad value")
		}
		if err := each(key, raw); err != nil {
			return errors.New("bad value")
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return errors.New("not an object")
	}
	return nil
}

// DNSHost is one dnsHosts entry. It decodes only exactly cased "name" and
// "address" keys, each once, as strings; null, an unknown key or a repeated
// key is a malformed body.
type DNSHost struct {
	Name    string
	Address string
}

// UnmarshalJSON implements json.Unmarshaler.
func (h *DNSHost) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("not an object")
	}
	var out DNSHost
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("bad key")
		}
		seen[key] = true
		var dst *string
		switch key {
		case "name":
			dst = &out.Name
		case "address":
			dst = &out.Address
		default:
			return errors.New("unknown key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil || json.Unmarshal(raw, dst) != nil || string(bytes.TrimSpace(raw)) == "null" {
			return errors.New("bad value")
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return errors.New("not an object")
	}
	*h = out
	return nil
}

// ProfileCreator validates a creation request, refuses a profile that exists
// and starts the job that creates it. It keeps this package free of Colima
// knowledge. Create returns *ValidationError, ErrProfileExists, an error from
// operations.Manager.Submit (*operations.BusyError, ErrClosed, ErrIDExhausted)
// or any other error when Colima could not be asked or launched (a 502).
type ProfileCreator interface {
	Create(ctx context.Context, p NewProfile) (jobID string, err error)
}

// WithCreate registers the creation route:
//
//	POST /api/profiles
func WithCreate(c ProfileCreator) Option {
	cr := &createRoutes{creator: c}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles", cr.post)
	}
}

type createRoutes struct{ creator ProfileCreator }

type profileCreated struct {
	JobID   string `json:"jobId"`
	Profile string `json:"profile"`
}

func (c *createRoutes) post(w http.ResponseWriter, r *http.Request) error {
	var p NewProfile
	err := decodeStrictObject(w, r, createBodyLimit, map[string]any{
		"name": &p.Name, "cpus": &p.CPUs, "memoryGiB": &p.MemoryGiB, "diskGiB": &p.DiskGiB,
		"rootDiskGiB": &p.RootDiskGiB, "runtime": &p.Runtime, "vmType": &p.VMType,
		"arch": &p.Arch, "mountType": &p.MountType, "kubernetes": &p.Kubernetes,
		"kubernetesVersion": &p.KubernetesVersion,
		"hostname":          &p.Hostname, "dns": &p.DNS, "dnsHosts": &p.DNSHosts, "sshAgent": &p.SSHAgent,
		"vzRosetta": &p.VZRosetta, "portForwarder": &p.PortForwarder, "mountInotify": &p.MountInotify,
		"networkAddress": &p.NetworkAddress, "networkMode": &p.NetworkMode,
		"networkInterface": &p.NetworkInterface, "cpuType": &p.CPUType,
		"mounts": &p.Mounts, "env": &p.Env,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), gateTimeout)
	defer cancel()
	id, err := c.creator.Create(ctx, p)
	if err != nil {
		return createError(err)
	}
	writeJSON(w, http.StatusAccepted, profileCreated{JobID: id, Profile: p.Name})
	return nil
}

// createError maps what a ProfileCreator returns. Nothing from err reaches the
// client.
func createError(err error) error {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		fields := make(map[string]any, len(verr.Fields))
		keys := make([]string, 0, len(verr.Fields))
		for k := range verr.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fields[k] = verr.Fields[k]
		}
		return newAPIError(http.StatusUnprocessableEntity, "validation_failed",
			"Some values are not allowed.", map[string]any{"fields": fields})
	case errors.Is(err, ErrProfileExists):
		return newAPIError(http.StatusConflict, "profile_exists", "A profile with this name already exists.", nil)
	}
	if errors.Is(err, ErrExistsCheckFailed) {
		// Only the error type is logged: the text can hold paths.
		slog.Error("profile exists check failed", "errorType", errTypeName(err))
		return errColimaFailed()
	}
	// Busy, shutting down and an exhausted ID generator have their own answers;
	// anything else is Colima (the listing, the folder scan or the launch)
	// failing, a 502 with nothing from err in the body.
	return submitError("", operations.ActionCreate, err)
}
