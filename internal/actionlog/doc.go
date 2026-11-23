// Package actionlog records finished jobs and applied config changes as
// action log rows in the History Store.
//
// Recorder is an operations.Observer. Producers never block: rows go through a
// bounded queue drained by one writer goroutine, and a full queue or a failing
// store drops the row with a log line instead of failing the operation. Detail
// text is built only from typed data (exit code, field names), never from
// process output or paths.
package actionlog
