# REST and SSE API contract

This document is the single written contract between the Go backend and the React frontend. Backend handlers implement it and the frontend client and its types follow it. If the code and this file disagree, fix one of them in the same change.

It covers every route the five views need (dashboard, profile detail, capacity, configuration editor, operation log panel), one Server Sent Events stream, and one error body shape. It does not cover how routes are implemented.

The format is Markdown, not OpenAPI. The frontend keeps its response types in one TypeScript file and checks them against the example payloads here. An OpenAPI file can be added later without changing any route.

## 1. Conventions

- Base URL: `http://127.0.0.1:8787`. The server listens on loopback only, default port 8787.
- Every request and response body is JSON (`Content-Type: application/json`), except the event stream in section 5.
- Timestamps are RFC 3339 strings in UTC, for example `2026-11-23T01:08:04Z`.
- Every profile route carries the profile name in the path as `{name}`. The name is the Colima profile name (`default` for the default profile). It is path-escaped like any URL segment.
- Memory and disk sizes are in bytes in profile and metric payloads. The configuration routes use GiB, the unit Colima's YAML uses: `memoryGiB` may be fractional, `cpu` and `diskGiB` are whole numbers. Field names carry the unit (`memoryBytes`, `memoryGiB`) so the two cannot be mixed up.
- Unknown JSON fields in a request body are rejected with `400`.

### 1.1 Authentication

Every route under `/api` requires the startup token as `Authorization: Bearer <token>`. This is the only accepted channel. There is no query-string token, because query strings end up in history and logs.

- `GET /healthz` is outside `/api`, needs no token and returns `200 {"status":"ok"}`.
- The Host and Origin checks apply to every request, including `/healthz`. A bad Host or a foreign Origin returns `403`.
- A missing or wrong token returns `401` with `WWW-Authenticate: Bearer`. This also applies to unknown paths under `/api`, so an unauthenticated caller cannot map the routes.
- The server prints `http://127.0.0.1:<port>/#token=<token>` at startup. The browser keeps the token in memory and sends it on every call.

In the route tables below, "Token" is "yes" for every route under `/api`.

### 1.2 Status codes

| Code | Meaning |
| --- | --- |
| `200` | Success with a body |
| `202` | Operation accepted and running as a job |
| `400` | Malformed input, unknown field, or an invalid range or query value |
| `401` | Missing or wrong token |
| `403` | Host or Origin not allowed |
| `404` | Unknown profile, job or path |
| `405` | Method not allowed for the path (the `Allow` header lists the allowed methods) |
| `409` | Conflict with current state |
| `422` | Field validation failed (configuration writes) |
| `500` | Unexpected server error |
| `502` | Colima or Docker failed or is not reachable |
| `503` | The backend is shutting down |

## 2. Error body

Every error response, from any route and from the middleware, has this shape. It is defined here once and every route's error table refers to it by status and `code`.

```json
{
  "error": {
    "code": "profile_busy",
    "message": "A job is already running on profile \"default\".",
    "details": { "jobId": "j_8f3a2c" }
  }
}
```

- `code` is a stable snake_case string that clients may switch on.
- `message` is text for people. Clients must not parse it.
- `details` is always an object, empty (`{}`) when there is nothing to add.

Error codes in use:

| Status | `code` | When | `details` |
| --- | --- | --- | --- |
| `400` | `bad_request` | Malformed JSON, unknown field, wrong type | `{}` |
| `400` | `invalid_range` | `from` after `to`, non-integer or non-positive `limit` or `maxPoints`, unparseable time | `{"parameter":"from"}` |
| `401` | `unauthorized` | Missing or wrong token | `{}` |
| `403` | `forbidden_host` | Host not allowed | `{}` |
| `403` | `forbidden_origin` | Origin not allowed | `{}` |
| `404` | `profile_not_found` | No profile with that name, or a malformed name | `{"name":"dev"}`, or `{}` when the name is malformed |
| `404` | `job_not_found` | No job with that id | `{"jobId":"j_x"}` |
| `404` | `not_found` | Unknown path (token present) | `{}` |
| `405` | `method_not_allowed` | Known path, wrong method (`Allow` header set) | `{}` |
| `409` | `profile_busy` | A job is running on that profile | `{"jobId":"<running job>"}` |
| `409` | `job_not_running` | Cancel on a job that already finished | `{"jobId":"...","state":"succeeded"}` |
| `409` | `invalid_state` | Start on a running profile, stop on a stopped one, container list on a stopped VM | `{"status":"running"}` |
| `422` | `validation_failed` | Configuration values rejected | `{"fields":{"cpu":"must be between 1 and 16"}}` |
| `500` | `internal_error` | Unexpected server error; the text never carries internal detail | `{}` |
| `502` | `colima_failed` | Colima could not be run or returned an error | `{}` |
| `502` | `docker_unavailable` | The Docker socket is missing or not answering | `{}` |
| `503` | `shutting_down` | The backend is stopping and accepts no new jobs | `{}` |

