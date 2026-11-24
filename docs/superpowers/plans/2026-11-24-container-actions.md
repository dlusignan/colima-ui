# Container start, stop and restart: implementation plan

**Goal:** a Start, Stop and Restart button with a confirmation dialog on each row of a profile's Containers table.

**Architecture:** three Docker Engine write routes built on the existing Docker write foundation (`internal/dockerc` typed targets and validators, `internal/httpapi/dockerwrite.go` writer with profile gate, advisory busy check, audit rows and 404/409/422/502 mapping), then a UI column using the shared ConfirmDialog, ActionButton and `useDockerMutation`.

**Tech stack:** Go stdlib net/http, hand-rolled Docker client pinned /v1.41, React 19, TanStack Query, Vitest.

## Design decisions (approved by the owner)
1. Routes `POST /api/profiles/{name}/containers/{id}/start`, `/stop`, `/restart`. No request body, strictly validated container id, VM must be running (existing `invalid_state`).
2. Stop and restart use Docker's default grace period; the UI cannot change it.
3. Docker's 304 ("already in that state") is success with `{"changed": false}`; otherwise `{"changed": true}`.
4. Every attempt that reaches Docker writes an action log row; unknown container 404; no Docker error text reaches browsers.
5. UI: Actions column. Stopped (exited or crashed) shows Start; running shows Stop and Restart; paused and other states show nothing. Every button opens a ConfirmDialog naming the container; Stop and Restart state that the container goes down briefly. Table refetches afterwards and a toast reports the result.
6. Out of scope: remove, pause, kill, bulk actions, exec, run, build, changing the grace period.

## Tasks
1. Issue A, backend: dockerc methods, three routes, tests, docs/api.md, README, this plan committed at `docs/superpowers/plans/2026-11-24-container-actions.md`. Opus security review.
2. Issue B, frontend: Actions column, dialogs, hooks, endpoints, contract test. Depends on A.

## Review focus
1. Container id that is a name, a prefix, uppercase, or carries path or query characters: refused before any request reaches the socket.
2. A container removed between list and action: 404, not a crash or a misleading success.
3. Start on a paused container, stop on a stopped one: no 5xx leaks, honest result.
4. Double click: second request answers truthfully and the UI sends one.
5. A container that restarts itself (restart policy) while the dialog is open: UI state follows the refetch.
