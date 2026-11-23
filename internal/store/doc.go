// Package store is the History Store holding metric samples and the action log.
//
// It keeps history only: current state is never read from here. Data lives in
// one SQLite file (pure Go driver, no cgo) behind two connection pools (one
// writer, a small reader pool), plus an in-memory ring of the last 24 hours per
// (profile, scope) so recent chart queries never touch the disk.
//
// Schema changes ship as embedded SQL migrations versioned through PRAGMA
// user_version. Retention is applied by Cleanup: raw samples older than 24
// hours are folded into 1 minute averages, those are deleted after 30 days, and
// the action log keeps the newest rows per profile.
//
// The API uses plain structs (nil pointers mean NULL) and narrow interfaces
// (SampleSink, MetricReader, ActionLog) that callers can fake in tests. No SQL
// type leaks out of the package.
package store