Reserved for later: `unsupported_version` (`502` or `409`, decided in the Colima versions issue #1). It is additive and no route changes if it arrives.

For `422`, `details.fields` maps each rejected field name to a message. Field names are the request body keys (`cpu`, `memoryGiB`, `diskGiB`).

## 3. Routes

There are 13 routes under `/api` plus `/healthz`. All `/api` routes need the token and can also return `401` and `403` as described in 1.1, so those two are not repeated in each error table.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness, no token |
| `GET` | `/api/profiles` | List profiles |
| `GET` | `/api/profiles/{name}` | One profile |
| `POST` | `/api/profiles/{name}/start` | Start a profile |
| `POST` | `/api/profiles/{name}/stop` | Stop a profile |
| `POST` | `/api/profiles/{name}/restart` | Restart a profile |
| `GET` | `/api/jobs/{id}` | Job status and captured output |
| `POST` | `/api/jobs/{id}/cancel` | Cancel a running job |
| `GET` | `/api/profiles/{name}/config` | Read configuration |
| `PUT` | `/api/profiles/{name}/config` | Update configuration |
| `GET` | `/api/profiles/{name}/metrics` | Metric history |
| `GET` | `/api/profiles/{name}/containers` | Containers with current usage |
| `GET` | `/api/profiles/{name}/actions` | Recent action log entries |
| `GET` | `/api/events` | Server Sent Events stream |

### 3.1 `GET /healthz`

Token: no. Request body: none.

Response `200`:

```json
{ "status": "ok" }
```

Errors: `403` `forbidden_host` only (section 2).

### 3.2 `GET /api/profiles`

Token: yes. Request body: none. Used by the dashboard.

Response `200`:

```json
{
  "profiles": [
    {
      "name": "default",
      "status": "running",
      "runtime": "docker",
      "cpus": 4,
      "memoryBytes": 8589934592,
      "diskBytes": 107374182400,
      "colimaVersion": "0.8.1",
      "source": "both",
      "activeJobId": null
    },
    {
      "name": "dev",
      "status": "stopped",
      "runtime": "containerd",
      "cpus": 2,
      "memoryBytes": 4294967296,
      "diskBytes": 64424509440,
      "colimaVersion": "0.8.1",
      "source": "file_only",
      "activeJobId": "j_8f3a2c",
      "warnings": [
        {
          "code": "colima_version_unsupported",
          "message": "The installed Colima version is outside the tested 0.10.x line."
        }
      ]
    }
  ]
}
```

Fields:

- `status`: `running`, `stopped` or `unknown`.
- `runtime`: the container runtime name as Colima reports it.
- `cpus`, `memoryBytes`, `diskBytes`: allocated resources from configuration.
- `colimaVersion`: a string. Empty string when unknown.
- `source`: `both` (known to the CLI and to a config file), `cli_only` (running without a file) or `file_only` (a file that Colima does not list).
- `activeJobId`: the id of the running job on this profile, or `null`. The frontend uses it to disable buttons and to open the log panel.
- `warnings`: optional array of `{"code": "...", "message": "..."}` for non-fatal problems with that profile. The key is omitted when there are none. The codes in use are `colima_unavailable` (Colima could not be queried, so live fields are unknown), `config_unreadable` (the configuration folder could not be read), `config_invalid` (the profile file could not be read or parsed) and `colima_version_unsupported` (the installed Colima version is outside the tested 0.10.x line). Messages are fixed texts without paths. Clients must ignore codes they do not know.

State is read live on every call. An empty list is `{"profiles":[]}`.

Errors: `502` `colima_failed` when Colima cannot be queried and no profile data could be built.

### 3.3 `GET /api/profiles/{name}`

Token: yes. Request body: none. Used by the profile detail page.

Response `200`: one profile object, the same fields as an element of the list above (not wrapped), including the optional `warnings` array.

Errors: `404` `profile_not_found`, `502` `colima_failed`.

### 3.4 Start, stop and restart

`POST /api/profiles/{name}/start`, `POST /api/profiles/{name}/stop`, `POST /api/profiles/{name}/restart`

Token: yes. Request body: none (an empty body or `{}`). The call returns at once and does not wait for the command.

Response `202`:

```json
{ "jobId": "j_8f3a2c" }
```

The client then follows the job with the event stream (section 5) and `GET /api/jobs/{id}`.

Errors:

- `404` `profile_not_found`.
- `409` `profile_busy`: another job is running on that profile. `details.jobId` is the running job.
- `409` `invalid_state`: start on a running profile, or stop on a stopped one. `details.status` is the current status. Restart is allowed from either state.
- `400` `bad_request`: the body is not empty or `{}`.
- `502` `colima_failed`: Colima could not be queried or launched.
- `503` `shutting_down`: the backend is stopping and accepts no new jobs.
- `500` `internal_error`: unexpected failure.

### 3.5 `GET /api/jobs/{id}`

Token: yes. Request body: none. Used by the operation log panel.

Response `200`:

```json
{
  "id": "j_8f3a2c",
  "profile": "default",
  "action": "start",
  "state": "failed",
  "startedAt": "2026-11-23T01:10:00Z",
  "finishedAt": "2026-11-23T01:10:42Z",
  "exitCode": 1,
  "output": [
    { "seq": 1, "line": "\u001b[32mstarting colima\u001b[0m" },
    { "seq": 2, "line": "waiting for the VM to boot" }
  ],
  "truncated": false,
  "droppedLines": 0
}
```

Fields:

- `action`: `start`, `stop` or `restart`.
- `state`: `running`, `succeeded`, `failed` or `cancelled`.
- `finishedAt` and `exitCode` are `null` while the job is running. `exitCode` is also `null` for a cancelled job whose process was killed.
- `output` holds the captured lines in order. Each line has a per-job `seq` that starts at 1 and rises by 1. Lines keep their ANSI escape sequences.
- Captured output is bounded. When the bound is hit the oldest lines are dropped, `truncated` is `true` and `droppedLines` counts them. The first `seq` in `output` is then `droppedLines + 1`, so the gap is visible and not silent.

How the panel avoids losing or repeating lines: open the stream first, call this route, render `output`, then render streamed `job.output` events whose `seq` is greater than the last `seq` already shown.

Finished jobs after a backend restart: jobs are kept in memory only, so while the backend process lives this route returns finished jobs with their full captured output. After a restart the in-memory jobs are gone and this route returns `404` `job_not_found` for them. The action log (section 3.11) still holds a summary row for each finished job, without its output.

Errors: `404` `job_not_found`.

### 3.6 `POST /api/jobs/{id}/cancel`

Token: yes. Request body: none.

Response `200`: the job object from 3.5, with `state` `cancelled`. The response is sent after the manager has recorded the cancellation, so a following `GET /api/jobs/{id}` shows `cancelled`.

Errors:

- `404` `job_not_found`.
- `409` `job_not_running`: the job already finished. `details.state` holds its final state.
- `400` `bad_request`: the body is not empty or `{}`.
- `500` `internal_error`: the cancellation was not recorded within 10 seconds (a repeated cancel then returns `200` or `409`).

### 3.7 `GET /api/profiles/{name}/config`

Token: yes. Request body: none. Used by the configuration editor.

Response `200`:

```json
{
  "name": "default",
  "cpu": 4,
  "memoryGiB": 8,
  "diskGiB": 100
}
```

These are the editable settings. The values are the ones the Config Service read from the profile's YAML file. Other keys in the file are not returned and are preserved on write.

Errors: `404` `profile_not_found`. The file is read directly, so `502` does not apply.

### 3.8 `PUT /api/profiles/{name}/config`

Token: yes. Used by the configuration editor.

Request body: the fields to change. Fields that are absent are left alone.

```json
{
  "cpu": 6,
  "memoryGiB": 12
}
```

Response `200`: the saved configuration and the restart-required list.

```json
{
  "config": { "name": "default", "cpu": 6, "memoryGiB": 12, "diskGiB": 100 },
  "restartRequired": ["cpu", "memoryGiB"]
}
```

`restartRequired` lists the changed fields that only take effect after a restart. It is empty (`[]`) when nothing needs one. The route never restarts the VM. The user starts a restart as a normal operation (3.4).

Errors:

- `400` `bad_request`: malformed body or unknown field.
- `404` `profile_not_found`.
- `409` `profile_busy`: a job is running on that profile. Nothing is written.
- `422` `validation_failed`: `details.fields` maps each bad field to a message. Nothing is written. Example:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Some values are not allowed.",
    "details": { "fields": { "cpu": "must be between 1 and 16", "diskGiB": "cannot be reduced" } }
  }
}
```

### 3.9 `GET /api/profiles/{name}/metrics`

Token: yes. Request body: none. Used by the capacity view for history. Current usage has no separate route: it is the last point of this route plus `metric` events (section 5).

Query parameters:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `scope` | `vm` | `vm` or a container id |
| `from` | one hour before `to` | Start of the range, RFC 3339 |
| `to` | now | End of the range, RFC 3339 |
| `maxPoints` | `300` | Upper bound on returned points, at most `2000` (larger values are clamped) |

Response `200`:

```json
{
  "profile": "default",
  "scope": "vm",
  "from": "2026-11-23T00:10:00Z",
  "to": "2026-11-23T01:10:00Z",
  "points": [
    { "takenAt": "2026-11-23T00:10:00Z", "cpuPercent": 12.5, "memoryBytes": 2147483648, "diskBytes": 21474836480 },
    { "takenAt": "2026-11-23T00:10:30Z", "cpuPercent": 14.1, "memoryBytes": 2181038080, "diskBytes": 21474836480 }
  ]
}
```

`cpuPercent`, `memoryBytes` and `diskBytes` are each `null` when the sampler could not read that value (the first tick after a start, a Docker outage, a disk figure older than three minutes). A client must draw a gap for `null`, never `0`.

Points are in time order and are downsampled when the range holds more than `maxPoints` samples, so `points` never exceeds `maxPoints`. A range with no samples, or a scope with no samples, returns `"points": []`. A stopped profile also returns history, not an error.

Errors: `400` `invalid_range` (`from` after `to`, `maxPoints` below 1, unparseable time; `details.parameter` names the field), `404` `profile_not_found`.

### 3.10 `GET /api/profiles/{name}/containers`

Token: yes. Request body: none. Used by the capacity view for the per-container breakdown.

Response `200`:

```json
{
  "containers": [
    {
      "id": "3f9a1c0b7d2e",
      "name": "web",
      "image": "nginx:1.27",
      "state": "running",
      "cpuPercent": 0.8,
      "memoryBytes": 31457280,
      "status": "Up 2 hours",
      "createdAt": "2025-06-01T09:30:00Z",
      "ports": [
        { "containerPort": 80, "protocol": "tcp", "hostIp": null, "hostPort": null },
        { "containerPort": 443, "protocol": "tcp", "hostIp": "0.0.0.0", "hostPort": 8443 },
        { "containerPort": 443, "protocol": "tcp", "hostIp": "::", "hostPort": 8443 }
      ]
    }
  ]
}
```

- `id` is the value used as `scope` in the metrics route and in `metric` events.
- `state` is the Docker state string (`running`, `exited`, `paused`, and so on).
- `status` is Docker's human readable status text, for example `Up 2 hours` or `Exited (0) 3 days ago`. Never null.
- `createdAt` is the creation time as RFC 3339 in UTC.
- `ports` is an array, `[]` when the container has none. `hostIp` and `hostPort` are `null` for a port that is exposed but not published. The array is sorted by `containerPort`, then `protocol`, then `hostPort` (unpublished first), then `hostIp`. Docker lists a published port once per bound address, so one port can appear twice (`0.0.0.0` and `::`); both entries are kept.
- `cpuPercent` and `memoryBytes` are current usage read live from the Docker Client when the request is handled. For a container that is not running both are `0`.
- A running VM with no containers returns `{"containers":[]}`. So does a running profile whose runtime is not Docker.

This route only reads. There is no container control.

Errors:

- `404` `profile_not_found`.
- `409` `invalid_state`: the VM is stopped (`details.status` is `stopped`). This is the expected answer for a stopped VM, not a server error.
- `502` `docker_unavailable`: the VM is running but the Docker socket is missing or not answering.

### 3.11 `GET /api/profiles/{name}/actions`

Token: yes. Request body: none. Used by the profile detail page. History only: it never says what the current state is.

Query parameters:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `limit` | `20` | Maximum entries, at most `100` (larger values are clamped) |

Response `200`, newest first:

```json
{
  "entries": [
    {
      "startedAt": "2026-11-23T01:10:00Z",
      "action": "start",
      "outcome": "failed",
      "detail": "colima exited with status 1"
    },
    {
      "startedAt": "2026-11-22T22:03:11Z",
      "action": "config_change",
      "outcome": "success",
      "detail": null
    }
  ]
}
```

- `action` is a string such as `start`, `stop`, `restart` or `config_change`. Clients must tolerate new values.
- `outcome` is `success`, `failed` or `cancelled`.
- `detail` is a short string or `null`.
- Entries carry no job id. Finished job output is not kept across a restart (see the C4 decisions table), so there is nothing to reopen.
- An empty log returns `{"entries":[]}`.

Errors: `400` `invalid_range` (`limit` below 1 or not an integer), `404` `profile_not_found`.

### 3.12 `GET /api/profiles/{name}/images`

Token: yes. Request body: none. Used by the Docker tab to list the images on the VM.

Response `200`:

```json
{
  "images": [
    {
      "id": "3f9a1c0b7d2e",
      "tags": ["nginx:1.27"],
      "sizeBytes": 192000000,
      "createdAt": "2026-05-02T09:41:07Z",
      "containers": 2
    }
  ]
}
```

- `id` is the first 12 characters of the image ID, without the `sha256:` prefix.
- `tags` is `[]` for an untagged image. It is never null.
- `sizeBytes` is the image size with shared layers counted in every image that uses them, so the sum over all images can exceed the disk use in the engine overview.
- `createdAt` is the creation time as RFC 3339 in UTC.
- `containers` is the number of containers, running or not, that use the image. It is counted from each container's image ID, so a container created from a tag that has since moved still counts against the old image. The count is a snapshot: a container started while the request runs can be missed.
- The list is sorted by first tag (untagged images last), then by `id`.
- A running VM with no images returns `{"images":[]}`. So does a running profile whose runtime is not Docker.

This route only reads. There is no image delete, pull or prune.

Errors:

- `404` `profile_not_found`.
- `409` `invalid_state`: the VM is stopped (`details.status` is `stopped`).
- `502` `docker_unavailable`: the VM is running but the Docker socket is missing or not answering.

### 3.13 `GET /api/profiles/{name}/volumes`

Token: yes. Request body: none. Used by the Docker tab to list the volumes on the VM.

Response `200`:

```json
{
  "volumes": [
    {
      "name": "pgdata",
      "driver": "local",
      "createdAt": "2026-04-11T18:02:55Z",
      "sizeBytes": 73400320,
      "containers": 1
    }
  ]
}
```

- `sizeBytes` and `containers` come from the engine's disk usage report. Both are `null` when the engine has no usage figure for the volume (older engines, or a value Docker reports as unknown). A volume of size zero or with no containers returns `0`, not `null`.
- `containers` is the number of containers, running or not, that mount the volume.
- `createdAt` is the creation time as RFC 3339 in UTC, or an empty string if the engine's value cannot be parsed.
- The response never contains the volume's mountpoint or any other host path.
- The list is sorted by `name`.
- A running VM with no volumes returns `{"volumes":[]}`. So does a running profile whose runtime is not Docker.
- The engine measures volume sizes while it answers, so this route is slower on VMs with large volumes. It has a 15 second budget.

This route only reads. There is no volume create, remove or prune.

Errors:

- `404` `profile_not_found`.
- `409` `invalid_state`: the VM is stopped (`details.status` is `stopped`).
- `502` `docker_unavailable`: the VM is running but the Docker socket is missing or not answering.

### 3.14 `GET /api/profiles/{name}/networks`

Token: yes. Request body: none. Used by the Docker tab to list the networks on the VM.

Response `200`:

```json
{
  "networks": [
    {
      "id": "9d1c0a47be12",
      "name": "bridge",
      "driver": "bridge",
      "scope": "local",
      "internal": false,
      "containers": 3
    }
  ]
}
```

- `id` is the first 12 characters of the network ID.
- `containers` is the number of containers attached to the network, and it includes stopped containers. The engine's network list has no attached containers, so the count is taken from the container list (each container's network ID). The count is a snapshot: a container started while the request runs can be missed.
- The list is sorted by `name`, then by `id`.
- A running VM always has the built-in networks, so the list is empty only for a running profile whose runtime is not Docker, which returns `{"networks":[]}`.

This route only reads. There is no network create, remove or prune.

Errors are the same as for 3.13.

## 4. Routes that do not exist

By design the API has no route for arbitrary commands, container control, profile creation or deletion, YAML editing, or profile logs. A separate "current usage" route is also not defined, see 3.9.

## 5. Event stream

`GET /api/events` returns `text/event-stream`. There is one connection per browser tab. Token: yes.

Query parameters, both optional: `profile={name}` limits events to one profile, and `job={id}` limits `job.output` and `job.finished` to one job. A `metric` event is not tied to a job, so it passes a `job` filter.

### 5.1 Token transport

`EventSource` cannot set headers, so the client does not use it. The client calls `fetch` on this route with the same `Authorization: Bearer <token>` header as every REST call and reads the response body as a stream. The token never goes in the URL.

### 5.2 Framing

Each event is a standard SSE frame. The `data` is one line of JSON:

```
id: 42
event: job.output
data: {"jobId":"j_8f3a2c","profile":"default","seq":3,"line":"waiting for the VM to boot"}

