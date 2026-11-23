package colima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Known values of Instance.Status. Colima may report others; they are passed
// through unchanged.
const (
	StatusRunning = "Running"
	StatusStopped = "Stopped"
)

// Instance is one profile as reported by `colima list --json`.
type Instance struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Arch    string `json:"arch"`
	CPUs    int    `json:"cpus"`
	Memory  int64  `json:"memory"` // bytes
	Disk    int64  `json:"disk"`   // bytes
	Runtime string `json:"runtime"`
	Address string `json:"address,omitempty"` // optional
}

// List runs `colima list --json`. Listing is global, so no profile flag is
// passed. Empty output is an empty list.
func List(ctx context.Context, r Runner) ([]Instance, error) {
	res, err := run(ctx, r, []string{"list", "--json"})
	if err != nil {
		return nil, err
	}
	out, err := parseList(res.Stdout)
	if err != nil {
		return nil, &ParseError{Op: "list", Raw: string(res.Stdout), Err: err}
	}
	return out, nil
}

// parseList accepts Colima's one-object-per-line output and, tolerantly, a
// JSON array.
func parseList(b []byte) ([]Instance, error) {
	out := []Instance{}
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '[' {
		if err := json.Unmarshal(t, &out); err != nil {
			return nil, err
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(b))
		for {
			var in Instance
			err := dec.Decode(&in)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			out = append(out, in)
		}
	}
	for _, in := range out {
		if in.Name == "" || in.Status == "" {
			return nil, errors.New("instance without name or status")
		}
	}
	return out, nil
}
