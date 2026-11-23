package events

import (
	"encoding/json"
	"time"
)

// timeLayout is the contract timestamp format: UTC RFC 3339 without
// fractional seconds (docs/api.md section 1).
const timeLayout = "2006-01-02T15:04:05Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// Event is something the hub can publish. Only this package implements it:
// the three structs below are the whole event vocabulary of docs/api.md
// section 5.3.
type Event interface {
	// eventName is the SSE event field.
	eventName() string
	// scope is the (profile, job) pair the query filters match against. An
	// empty job means the event is not job scoped.
	scope() (profile, job string)
}

// JobOutput is one captured output line of a job (SSE event "job.output").
type JobOutput struct {
	JobID   string `json:"jobId"`
	Profile string `json:"profile"`
	Seq     int    `json:"seq"`
	Line    string `json:"line"`
}

func (JobOutput) eventName() string         { return "job.output" }
func (e JobOutput) scope() (string, string) { return e.Profile, e.JobID }

// JobFinished reports that a job reached a final state (SSE event
// "job.finished"). ExitCode is nil, serialized as null, when the process did
// not exit normally.
type JobFinished struct {
	JobID      string    `json:"jobId"`
	Profile    string    `json:"profile"`
	State      string    `json:"state"`
	FinishedAt time.Time `json:"finishedAt"`
	ExitCode   *int      `json:"exitCode"`
}

func (JobFinished) eventName() string         { return "job.finished" }
func (e JobFinished) scope() (string, string) { return e.Profile, e.JobID }

// MarshalJSON formats FinishedAt as UTC RFC 3339 without fractional seconds.
func (e JobFinished) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		JobID      string `json:"jobId"`
		Profile    string `json:"profile"`
		State      string `json:"state"`
		FinishedAt string `json:"finishedAt"`
		ExitCode   *int   `json:"exitCode"`
	}{e.JobID, e.Profile, e.State, formatTime(e.FinishedAt), e.ExitCode})
}

// Metric is the latest sample for one scope (SSE event "metric"). DiskBytes is
// nil, serialized as null, for a container with no figure. Metrics are not job
// scoped, so they pass the job filter.
type Metric struct {
	Profile     string    `json:"profile"`
	Scope       string    `json:"scope"`
	TakenAt     time.Time `json:"takenAt"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes int64     `json:"memoryBytes"`
	DiskBytes   *int64    `json:"diskBytes"`
}

func (Metric) eventName() string         { return "metric" }
func (e Metric) scope() (string, string) { return e.Profile, "" }

// MarshalJSON formats TakenAt as UTC RFC 3339 without fractional seconds.
func (e Metric) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Profile     string  `json:"profile"`
		Scope       string  `json:"scope"`
		TakenAt     string  `json:"takenAt"`
		CPUPercent  float64 `json:"cpuPercent"`
		MemoryBytes int64   `json:"memoryBytes"`
		DiskBytes   *int64  `json:"diskBytes"`
	}{e.Profile, e.Scope, formatTime(e.TakenAt), e.CPUPercent, e.MemoryBytes, e.DiskBytes})
}