```

- `id` is a number that rises by 1 for each event the hub sends. It gives an order and is the value a client sends back as `Last-Event-ID` when it reconnects.
- The server sends a comment line (`: keepalive`) on an idle connection so proxies keep it open.
- A client that cannot keep up is disconnected by the server. The client reconnects with backoff.
- The server does not promise to replay events missed while disconnected. After a reconnect the client reloads state over REST (`GET /api/jobs/{id}` for an open job, the metrics route for charts) and drops streamed lines it already has by `seq`.

### 5.3 Event types

Timestamps and `state` values use the same formats as the REST routes.

**`job.output`**: one captured output line. `seq` is the same per-job number that appears in `GET /api/jobs/{id}`. ANSI sequences are kept in `line`.

```json
{
  "jobId": "j_8f3a2c",
  "profile": "default",
  "seq": 3,
  "line": "\u001b[33mwaiting for the VM to boot\u001b[0m"
}
```

**`job.finished`**: the job reached a final state. No more `job.output` follows for that job.

```json
{
  "jobId": "j_8f3a2c",
  "profile": "default",
  "state": "succeeded",
  "finishedAt": "2026-11-23T01:10:42Z",
  "exitCode": 0
}
```

`state` is `succeeded`, `failed` or `cancelled`. `exitCode` is `null` when the process did not exit normally.

**`metric`**: the latest sample for one scope, published each time the sampler writes one. One event per scope per tick (the VM, and each container).

```json
{
  "profile": "default",
  "scope": "vm",
  "takenAt": "2026-11-23T01:10:30Z",
  "cpuPercent": 14.1,
  "memoryBytes": 2181038080,
  "diskBytes": 21474836480
}
```

`scope` is `vm` or a container id. For a container scope `diskBytes` can be `null`.

The `profile` field appears on every event so one connection can serve all views. Clients ignore event types they do not know.

## 6. What each view needs

| View | Routes |
| --- | --- |
| Dashboard | `GET /api/profiles`, start, stop and restart (3.4), `GET /api/jobs/{id}`, `GET /api/events` |
| Profile detail | `GET /api/profiles/{name}`, start, stop and restart, `GET /api/profiles/{name}/actions`, `GET /api/profiles/{name}/config` |
| Capacity | `GET /api/profiles/{name}/metrics`, `GET /api/profiles/{name}/containers`, `GET /api/events` (`metric`) |
| Configuration editor | `GET` and `PUT /api/profiles/{name}/config`, restart (3.4) |
| Operation log panel | `GET /api/jobs/{id}`, `POST /api/jobs/{id}/cancel`, `GET /api/events` (`job.output`, `job.finished`) |

## 7. Open points

- Finished job output after a backend restart is not kept. Section 3.5 describes the in-memory behaviour.
- Routes 3.10 and 3.11 are defined here and implemented by issues #38 and #39. No earlier issue built them.
