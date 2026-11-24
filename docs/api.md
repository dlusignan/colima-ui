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

### 1.3 Docker write routes

Routes that change Docker objects (volumes, networks, images) live under `/api/profiles/{name}/...` and all follow the same rules, in this order. The first rule that fails answers; nothing reaches Docker before the last step.

1. **Profile.** `404 profile_not_found` for an unknown or malformed profile name, `409 invalid_state` when the VM is not running (`details.status` names the state).
2. **Busy check.** `409 profile_busy` (with `details.jobId`) while a start, stop or restart job runs on the profile, the same answer as the config route. The check is advisory: it sees only this backend's own jobs, not a `colima` run from a terminal or a configuration write in progress, and a job can start right after it. For that reason a slow Docker route (image pull, 3.30) does not hold the profile slot: it runs as a job that is not counted by this check, so a pull never blocks a start, stop, restart or configuration write.
3. **Body.** The body is a JSON object with the route's own keys, at most 1 MiB unless the route says otherwise. Unknown keys, repeated keys, keys in the wrong case, a `null` or non-object body, trailing data and a body over the limit are `400 bad_request`. A route that takes no body accepts an empty body or `{}` and nothing else. The strict decoder checks the top-level keys only: write bodies stay flat (no nested objects), unless the route validates the nested keys itself.
4. **Validation.** Object names (volumes, networks) match `[a-zA-Z0-9][a-zA-Z0-9_.-]*` and are at most 128 characters. Image references follow a narrower form of Docker's reference grammar (`[domain[:port]/]path[:tag][@digest]`): the registry domain may use uppercase letters, the path may not, IPv6 registry hosts are rejected, and the limit of 255 characters covers the whole reference, tag and digest included. The same rules apply to path parameters and body values, so `..`, `/`, `%`, spaces and control characters never reach a Docker URL. A failure is `422 invalid_request` with `details.fields` mapping the rejected key or path parameter to a fixed message, never to the request value.
5. **Docker call.** One fixed, typed call. Docker's own error text is never returned; only the fixed message and, for 404 and 409, `details.name` with the validated object name:

| Docker answer | Status | `code` | `details` |
| --- | --- | --- | --- |
| Object does not exist | `404` | `not_found` | `{"name":"pgdata"}` |
| In use (409) or predefined and protected (403) | `409` | `conflict` | `{"name":"pgdata"}` |
| Request values rejected (400) | `422` | `invalid_request` | `{"fields":{}}` |
| Socket missing or not answering, timeout, Docker 5xx | `502` | `docker_unavailable` | `{}` |
| Profile does not use the Docker runtime | `409` | `invalid_state` | `{"runtime":"not_docker"}` |
| The VM stopped between the profile lookup and the Docker client lookup | `409` | `invalid_state` | `{"status":"stopped"}` |
| Any other failure while getting the Docker client | `500` | `internal_error` | `{}` (a `failed` row is still written) |

The `502 docker_unavailable` message tells the causes apart: the socket is missing or not answering, Docker answered with an error, or Docker did not answer in time.

6. **Audit.** Every attempt that reaches step 5 appends one row to the action log (3.11) with the route's action name (`docker.<resource>.<verb>`, for example `docker.volume.remove`), the outcome `success` or `failed`, and as `detail` only the object name or reference. Requests refused in steps 1 to 4 write no row, so a hostile name never lands in the log. The row never contains body content, registry credentials or paths. A failed append is logged on the server and never changes the answer. The one exception is the image pull (3.30): it answers before its work is done, so its row is written when the job ends, with the job's real outcome (`success`, `failed` or `cancelled`). From step 5 on the call runs even if the browser disconnects (it is bounded by the route's own timeout), so the row says what happened. `failed` means the write was not confirmed: for `404`, `409` and `422` Docker refused it and nothing changed, but after a timeout, a dropped socket or a Docker `5xx` the change may or may not have been applied. The answer's status code tells the two apart; the row does not.

A successful write answers `200` with a JSON body, or `204` with none, as each route documents.

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
| `404` | `not_found` | Unknown path (token present), or a Docker object a write route names does not exist (1.3) | `{}`, or `{"name":"pgdata"}` |
| `405` | `method_not_allowed` | Known path, wrong method (`Allow` header set) | `{}` |
| `409` | `profile_busy` | A job is running on that profile | `{"jobId":"<running job>"}` |
| `409` | `job_not_running` | Cancel on a job that already finished | `{"jobId":"...","state":"succeeded"}` |
| `409` | `conflict` | A Docker object is in use or protected (1.3) | `{"name":"pgdata"}` |
| `409` | `invalid_state` | Start on a running profile, stop on a stopped one, container list on a stopped VM, engine route on a running profile that does not use Docker | `{"status":"running"}` |
| `422` | `validation_failed` | Configuration values rejected | `{"fields":{"cpu":"must be between 1 and 16"}}` |
| `422` | `invalid_request` | A Docker write route rejected a name, reference or value (1.3) | `{"fields":{"name":"Not a valid Docker name."}}` |
| `500` | `internal_error` | Unexpected server error; the text never carries internal detail | `{}` |
| `502` | `colima_failed` | Colima could not be run or returned an error | `{}` |
| `502` | `docker_unavailable` | The Docker socket is missing or not answering | `{}` |
| `503` | `shutting_down` | The backend is stopping and accepts no new jobs | `{}` |

Reserved for later: `unsupported_version` (`502` or `409`, decided in the Colima versions issue #1). It is additive and no route changes if it arrives.

For `422`, `details.fields` maps each rejected field name to a message. The volume routes (3.18 to 3.21) use `name`, `labels`, `volume` (the path parameter) and `confirm`; the network routes (3.22 to 3.25) use `name`, `subnet`, `network` (the path parameter) and `confirm`; the image routes (3.26 to 3.29) use `image` (the path parameter), `repository`, `tag` and `confirm`; the container action routes (3.36) use `id` (the path parameter). Field names are the request body keys: `cpu`, `memoryGiB` and `diskGiB` for the config route (3.8), and `name`, `cpus`, `memoryGiB`, `diskGiB`, `rootDiskGiB`, `runtime`, `vmType`, `arch`, `mountType`, `kubernetesVersion`, `hostname`, `dns`, `dnsHosts`, `vzRosetta`, `portForwarder`, `networkMode`, `networkInterface`, `cpuType`, `mounts` and `env` for profile creation (3.17; array elements are named `dns[0]`, `dnsHosts[0].name`, `dnsHosts[0].address` and `mounts[0].path`; environment entries are named `env[0]` or `env.KEY`, see 3.17).

## 3. Routes

There are 45 routes under `/api` plus `/healthz`. All `/api` routes need the token and can also return `401` and `403` as described in 1.1, so those two are not repeated in each error table.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Liveness, no token |
| `GET` | `/api/profiles` | List profiles |
| `GET` | `/api/profiles/{name}` | One profile |
| `POST` | `/api/profiles` | Create a profile (a start job on a new name) |
| `POST` | `/api/profiles/{name}/start` | Start a profile |
| `POST` | `/api/profiles/{name}/stop` | Stop a profile |
| `POST` | `/api/profiles/{name}/restart` | Restart a profile |
| `POST` | `/api/profiles/{name}/update` | Update the container runtime of a running profile |
| `POST` | `/api/profiles/{name}/delete` | Delete a profile as a job, only when its VM holds no Docker containers, images or volumes; needs the profile name as confirmation (3.32) |
| `GET` | `/api/profiles/{name}/contents` | Counts of Docker containers, images and volumes in a running profile (3.37) |
| `GET` | `/api/jobs/{id}` | Job status and captured output |
| `POST` | `/api/jobs/{id}/cancel` | Cancel a running job |
| `GET` | `/api/profiles/{name}/config` | Read configuration |
| `PUT` | `/api/profiles/{name}/config` | Update configuration |
| `GET` | `/api/profiles/{name}/metrics` | Metric history |
| `GET` | `/api/profiles/{name}/containers` | Containers with current usage |
| `GET` | `/api/profiles/{name}/images` | Docker images |
| `POST` | `/api/profiles/{name}/images/{image}/tag` | Add a tag to an image (3.26) |
| `GET` | `/api/profiles/{name}/images/{image}` | Inspect one image (3.27) |
| `DELETE` | `/api/profiles/{name}/images/{image}` | Remove an image or untag one reference, never forced (3.28) |
| `POST` | `/api/profiles/{name}/images/prune` | Remove unused images (dangling only, or all); needs a confirmation text (3.29) |
| `POST` | `/api/profiles/{name}/images/pull` | Pull an image as a job with progress; public registries only (3.30) |
| `GET` | `/api/profiles/{name}/volumes` | Docker volumes |
| `POST` | `/api/profiles/{name}/volumes` | Create a volume (3.18) |
| `GET` | `/api/profiles/{name}/volumes/{volume}` | Inspect one volume (3.19) |
| `DELETE` | `/api/profiles/{name}/volumes/{volume}` | Remove a volume, never forced (3.20) |
| `POST` | `/api/profiles/{name}/volumes/prune` | Remove all unused volumes, named ones included; needs a confirmation text (3.21) |
| `GET` | `/api/profiles/{name}/networks` | Docker networks |
| `POST` | `/api/profiles/{name}/networks` | Create a bridge network (3.22) |
| `GET` | `/api/profiles/{name}/networks/{network}` | Inspect one network (3.23) |
| `DELETE` | `/api/profiles/{name}/networks/{network}` | Remove a network, never forced (3.24) |
| `POST` | `/api/profiles/{name}/networks/prune` | Remove all unused networks; needs a confirmation text (3.25) |
| `GET` | `/api/profiles/{name}/engine` | Docker engine facts and disk usage |
| `GET` | `/api/profiles/{name}/containers/{id}/logs` | Recent output of one container |
| `POST` | `/api/profiles/{name}/containers/{id}/start` | Start a stopped container (3.36) |
| `POST` | `/api/profiles/{name}/containers/{id}/stop` | Stop a running container with Docker's default grace period (3.36) |
| `POST` | `/api/profiles/{name}/containers/{id}/restart` | Restart a container with Docker's default grace period (3.36) |
| `GET` | `/api/profiles/{name}/actions` | Recent action log entries |
| `GET` | `/api/profiles/{name}/ssh-config` | SSH settings of a profile's VM (3.31) |
| `GET` | `/api/profiles/{name}/kubernetes` | Whether Kubernetes is enabled in a running profile's configuration (3.33) |
| `POST` | `/api/profiles/{name}/kubernetes/start` | Start the Kubernetes cluster as a job (3.34) |
| `POST` | `/api/profiles/{name}/kubernetes/stop` | Stop the Kubernetes cluster as a job (3.34) |
| `POST` | `/api/profiles/{name}/kubernetes/reset` | Reset the cluster, destroying its workloads and state; needs a confirmation text (3.34) |
| `POST` | `/api/profiles/{name}/kubernetes/delete` | Delete the cluster, destroying its workloads and state; needs a confirmation text (3.34) |
| `POST` | `/api/colima/cache/prune` | Clear Colima's cached downloads as a global job; needs a confirmation text (3.35) |
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

### 3.4 Start, stop, restart and update

`POST /api/profiles/{name}/start`, `POST /api/profiles/{name}/stop`, `POST /api/profiles/{name}/restart`, `POST /api/profiles/{name}/update`

`update` runs `colima update -p <profile>`, which refreshes the container runtime inside a running VM, and is refused with `409 invalid_state` unless the profile is running. The job has `action` `update`, holds the profile slot, streams output, can be cancelled and writes an `update` action log row.

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
- `409` `invalid_state` with `details.reason` `cache_prune_running` (`details.jobId` is the prune) for start and restart while a cache prune runs (3.35).
- `409` `invalid_state` for `update` when the profile is not running (`details.status` is the current status; nothing is started).
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

- `action`: `start`, `stop`, `restart`, `update`, `delete` (3.32), `create` (3.17), `image-pull` (3.30), or `kubernetes-start`, `kubernetes-stop`, `kubernetes-reset` or `kubernetes-delete` (3.34). An `image-pull` job has `exitCode` `0` when it succeeded, `1` when it failed and `null` when it was cancelled; its `output` is a compact progress text, not process output.
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
      "fullId": "3f9a1c0b7d2e5a46c8d1f0b92e7a3c5d8f1b4e6a07c9d2e5f8a1b3c6d9e0f247",
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

- `id` is the value used as `scope` in the metrics route and in `metric` events, and in the logs route (3.16). It is the first 12 characters of the container id.
- `fullId` is the full 64 character lowercase hex id, the only id the container action routes (3.36) accept. It is an empty string if Docker reported an id of another shape.
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

- `action` is a string such as `start`, `stop`, `restart`, `delete`, `create`, `config_change`, `docker.image.pull`, `docker.container.restart` or `kubernetes-reset`. Clients must tolerate new values. The `detail` of a Kubernetes row names the verb (`kubernetes reset`, `kubernetes stop: colima exited with status 1`); a reset or delete that was cancelled or failed after colima ran adds `; the cluster may be partly changed`, and one that never ran says `not run: profile was not running or Kubernetes is not enabled`.
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

This route only reads. Tag, inspect, remove and prune are 3.26 to 3.29; there is no pull yet.

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

This route only reads. Volumes are created, inspected, removed and pruned by the routes in 3.18 to 3.21.

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

This route only reads. Networks are created, inspected, removed and pruned by the routes in 3.22 to 3.25.

Errors are the same as for 3.13.

### 3.15 `GET /api/profiles/{name}/engine`

Token: yes. Request body: none. Used by the Docker tab to show the engine facts and disk usage of the VM.

Response `200`:

```json
{
  "version": "27.3.1",
  "apiVersion": "1.47",
  "os": "linux",
  "arch": "aarch64",
  "cpus": 4,
  "memoryBytes": 8350000000,
  "containers": { "total": 5, "running": 3, "paused": 0, "stopped": 2 },
  "images": 12,
  "diskUsage": {
    "imagesBytes": 1200000000,
    "containersBytes": 4000000,
    "volumesBytes": 73400320,
    "buildCacheBytes": 0,
    "layersBytes": 900000000
  }
}
```

- `version` is the engine's version. `os` is the engine's OS type (`linux`), `arch` its architecture and `cpus` and `memoryBytes` what the engine sees inside the VM.
- `apiVersion` is the highest API version the engine supports, read from the engine's version call. It is not the API version this backend speaks to the engine, which is older and fixed.
- `containers` and `images` are the engine's own counts. They are a snapshot taken when the engine answers, so they can differ slightly from the list routes, which are separate calls.
- `diskUsage` is the engine's disk accounting in bytes, the same figures the sampler records for the capacity charts. `layersBytes` counts shared layers once, so it is not the sum of the image sizes.
- The response is a fixed subset. It never contains the engine's host name, its data directory, registry settings or any other host path.
- The engine facts and the disk usage are read in parallel under one 15 second budget. If either read fails, the route answers `502`.

This route only reads. There is no prune or cleanup.

Errors:

- `404` `profile_not_found`.
- `409` `invalid_state`: the VM is stopped (`details.status` is `stopped`).
- `409` `invalid_state`: the VM is running but the profile does not use the Docker runtime, so there is no engine to describe. `details` is `{"runtime":"not_docker"}`. This differs from the list routes, which return an empty list.
- `502` `docker_unavailable`: the VM is running but the Docker socket is missing or not answering.

### 3.16 `GET /api/profiles/{name}/containers/{id}/logs`

Token: yes. Request body: none. Used by the Docker tab to show why a container stopped or failed.

Query parameter, optional: `tail`, the number of lines wanted. The default is 200 and values above 1000 are clamped to 1000. A value below 1, a value that is not a plain base 10 integer (no sign, no spaces, no hex, no decimals), an empty value or a repeated `tail` returns `400` `invalid_range` with `details.parameter` set to `tail`.

Response `200`:

```json
{
  "lines": [
    "2026-05-02T09:41:07.405003140Z server started",
    "2026-05-02T09:41:08.112003900Z listening on :8080"
  ],
  "truncated": false
}
```

1. `id` is the container id as `GET /api/profiles/{name}/containers` returns it: 12 to 64 lowercase hexadecimal characters. A name, a prefix, an uppercase id or anything else returns `404` `container_not_found` with a fixed message that does not repeat the input.
2. Each line starts with Docker's timestamp (RFC 3339 with nanoseconds, UTC) followed by a space and the text. Stdout and stderr are merged in the order Docker sent them, and there is no field that tells them apart.
3. The text is UTF-8: invalid bytes are replaced with U+FFFD. Trailing `\n` and `\r` are removed. A line is cut at 4000 characters, timestamp included. Output is split on newlines, so there is one entry per line. A line that Docker stored as several pieces (the json-file driver splits lines over 16 KiB) is joined back into one line, which is then cut at 4000 characters. A final line without a newline is kept.
4. `truncated` is `true` when the container has older output than `tail` lines, when any line was cut, when lines were dropped to keep the response under 512 KiB, or when Docker reported a read error part way through the output (the text of that error is never returned). The oldest lines are dropped first.
5. `lines` is `[]` for a container that has written nothing.
6. Stopped containers work, because Docker keeps their output. A container whose log driver does not keep readable logs (for example `none` or `syslog`) returns `409` `invalid_state` with `details` set to `{"reason":"logs_unavailable"}`.
7. Lines are raw container output. They can contain ANSI escape sequences, control characters and secrets, so clients must render them as plain text and never as HTML. The backend does not log or store them.
8. The snapshot is bounded by a 15 second read budget. There is no follow mode and no streaming.

This route only reads. There is no container start, stop or remove.

Errors:

1. `400` `invalid_range`: `tail` is not valid (`details.parameter` is `tail`).
2. `404` `profile_not_found`.
3. `404` `container_not_found`: the id is malformed, the container does not exist, or the profile is running but does not use the Docker runtime.
4. `409` `invalid_state`: the VM is stopped (`details.status` is `stopped`).
5. `409` `invalid_state`: the log driver cannot be read (`details.reason` is `logs_unavailable`).
6. `502` `docker_unavailable`: the VM is running but the Docker socket is missing or not answering, or Docker sent a stream that is not valid log framing.

### 3.17 `POST /api/profiles`

Token: yes. Creates a profile by starting a new VM with the basic settings and, optionally, the advanced ones (networking, DNS, hostname, SSH agent, Rosetta, port forwarder) and host folder mounts and environment variables for the VM. It is the same kind of job as a start (3.4), so the call returns at once, the output streams over the event stream (section 5) and the job appears in the action log with the action `create`.

Request body: a JSON object with a required `name` and these optional settings. Unknown keys are rejected, keys match exactly (case included) and may appear once. A key set to `null` counts as absent. The body is at most 256 KiB.

```json
{
  "name": "dev",
  "cpus": 4,
  "memoryGiB": 8,
  "diskGiB": 100,
  "rootDiskGiB": 20,
  "runtime": "docker",
  "vmType": "vz",
  "arch": "aarch64",
  "mountType": "virtiofs",
  "kubernetes": false,
  "kubernetesVersion": "",
  "hostname": "dev-vm",
  "dns": ["1.1.1.1", "2001:db8::1"],
  "dnsHosts": [{ "name": "example.com", "address": "192.0.2.10" }],
  "sshAgent": true,
  "vzRosetta": true,
  "portForwarder": "ssh",
  "mountInotify": false,
  "networkAddress": true,
  "networkMode": "bridged",
  "networkInterface": "en0",
  "mounts": [{ "path": "/home/user/src", "writable": false }],
  "env": { "HTTP_PROXY": "http://192.0.2.1:3128" }
}
```

1. A setting that is left out is not passed to Colima, so Colima's default applies. A body with only `name` is valid.
2. `name` follows the same rules as every other route (a letter or digit first, then letters, digits, dots, underscores and hyphens, at most 64 characters, no `..`). `default` is allowed. `colima` and any name starting with `colima-` (in any letter case) are rejected on `name`, because Colima maps them onto the default profile or another profile, so a creation would rewrite an existing profile. Names that merely contain the word (`colimadev`, `my-colima`) are fine. Names that differ only by letter case are the same profile.
3. `cpus`, `memoryGiB`, `diskGiB` and `rootDiskGiB` use the same ranges as the config route (3.8). `runtime` is `docker`, `containerd` or `incus`; `vmType` is `qemu`, `vz` or `krunkit`; `arch` is `aarch64` or `x86_64`; `mountType` is `sshfs`, `9p` or `virtiofs`. `kubernetesVersion` must look like `v1.31.2+k3s1`.
4. `kubernetesVersion` needs `kubernetes` to be `true`; otherwise it is rejected on `kubernetesVersion`. The route does not check which other combinations Colima accepts (for example Kubernetes with the `incus` runtime, or `9p` with the `vz` VM type). Colima refuses those itself and the job then fails with its output.
5. Colima saves the settings to the new profile's YAML file, so later starts reuse them.
6. The advanced settings are all optional, and each one becomes exactly one Colima flag (an array becomes the flag repeated, once per element). Every value is checked against the rule below before anything starts. A rejected value is answered on its key (for an array element on `dns[0]`, `dnsHosts[1].name` or `dnsHosts[1].address`) and nothing is launched. `false` and an empty value mean the same as leaving the key out.
   1. `hostname` (`--hostname`): one DNS label, 1 to 63 letters, digits or hyphens, starting and ending with a letter or digit.
   2. `dns` (`--dns`, repeated): an array of at most 8 IPv4 or IPv6 addresses. IPv4-mapped IPv6 addresses (`::ffff:192.0.2.1`) are accepted. A zone (`fe80::1%en0`), a port, a prefix or a host name is rejected. Repeated addresses are passed on as given.
   3. `dnsHosts` (`--dns-host name=address`, repeated): an array of at most 16 objects with exactly the keys `name` and `address`. `name` is a host name of dot-separated labels (letters, digits and hyphens, each label at most 63 characters, at most 253 in all, no trailing dot); `address` is an IPv4 or IPv6 address. A null element, an unknown or repeated key, or a value that is not a string is a `400`.
   4. `sshAgent` (`--ssh-agent`): boolean, forwards the SSH agent into the VM.
   5. `vzRosetta` (`--vz-rosetta`): boolean, enables Rosetta for amd64 emulation. Accepted only with `vmType` `vz` and `arch` `aarch64`; a left-out `vmType` or `arch` counts as Colima's default for this machine (`vz` and, on Apple Silicon, `aarch64`; on an Intel Mac a left-out `arch` is `x86_64`, so Rosetta then needs `arch` set to `aarch64` explicitly). Otherwise `422` on `vzRosetta`.
   6. `portForwarder` (`--port-forwarder`): `ssh`, `grpc` or `none`.
   7. `mountInotify` (`--mount-inotify`): boolean, propagates inotify file events to the VM.
   8. `networkAddress` (`--network-address`): boolean, assigns a reachable IP address to the VM.
   9. `networkMode` (`--network-mode`): `shared` or `bridged`.
   10. `networkInterface` (`--network-interface`): a host interface name, a letter or digit first, then letters, digits, dots, underscores and hyphens, at most 15 characters. Accepted only with `networkMode` `bridged`, otherwise `422` on `networkInterface`.
   11. `cpuType` (`--cpu-type`): a single CPU model name such as `max` or `cortex-a72`, a letter or digit first, then letters, digits, dots, underscores and hyphens, at most 64 characters. A feature list such as `host,+x2apic` is rejected.
7. Only the two cross-field rules above are checked here, because Colima's help text states them. Other combinations (`cpuType` with a VM type other than `qemu`, `bridged` without `networkAddress`, a port forwarder that a VM type does not support) are left to Colima: it refuses what it cannot run and the job then fails with its output.
8. `mounts` and `env` are the settings with real security weight, so they are validated more strictly than the rest. Both are optional; `null`, `[]` and `{}` mean the same as leaving the key out. Neither is ever repeated back in an error message or written to a log: a rejection names only the field.
   1. `mounts` (`--mount`, repeated): an array of at most 16 objects with exactly the keys `path` (a string) and `writable` (a boolean, default `false`). A read-only mount becomes `--mount path` and a writable one `--mount path:w`, in array order. Every mounted folder is visible to every container in the VM, and a writable one can be changed from inside it. An array of more than 16 is rejected on `mounts` alone, before any element is examined. A null or non-object element, an unknown key, a differently cased key (`Path`) or a repeated key is a `400`.
   2. `mounts[i].path` must be absolute and already clean: it starts with `/` and has no `..` or `.` segment, no repeated slash and no trailing slash (`/` itself is clean). It is at most 4096 bytes, valid UTF-8, and has no control character (NUL, newline and so on). It must not contain a comma, a double quote or a colon: Colima reads `--mount` as a comma separated, CSV-quoted list (so a comma would add a second mount and a quote is a parse error), and a colon begins the `:w` suffix. It must not contain `{`, `}` or `$` either, which start template and environment expansion in the tools Colima passes the value to. Spaces and other punctuation are fine and stay inside one argument.
   3. The path must exist on this machine at request time and be a directory. A missing path, a file and an unreadable path get the same message. Symlinks are resolved on purpose: the path that is checked is the resolved one, and the resolved path is what is passed to Colima, so a link cannot hide a broad folder, two names for one folder count as a duplicate, and a link swapped after the check has no effect. Two entries that are the same folder are rejected on the later `mounts[i].path`; sameness is decided by file identity, so case, Unicode normalisation and firmlink variants of one folder count too. The root `/` and the home folder, and the folders that contain the home folder (such as `/Users`), are too broad: they are refused when `writable` is `true` and allowed read-only. If the home folder cannot be determined, every writable mount is refused. The too-broad check is by file identity (device and inode), not by string, so APFS firmlinks (`/System/Volumes/Data/Users/me` is `/Users/me`), case and Unicode normalisation variants and bind mounts cannot get past it; a case-insensitive string comparison runs first as a cheap guard. A path whose identity cannot be read counts as not matching.
   4. `env` (`--env KEY=value`, repeated): a JSON object of string values, at most 32 entries. A repeated key is a `400`; keys that differ only in case are different variables. The entries are passed in the order of their keys (byte order), so the same body always gives the same command. Each key matches `[A-Za-z_][A-Za-z0-9_]*` and has at most 128 characters. Each value is at most 4096 bytes, has no NUL byte, may be empty and is passed as one argument, but two parser quirks are refused. Colima's `--env` is a pflag `stringToString`, probed read-only on 0.10.3 with `colima start --env ... --help`: with exactly one `=` in `KEY=value` it only trims double quotes off both ends of the whole argument, so `A=x"` would lose its quote (`A=x"y` is fine); with two or more `=` it parses the argument as one CSV record, so `A=x=1,"B=2"` silently creates a second variable `B` (past the key pattern and the 32 cap), `A=x=1,junk` and `A=x=1,B"q` fail with the value echoed into the job output, and a line break after the second `=` drops the rest. A value is therefore rejected when it ends with a double quote, or when it contains `=` together with a comma, a double quote, a carriage return or a line feed. Commas, quotes inside, line breaks, semicolons and a lone `=` are otherwise fine. An object of more than 32 entries is rejected on `env` alone. A bad key is answered on `env[i]`, `i` being its position in the sorted order (the key text is never echoed); a bad value is answered on `env.KEY`, where KEY has already passed the key pattern.
   5. What Colima does with the values is Colima's: for example, how it writes a value that holds a newline into the VM is not checked here.
9. What `colima start --help` (Colima 0.10.3, read-only) says about these flags: `-V, --mount strings: directories to mount, suffix ':w' for writable, disable with 'none'` and `--env stringToString: environment variables for the VM (default [])`. The help does not say whether giving `--mount` replaces the default mount of the home folder; that behaviour is unverified here. The UI must therefore tell the user that mounts they list may replace the default home mount. The API never sends `none`, so a request cannot ask for the default mounts to be disabled.

Response `202`:

```json
{ "jobId": "j_8f3a2c", "profile": "dev" }
```

`profile` echoes the requested name. The client then follows the job as in 3.4.

Errors:

1. `400` `bad_request`: malformed JSON, a body that is not an object, an unknown, repeated or differently cased key, a value of the wrong type (including a number that is not an integer where one is needed or does not fit), or a body over 256 KiB. Nothing is launched.
2. `422` `validation_failed`: `details.fields` maps every rejected key to a fixed message, `name` included (missing, empty or malformed). Messages never repeat the request text. Nothing is launched. Example:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Some values are not allowed.",
    "details": { "fields": { "name": "is required", "cpus": "must be between 1 and 16" } }
  }
}
```

3. `409` `profile_exists`: the name is listed by Colima or has a folder under the Colima home, in any letter case. Nothing is launched, so an existing profile's settings are never rewritten.
4. `409` `profile_busy`: a job is already running on that name, in any letter case. `details.jobId` is the running job.
5. `502` `colima_failed`: Colima could not be queried or launched.
6. `503` `shutting_down`: the backend is stopping and accepts no new jobs.
7. `500` `internal_error`: unexpected failure.

A failed creation can leave a half-created profile behind (a folder, a YAML file, a partial VM). It then shows up in the profile list as stopped. The user can remove it with the delete route (3.32). Until Colima has written the profile's folder, `GET /api/profiles/{name}/actions` answers `404` for the new name, even though the job and its action row exist.

### 3.18 `POST /api/profiles/{name}/volumes`

Token: yes. Creates a local volume. It follows the Docker write rules in 1.3 (profile, busy check, strict body, validation, one fixed Docker call, action log row `docker.volume.create`). The body is at most 16 KiB.

Request:

```json
{
  "name": "data",
  "labels": { "team": "backend" }
}
```

- `name` is required and follows the object name rule of 1.3.
- `labels` is optional (absent, `null` and `{}` mean none). It is the one nested value of a write body, so this route validates it itself, by hand, before Docker is reached: it must be an object whose values are all strings; at most 16 labels; a repeated key is refused (the usual JSON parsers let the last one win, so it is refused instead); two keys that differ only in letter case are refused; a key is 1 to 63 characters of `[a-zA-Z0-9_.-]` starting with a letter or digit, and may not start with `com.docker.`, `io.docker.` or `org.dockerproject.` (compared without regard to case), which Docker reserves; a value is valid UTF-8 of at most 256 bytes without control characters, and may be empty. Any failure is `422 invalid_request` with `details.fields` holding the single key `labels` and a fixed message: the answer never repeats a key or a value.
- No driver and no driver options are accepted (`driver`, `driverOpts` are unknown keys, `400`). The volume is always created with the `local` driver.

Response `201`, the same fields as a list entry (3.13):

```json
{
  "name": "data",
  "driver": "local",
  "createdAt": "2026-04-11T18:02:55Z",
  "sizeBytes": null,
  "containers": null
}
```

`sizeBytes` and `containers` are `null` because Docker's create answer carries no usage figures. The labels are not echoed; read them with 3.19.

Errors: the shared ones of 1.3, plus `409 conflict` (`details.name`) when a volume with that name already exists. Docker itself answers `201` with the existing volume when the name and driver match, so the backend checks for the name first (an inspect, `404` means free) and refuses. Two creates of the same name racing each other can both pass that check; Docker then treats the second as a no-op on the same volume, nothing is overwritten.

### 3.19 `GET /api/profiles/{name}/volumes/{volume}`

Token: yes. Request body: none. Inspects one volume. It follows the profile, body, validation and error rules of 1.3 but it is a read: it does not check the busy state (it works while a job runs) and it writes no action log row. Clients should still not poll it: it is meant to be asked for when the user opens a volume, and it can be slow because the size comes from the engine's disk usage report.

Response `200`:

```json
{
  "name": "pgdata",
  "driver": "local",
  "createdAt": "2026-04-11T18:02:55Z",
  "sizeBytes": 73400320,
  "labels": { "team": "backend" },
  "containers": 1,
  "containerNames": ["db"]
}
```

- `labels` are the volume's labels as Docker reports them (an empty object when none). They are the user's own data and are returned as stored; they are never logged and never appear in an error answer.
- `containers` is the number of containers, running or not, that mount the volume, and `containerNames` lists their names (sorted; a container without a name shows its short id).
- `sizeBytes` is `null` when the engine has no figure. It comes from the engine's disk usage report, the only call that has sizes, so this route can be slow on VMs with large volumes.
- The response never contains the mountpoint, the driver options or any other host path.

Errors: the shared ones of 1.3, plus `404 not_found` (`details.name`) for an unknown volume and `422 invalid_request` (`details.fields.volume`) for a malformed name.

### 3.20 `DELETE /api/profiles/{name}/volumes/{volume}`

Token: yes. Request body: none. Removes one volume. Response `204` with no body. Action log row `docker.volume.remove`.

The removal is never forced: no `force` parameter exists, and the backend never sends one to Docker. Before removing, it asks Docker which containers, running or stopped, mount the volume. If there is at least one, no remove request is sent and the answer is `409 conflict` with `details.name` and `details.containers` (the number of containers; no names, no paths). If a container starts using the volume after that check, Docker refuses itself and the answer is the same `409` without `details.containers`.

Errors: the shared ones of 1.3, plus `404 not_found` for an unknown volume, `409 conflict` for a mounted volume and `422 invalid_request` (`details.fields.volume`) for a malformed name. A volume literally named `prune` is removed with this route like any other.

### 3.21 `POST /api/profiles/{name}/volumes/prune`

Token: yes. Removes unused volumes. **This deletes data and cannot be undone.** Because of that the request must carry a fixed confirmation text. Action log row `docker.volume.prune` with no detail. It has a 2 minute budget.

Request:

```json
{
  "confirm": "delete-unused-volumes"
}
```

- `confirm` must be exactly `delete-unused-volumes`. An empty body, `{}`, a missing or `null` key and any other string answer `422 invalid_request` with `details.fields` holding `confirm`; Docker is not reached and no log row is written. Any other key (including a filter), a repeated key, trailing data and a non-object body are `400 bad_request`. The body is at most 1 KiB.

Response `200`:

```json
{
  "removed": ["scratch", "9f2c0d1e4b7a3c5d8e6f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5"],
  "removedCount": 2,
  "truncated": false,
  "reclaimedBytes": 104857600,
  "includesNamedVolumes": true,
  "resultComplete": true
}
```

- `removed` lists the volumes Docker deleted (sorted, `[]` when there was nothing to remove), at most 1000 names. Names that do not follow the object name rule of 1.3 are never listed or echoed.
- `removedCount` is the number of volumes Docker says it deleted. `truncated` is `true` when `removed` lists fewer names than `removedCount` (more than 1000 volumes, or names that were left out). Names are never dropped without `truncated` being set.
- `reclaimedBytes` is the space Docker says it freed, at least `0`.
- `includesNamedVolumes` is `true`: on the Docker API version this backend pins (v1.41) the prune removes every volume that no container uses, **named volumes included**, not only anonymous ones. A volume you created by name and have not attached yet is removed too.
- `resultComplete` is `false` when Docker ran the prune (it answered `200`) but its answer could not be read in full: the body was too large or not the expected JSON, or `SpaceReclaimed` does not fit a 64-bit integer. The volumes are already gone, so the route still answers `200` and writes a `success` row; it reports what it does not know as `null` (`removedCount`, `reclaimedBytes`) and `removed` may be empty or short. A client must show this as "the prune ran, the figures are unknown", never as a failure:

```json
{
  "removed": [],
  "removedCount": null,
  "truncated": false,
  "reclaimedBytes": null,
  "includesNamedVolumes": true,
  "resultComplete": false
}
```

How the v1.41 behaviour was verified. The v1.41 API description of `POST /volumes/prune` lists `label` as the only filter and no `all` filter. The API version history for v1.42 says: "`POST /volumes/prune` will now only prune "anonymous" volumes (volumes which were not given a name) by default. A new filter parameter `all` can be set to a truth-y value (`true`, `1`) to get the old behavior." The backend sends `/v1.41/volumes/prune` with no filters, so it gets the pre-1.42 behaviour: all unused volumes. A newer engine keeps that behaviour for a client that asks for v1.41 (the engine answers each request according to the version in its URL). The check was made against the published documentation only; nothing was pruned on any engine to test it. A unit test pins the API version and fails if it changes, so moving the pin forces this decision to be made again.

Errors: the shared ones of 1.3, plus the `422` and `400` above.

### 3.22 `POST /api/profiles/{name}/networks`

Token: yes. Creates a bridge network. It follows the Docker write rules in 1.3 (profile, busy check, strict body, validation, one fixed Docker call, action log row `docker.network.create`). The body is at most 4 KiB.

Request:

```json
{
  "name": "backend",
  "internal": false,
  "subnet": "172.28.0.0/16"
}
```

- `name` is required and follows the object name rule of 1.3. The names `bridge`, `host`, `none` and `ingress` are refused in any letter case (`422`, `details.fields.name`), like their removal.
- `internal` is optional (absent and `null` mean `false`). An internal network has no route to the outside.
- `subnet` is optional (absent and `null` mean that Docker chooses). It is one CIDR, IPv4 or IPv6, and nothing else: no list, no gateway, no IP range. Anything that fails the rules below is `422 invalid_request` with `details.fields` holding the single key `subnet` and a fixed message; the answer never repeats the value.
  1. It must be ASCII text of at most 64 characters, with no spaces, control or format characters, no zone and no bare address, and an IPv4-mapped IPv6 address (`::ffff:...`) is refused.
  2. The prefix length must be 8 to 28 for IPv4 and 48 to 120 for IPv6. That refuses `/0` and ranges too wide to be a network, and ranges too small to hold a gateway and containers.
  3. The range must not overlap any of these, and overlap is what counts, so a short prefix cannot contain a refused range (`192.168.0.0/16` is refused because it contains `192.168.5.0/24`):
     1. `127.0.0.0/8` and `::1`, loopback.
     2. `0.0.0.0/8` and `::/8`, the unspecified or "this network" range, never valid for hosts (this also refuses `0.1.0.0/16`).
     3. `169.254.0.0/16` and `fe80::/10`, link-local.
     4. `224.0.0.0/4` and `ff00::/8`, multicast.
     5. `240.0.0.0/4`, reserved, and `::ffff:0:0/96`, IPv4-mapped.
     6. `192.168.5.0/24`, the user-mode network Colima's VM uses for its own gateway and DNS. A network on it would cut the VM off.
  4. **Host bits are ignored.** `172.28.0.5/16` is read as `172.28.0.0/16`, and the masked form is what is sent to Docker, so Docker never stores something other than what was checked.
  Docker checks the new range against its other networks only and answers `409 conflict` on an overlap there. It does not check the host's or the VM's routes: apart from `192.168.5.0/24` above, a range that collides with something else on the VM or the host's network is not detected.
- No driver and no options are accepted (`driver`, `options`, `attachable`, `enableIPv6`, `gateway` and the like are unknown keys, `400`). The network is always created with the `bridge` driver. The Docker request carries exactly: the name, the driver `bridge`, `Internal`, `CheckDuplicate: true`, at most one IPAM subnet, and `EnableIPv6: true` only when that subnet is IPv6 (Docker ignores an IPv6 subnet on a network without IPv6). It never carries driver options, labels, `Attachable`, `Ingress` or a swarm scope.

Response `201`, the same fields as a list entry (3.14):

```json
{
  "id": "9d1c0a47be12",
  "name": "backend",
  "driver": "bridge",
  "scope": "local",
  "internal": false,
  "containers": 0
}
```

If Docker answered `201` but its answer could not be read, the network exists and the route still answers `201` with `"id": ""`: the list (3.14) has the id.

Errors: the shared ones of 1.3, plus `409 conflict` (`details.name`) when a network with that exact name exists, and `422` for `name` or `subnet`. Duplicate names are refused in two ways. The backend first looks the name up (an inspect) and does not create anything if that lookup fails for any reason other than "no such network". Only a network whose name equals the requested one counts as a duplicate: a name that merely starts an existing network's id is free, and so is a lookup Docker answers with `400` (an ambiguous id prefix or two networks of one name, where no single network is meant). The create request also carries `CheckDuplicate`, because on Docker API v1.41 a create without it makes a second network with the same name, which then cannot be addressed by name. Docker's own documentation calls that check best effort, so two creates of one name racing each other can both pass and leave two networks with that name; the list shows both by id. This was checked against the published API description only; no network was created on any engine to test it.

### 3.23 `GET /api/profiles/{name}/networks/{network}`

Token: yes. Request body: none. Inspects one network by name, or by its short (12 characters) or full (64 characters) lowercase hex id. It follows the profile, body, validation and error rules of 1.3 but it is a read: it does not check the busy state and it writes no action log row. Clients should not poll it; ask for it when the user opens a network.

Response `200`:

```json
{
  "id": "9d1c0a47be12",
  "name": "backend",
  "driver": "bridge",
  "scope": "local",
  "internal": false,
  "subnets": [{ "subnet": "172.28.0.0/16", "gateway": "172.28.0.1" }],
  "containers": [{ "name": "api", "ipv4Address": "172.28.0.2", "ipv6Address": "" }]
}
```

- `subnets` lists the network's address ranges in canonical form, `gateway` is `""` when there is none. Ranges the engine reports in a form that is not a CIDR are left out.
- `containers` lists the containers that are attached: those the engine reports as connected (with their addresses, without the prefix length) and those the container list says use the network, found by the network's id or name, stopped and never-started ones included (with empty addresses). A never-started container that was configured with the network by id rather than by name cannot be found before it starts, so the list is a best effort, not a guarantee. It is sorted by name. A container name that is not a plain name is replaced by its short id. Nothing else about a container is returned.
- Options, labels, endpoint ids and MAC addresses are never returned.
- A name and a 12 character id look alike: Docker resolves names before id prefixes, so a network whose name is a 12 character hex string is the one found for that text, not the network with that id. A client must send the network's name (the UI of issue #123 sends the name). A text that two networks match (an ambiguous id prefix, or two networks of one name) is `404`.
- `name` is the network's name when it is a plain object name, else its short id. All other strings are re-parsed or fixed values.
- Text that is neither a valid name nor a 12 or 64 character hex id is `422` (`details.fields.network`). Docker also resolves a name by any id prefix; the backend does not: if the network Docker finds is not the one the text names exactly (its name, or a 12 or 64 character id prefix), the answer is `404`.

Errors: the shared ones of 1.3, plus `404 not_found` (`details.name`) for an unknown network and `422 invalid_request` (`details.fields.network`).

### 3.24 `DELETE /api/profiles/{name}/networks/{network}`

Token: yes. Request body: none. Removes one network, found by name or id as in 3.23. Response `204` with no body. Action log row `docker.network.remove`.

The removal is never forced: no `force` parameter exists and the backend never sends one. Before removing, it looks the network up and refuses, without sending any remove request:

1. The predefined networks `bridge`, `host` and `none`, matched by name in any letter case (so also when reached by id), and the swarm `ingress` network: `409 conflict` with `details.name`.
2. A network with attached containers, running, stopped or created but not started (found as in 3.23, with the same best-effort limit): `409 conflict` with `details.name` and `details.containers` (the number of containers; no names, no addresses). If a container attaches after that check, Docker refuses itself and the answer is the same `409` without `details.containers`.

The remove request then goes to the network's full id, as Docker reported it in the lookup, never to the text of the URL.

Errors: the shared ones of 1.3, plus `404 not_found` for an unknown network, `409 conflict` as above and `422 invalid_request` (`details.fields.network`). A network literally named `prune` is removed with this route like any other.

### 3.25 `POST /api/profiles/{name}/networks/prune`

Token: yes. Removes unused networks. This cannot be undone. Because of that the request must carry a fixed confirmation text. Action log row `docker.network.prune` with no detail. It has a 2 minute budget. Docker never prunes the predefined networks (`bridge`, `host`, `none`). What counts as unused is Docker's own rule and the backend does not second-guess it: a network that no running container uses may be removed even if a stopped container is configured for it. No filters are sent and none can be given.

Request:

```json
{
  "confirm": "delete-unused-networks"
}
```

- `confirm` must be exactly `delete-unused-networks`. An empty body, `{}`, a missing or `null` key and any other string (including the volume prune text) answer `422 invalid_request` with `details.fields` holding `confirm`; Docker is not reached and no log row is written. Any other key, a repeated key, trailing data and a non-object body are `400 bad_request`. The body is at most 1 KiB.

Response `200`:

```json
{
  "removed": ["old-backend", "scratch"],
  "removedCount": 2,
  "truncated": false,
  "resultComplete": true
}
```

- `removed` lists the networks Docker deleted (sorted, `[]` when there was nothing to remove), at most 1000 valid names. Names that do not follow the object name rule of 1.3 are never listed or echoed.
- `removedCount` is the number of networks Docker says it deleted. `truncated` is `true` when `removed` lists fewer names than `removedCount`.
- `resultComplete` is `false` when Docker ran the prune (it answered `200`) but its answer could not be read: the body was too large or not the expected JSON. The networks are already gone, so the route still answers `200`, writes a `success` row and reports `removedCount` as `null` with `removed` empty. A client must show this as "the prune ran, the list is unknown", never as a failure:

```json
{
  "removed": [],
  "removedCount": null,
  "truncated": false,
  "resultComplete": false
}
```

Errors: the shared ones of 1.3, plus the `422` and `400` above.

### 3.26 `POST /api/profiles/{name}/images/{image}/tag`

Token: yes. Adds a tag to an image. It follows the Docker write rules in 1.3 (profile, busy check, strict body, validation, Docker calls, action log row `docker.image.tag`; the `detail` is `<image text> -> <repository>:<tag>`, built from the validated values). The body is at most 4 KiB.

`{image}` is the image's short (12 characters) or full (64 characters) lowercase hex id, or a reference the image carries (3.27 explains how it is matched). A reference containing `/` or `:` must be sent URL-escaped (`team%2Fapp%3Av1`).

Request:

```json
{
  "repository": "registry.example.com:5000/team/app",
  "tag": "v2"
}
```

- `repository` and `tag` are both required (no default `latest`). A missing or `null` value is `422 invalid_request` with `details.fields` naming each rejected key; the answer never repeats a value.
- `repository` is the name part of a reference: lowercase path components separated by `/`, optionally preceded by a registry host with an optional port (`localhost:5000/app`, `ghcr.io/owner/app`; the host may use uppercase letters, IPv6 hosts are refused). No tag, no digest, no `@`, no whitespace, no control or format characters. A single 64 character hex name and the name `sha256` are refused (they would make `sha256:<hex>` read like an image id). The text is passed to Docker as given and Docker applies its own defaults (an unqualified name means Docker Hub); no registry is contacted.
- `tag` is 1 to 128 characters of `[a-zA-Z0-9_.-]`, not starting with `.` or `-`.
- Repository, `:` and tag together are at most 255 characters.

Response `204` with no body (read the image list, 3.12, for the new tag list).

What the backend does, in order: looks the image up (3.27 rules, `404` when unknown); looks `repository:tag` up (only a `404` means the tag is free; any other answer, a `409` included, stops the request); and only then sends the tag request, to the image's **full id**, never to the URL text. Docker's own tag call silently moves a tag that already exists from the image that holds it to the new one, which would leave the old image untagged. The backend refuses that: a `repository:tag` that is already on **another** image is `409 conflict` and no tag request is sent (untag it first with 3.28). A tag that is already on this very image succeeds without a request. If the lookup of the target cannot be answered, nothing is sent (fail closed). The lookup and the tag request run one at a time per engine inside this backend, so two tag requests through the UI cannot steal a tag from each other. They are still not atomic against Docker: a tag created by a `docker` command in a terminal in between can still be moved by Docker.

Errors: the shared ones of 1.3, plus `404 not_found` (`details.name` is the image text), `409 conflict`, `422 invalid_request` (`details.fields` with `image`, `repository` and or `tag`) and `400 bad_request` (unknown key such as `force`, wrong type, trailing data).

### 3.27 `GET /api/profiles/{name}/images/{image}`

Token: yes. Request body: none. Inspects one image. It follows the profile, body, validation and error rules of 1.3 but it is a read: it does not check the busy state and it writes no action log row. Clients must not poll it; ask for it when the user opens an image.

How `{image}` is matched, for this route and for 3.26 and 3.28. A 12 or 64 character lowercase hex text is an image id: the image Docker finds must have an id starting with it. Any other text must be a valid image reference and must equal, exactly, one of the tags or digests of the image Docker found (or equal one of them once `:latest` is added, when the text has no tag and no digest). Docker itself also resolves id prefixes of any length and normalises registry names (`docker.io/library/...`); neither is accepted, so the image acted on is always the one the text names. Consequences: use the tag or digest exactly as the image list shows it; a repository literally named like a 12 character hex id is read as an id and cannot be addressed by name; an ambiguous id prefix (Docker answers `400`) is `404`.

Response `200`:

```json
{
  "id": "3f9a1c0b7d2e",
  "tags": ["app:v1", "app:v2"],
  "digests": ["app@sha256:9b2f5d1c0a7e4b3d8c6f2a1e0d9c8b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c"],
  "sizeBytes": 192000000,
  "createdAt": "2026-05-02T09:41:07Z",
  "architecture": "amd64",
  "os": "linux",
  "exposedPorts": [80, 443],
  "containers": 1,
  "containerNames": ["web"],
  "layers": [
    { "createdAt": "2026-05-02T09:41:07Z", "sizeBytes": 4096 },
    { "createdAt": null, "sizeBytes": 0 }
  ],
  "layerCount": 2
}
```

- `id` is the first 12 characters of the image id. `tags` and `digests` list the references the image carries (only well-formed ones; sorted; `[]` for none).
- `createdAt` is RFC 3339 in UTC, or `null` when Docker's value could not be read. `architecture` and `os` are plain tokens (`amd64`, `linux`) or `""`.
- `exposedPorts` are port numbers only, sorted and without duplicates (the protocol is dropped), at most 100.
- `containers` is the number of containers, in any state, whose image id is this image, and `containerNames` lists their names (sorted; a name that is not a plain name shows the container's short id).
- `layers` is the history as creation time and size only, newest first, at most 200 entries; `layerCount` is the real number of layers.
- **What is never returned.** The command that built each layer (`CreatedBy`: it can hold secrets from `ARG` and `ENV` lines), a layer's comment, tags or author, the image's environment variables, labels, command, entrypoint, user, working directory, volumes, the graph driver paths and the layer digests. They are not decoded from Docker's answer at all, so no later code change can leak them by accident. A test feeds an answer with secrets in every one of those fields and checks that none appears.

Errors: the shared ones of 1.3, plus `404 not_found` (`details.name`) and `422 invalid_request` (`details.fields.image`).

### 3.28 `DELETE /api/profiles/{name}/images/{image}`

Token: yes. Request body: none. Removes an image, or one reference of it. Action log row `docker.image.remove` with the image text as `detail`. It has a 2 minute budget.

The removal is never forced: no `force` parameter exists and the backend never sends one. It sends `noprune=1`, so Docker keeps untagged parent images (only what the caller named is touched; leftover dangling parents are removed by a prune, 3.29). The text is matched as in 3.27 and the delete requests go to the **exact reference Docker reported**, or the image's full id, never to the URL text.

1. **By id** (12 or 64 hex characters): the image and all its tags and digests are removed, but only when no container uses it. Docker refuses a delete by id when the image has more than one reference unless forced, so the backend removes the references on its own, never forced. It deletes the **tags** first, each as a separate request, and skips a tag Docker has already listed as untagged. Docker drops a repository's digest references itself when that repository's last tag goes (and deletes the image with its last reference), so after the tags the backend reads the image again by its full id: `404` there means it is gone; otherwise it removes what is left from that fresh answer (digests, or the image by its full id once no reference remains). A `404` on any request after the first success counts as "already removed", not as an error. If the image has a reference that is not a well-formed reference, nothing is removed and the answer is `409 conflict`.
2. **By tag or digest reference**: only that reference is removed (untag). Docker deletes the image when it was its last reference. On Docker's classic image store removing the last tag of a repository also drops that repository's digest references. On the containerd image store, removing a digest reference may also remove the tags of that repository that point at the image. The `untagged` list of the answer is authoritative about what happened.

Partial failure. A removal by id can take several requests. If one of them fails after an earlier one succeeded, the answer is the usual error (`409` or `502`, same codes and messages as 1.3) and its `details` also carry `untagged` and `deleted` (the validated references and 12 character ids Docker had already reported), so the client can say that part of the removal happened. The action log row says `failed`. A failure of the first request carries neither key.

In use. Before any delete the backend asks Docker for all containers, running, stopped or created, and counts those whose image id is this image. If there is at least one, no delete is sent and the answer is `409 conflict` with `details.name` and `details.containers` (a count, no names). This also applies to an untag of one of several tags: the rule is deliberately simple and conservative. If a container starts using the image after that check, Docker refuses itself and the answer is the same `409` without `details.containers`.

Response `200`:

```json
{
  "untagged": ["app:v1"],
  "deleted": ["3f9a1c0b7d2e"],
  "removedCount": 2,
  "truncated": false,
  "resultComplete": true
}
```

- `untagged` lists the references Docker removed and `deleted` the images it deleted (12 character ids). Both are sorted, at most 1000 entries each, and contain only validated strings: anything else is never listed or echoed.
- `removedCount` is the number of entries Docker reported (untagged plus deleted). `truncated` is `true` when the lists hold fewer entries than `removedCount`.
- `resultComplete` is `false` when Docker answered `200` (it acted) but an answer could not be read in full. The route still answers `200`, writes a `success` row and reports `removedCount` as `null`; the lists may be short. A client must show this as "the removal ran, the details are unknown", never as a failure:

```json
{
  "untagged": [],
  "deleted": [],
  "removedCount": null,
  "truncated": false,
  "resultComplete": false
}
```

Errors: the shared ones of 1.3, plus `404 not_found` (`details.name`), `409 conflict` as above and `422 invalid_request` (`details.fields.image`). An image reference literally named `prune` is removed with this route like any other (the prune route is `POST`).

### 3.29 `POST /api/profiles/{name}/images/prune`

Token: yes. Removes unused images. **This cannot be undone.** The request must carry a fixed confirmation text. Action log row `docker.image.prune` with no detail. It has a 2 minute budget. An image counts as used when any container, in any state, was created from it (Docker's rule; the backend does not second-guess it).

Request:

```json
{
  "confirm": "delete-unused-images",
  "all": false
}
```

- `confirm` must be exactly `delete-unused-images`. An empty body, `{}`, a missing or `null` key and any other string (including the volume and network prune texts) answer `422 invalid_request` with `details.fields` holding `confirm`; Docker is not reached and no log row is written. Any other key (including a filter or `until`), a repeated key, a wrong type, trailing data and a non-object body are `400 bad_request`. The body is at most 1 KiB.
- `all` is optional (absent and `null` mean `false`). `false` removes **dangling** images only (untagged and unused). `true` removes **every** image that no container uses, **tagged images included**.
- The Docker request carries one query value, `filters`, built from fixed text: `{"dangling":["true"]}` for `all: false` and `{"dangling":["false"]}` for `all: true`. The filter is always sent explicitly, never left to Docker's default. No other filter exists.

How the filter was verified. The published Docker Engine API v1.41 description of `POST /images/prune` says for `dangling=<boolean>`: "When set to `true` (or `1`), prune only unused *and* untagged images. When set to `false` (or `0`), all unused images are pruned." The backend pins `/v1.41`. The check was made against the published description only (`https://docs.docker.com/reference/api/engine/version/v1.41.yaml`); nothing was pruned on any engine. A unit test checks the exact query of both modes.

Response `200`:

```json
{
  "untagged": ["app@sha256:9b2f5d1c0a7e4b3d8c6f2a1e0d9c8b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c"],
  "deleted": ["3f9a1c0b7d2e"],
  "removedCount": 2,
  "truncated": false,
  "reclaimedBytes": 104857600,
  "all": false,
  "resultComplete": true
}
```

- `untagged`, `deleted`, `removedCount` and `truncated` mean what they mean in 3.28 (sorted, at most 1000 validated entries each, nothing dropped without `truncated`).
- `reclaimedBytes` is the space Docker says it freed, at least `0`. `all` echoes the mode that ran.
- `resultComplete` is `false` when Docker ran the prune (it answered `200`) but its answer could not be read in full: the body was too large or not the expected JSON, or `SpaceReclaimed` does not fit a 64-bit integer. The images are already gone, so the route still answers `200` and writes a `success` row; what it does not know is `null` (`removedCount` and or `reclaimedBytes`) and the lists may be empty or short. A client must show this as "the prune ran, the figures are unknown":

```json
{
  "untagged": [],
  "deleted": [],
  "removedCount": null,
  "truncated": false,
  "reclaimedBytes": null,
  "all": true,
  "resultComplete": false
}
```

Errors: the shared ones of 1.3, plus the `422` and `400` above.

### 3.30 `POST /api/profiles/{name}/images/pull`

Token: yes. Pulls an image from a **public registry** as a job. A pull can take minutes and reports progress per layer, so the route answers `202` at once and the client follows the job like a start or stop (3.5, 3.6 and section 5).

Request:

```json
{
  "reference": "postgres:18-alpine"
}
```

- `reference` is required: an image reference (the grammar of 1.3, rule 4) that carries a **tag or a digest**, such as `postgres:18-alpine`, `ghcr.io/owner/app:1.2` or `postgres@sha256:...`. A reference with neither (`postgres`, `localhost:5000/app`) is `422 invalid_request` with `details.fields.reference`, so a pull never takes `latest` by accident. A repository named `sha256` or a 64 character hex string is refused too, because it would read like an image ID. A missing key, an empty body and `null` are `422` as well; Docker is not reached.
- Any other key (a platform, credentials, a registry, a filter), a repeated key, a wrong type, trailing data, a nested body and a non-object body are `400 bad_request`. The body is at most 1 KiB.
- Only the profile rules of 1.3 apply before the job starts (profile, running VM, busy check, strict body, validation). The busy check is advisory and the pull does **not** hold the profile's start, stop and restart slot.

Response `202`:

```json
{
  "jobId": "j_8f3a2c",
  "action": "image-pull",
  "profile": "default",
  "state": "running"
}
```

`jobId` is the same key start, stop and restart answer with; the other three fields let a client show the job without a first `GET`. `GET /api/jobs/{id}` (3.5) then returns the job, `POST /api/jobs/{id}/cancel` (3.6) cancels it, and `job.output` and `job.finished` (5.3) carry its output and its end, all with `action` `image-pull`.

The job:

- Output is a compact progress text, one line per layer state change, never Docker's raw stream: `Pulling postgres:18-alpine`, `Pulling from library/postgres`, then per layer (identified by its first 12 hex characters) `waiting`, `downloading (4.0 MB)`, `downloading 25%`, `downloading 50%`, `downloading 75%`, `download complete`, `extracting`, `pull complete` or `already exists`, then `Digest: sha256:...` and a final `Status: downloaded newer image` or `Status: image is up to date, nothing was downloaded`. Byte ticks are not lines. A pull of an image that is already up to date succeeds and says so. The output is bounded like any job output (3.5: `truncated`, `droppedLines`), and the server also keeps at most 256 layers and 3000 lines per pull.
- A failure ends the job `failed` with a last line `error: <message>`. For a registry or daemon error (not found, access denied, a network error) the message is the registry's own text, because it answers the user's own request. It is cleaned before it is shown: control characters, ANSI sequences and bidirectional or zero width characters are removed, whitespace is collapsed, absolute paths and socket addresses become `[path]`, text that looks like raw JSON is dropped, and it is cut to 300 characters. Where there is no usable text, a fixed message is shown (`The pull failed.`, `Docker is not reachable.`). A stream that ends without Docker's final status line is a failed pull.
- Cancelling closes the HTTP stream to Docker and the job ends `cancelled`. A pull that goes quiet for 2 minutes, or runs for 30 minutes, is stopped and fails.
- When the backend shuts down, running pulls are cancelled and awaited like other jobs.
- Action log row `docker.image.pull` with the validated reference as `detail` and the outcome `success`, `failed` or `cancelled`, written when the job ends (this is the one Docker write whose row is not written by the route itself).
- Limits per profile: at most 2 pulls at the same time, and one pull per identical reference. Both limits are released whatever way a pull ends, a moment after the job's final state is recorded, so a cancel followed by an immediate pull of the same reference can briefly answer `409 conflict`. "Identical" means the same spelling: `postgres:18` and `docker.io/library/postgres:18` are two pulls. Finished pulls stay readable in memory with the other finished jobs and share their retention of 50.
- The registry host in the reference is contacted by the VM's Docker daemon as given. Loopback, link-local and private hosts are not refused: the token already implies full Docker control, and local registries such as `localhost:5000` are legitimate. The cleaned registry error text in the job output can therefore tell the token holder whether a host is reachable from the VM.
- When a job is cancelled, its last output line is `Pull cancelled.`

How the Docker call was verified. The backend sends `POST /v1.41/images/create` with the query values `fromImage` and, for a tag reference, `tag`. The published Docker Engine API v1.41 description (`https://docs.docker.com/reference/api/engine/version/v1.41.yaml`, checked while writing this) says for `fromImage`: "Name of the image to pull. The name may include a tag or digest. [...] The pull is cancelled if the HTTP connection is closed." and for `tag`: "Tag or digest. If empty when pulling an image, this causes all tags for the given image to be pulled." So a tag reference is sent as `fromImage=<repository, with registry host and port>` plus `tag=<tag>` (never empty), and a digest reference is sent whole in `fromImage` (`name@sha256:...`) with no `tag`. No other query value is ever sent: not `fromSrc`, `repo`, `changes` or `message` (import), and not `platform`. The `X-Registry-Auth` header is never sent. The route was checked against the published description only; no test pulls a real image, every test uses a fake socket or a fake Docker client.

The stream is newline-delimited JSON objects (`status`, `id`, `progressDetail`, `error`, `errorDetail`). Docker reports a pull that failed after it started as an `error` object inside a `200` answer, and the backend turns that into a failed job. A line that is not the expected JSON, or is longer than 64 KiB, is skipped and never echoed.

Errors (before the job exists): the shared ones of 1.3, plus:

- `422` `invalid_request` with `details.fields.reference`: not a valid reference, or no tag or digest.
- `400` `bad_request`: the body rules above.
- `409` `conflict`: the profile already runs 2 pulls (`details.limit` is `2`), or the same reference is already being pulled on it (`details.name` is the reference).
- `503` `shutting_down`: the backend is stopping and accepts no new jobs.

### 3.31 `GET /api/profiles/{name}/ssh-config`

Token: yes. Request body: none. Shows the SSH settings `colima ssh-config -p <profile>` prints, for IDE remote access or `scp`. It is a read: no busy check, no action log row, and the UI fetches it once when its dialog opens and never polls.

Response `200`:

```json
{
  "raw": "Host colima-synthetic\n  HostName 127.0.0.1\n  Port 60022\n  User synthetic-user\n  IdentityFile \"/home/user/.colima/_lima/_config/user\"\n",
  "entries": [
    { "key": "Host", "value": "colima-synthetic" },
    { "key": "HostName", "value": "127.0.0.1" },
    { "key": "Port", "value": "60022" },
    { "key": "User", "value": "synthetic-user" },
    { "key": "IdentityFile", "value": "/home/user/.colima/_lima/_config/user" }
  ]
}
```

1. `raw` is Colima's text unchanged. It contains the path of the identity file, because showing it is the purpose of the route. It holds no key material, only the path.
2. `entries` lists every key and value line in the order Colima printed it, `Host` included. Comments and blank lines are skipped, `Key=value` and `Key value` forms are both read, and one pair of double quotes around a value is removed. A key may repeat (for example two `IdentityFile` lines). Clients must tolerate keys they do not know.
3. The profile name is checked first: an unknown or malformed name is `404` and never reaches a process.
4. A profile that is not running is not rejected up front. Colima answers whatever it answers; if it refuses, the route returns `409` `invalid_state` with `details.status`.

Errors:

1. `404` `profile_not_found`.
2. `409` `invalid_state`: Colima refused to print a config and the profile is not running (`details.status`).
3. `502` `colima_failed`: Colima could not be run, failed on a running profile, or printed text that is not an SSH config. The output is logged on the server only.

### 3.32 `POST /api/profiles/{name}/delete`

Token: yes. Deletes the profile's VM, only when it holds no Docker containers, images or volumes (see the errors below), with `colima delete -p <name> --force` as a job (action `delete`), plus `--data` when asked. `--force` is always passed so the process never waits on a prompt the UI cannot answer; the confirmation lives in this request instead. **This cannot be undone.** The call answers `202` once the job exists: unlike start and stop, it waits for the in-slot status read and Docker count described below (normally well under a second, at most 35 seconds) before the job is created. Follow the job like a start (3.4, 3.5, 3.6, section 5). The job holds the profile's slot, can be cancelled, writes an action log row (`delete`) and its output streams like a stop. When it succeeds the profile leaves the list and the sampler stops for it; its recorded history stays in the database.

Request body (at most 1 KiB, strict: exact keys, each at most once):

```json
{ "confirm": "dev", "deleteData": false }
```

1. `confirm` must equal the profile name exactly (case and spacing count). An empty body, `{}`, a missing or `null` key and any other string answer `422 confirmation_required`; no process starts.
2. `deleteData` is optional (default `false`). When `true` the container runtime data (the Docker or containerd disk) is deleted too (`--data`).
3. Any other key, a repeated key, a wrong type, trailing data and a non-object body are `400 bad_request`.

Response `202`:

```json
{ "jobId": "j_8f3a2c" }
```

Errors:

1. `404` `profile_not_found`: the name is malformed, or the profile is neither listed by Colima nor present in Colima's folder as a real directory (a file or a symlink with the name does not count; names compare ignoring letter case). Unlike the other routes, `default` gets no special case here: it can be deleted only when it exists.
2. `422` `confirmation_required`: `confirm` is missing or does not equal the name.
3. `409` `profile_busy`: another job runs on the profile (`details.jobId`).
4. `409` `invalid_state`: the state does not allow a delete. `details.status` is the current status (as in 3.4) and, except for a plain refusal, `details.reason` says why:
   1. `docker_not_empty`: the VM is `running` with the Docker runtime and holds at least one container (of any state), image or volume. `details` also carries `containers`, `images` and `volumes` (counts only, never names or ids). Remove the Docker objects first.
   2. `cannot_verify`: the VM is `stopped`. Its Docker engine cannot be read, so it cannot be shown empty. Start it, empty it, then delete it.
   3. No `reason`: any other status (`starting`, `stopping`, an unknown one) is refused; only `running` (when empty), `broken` and `running` on another runtime are allowed. A `broken` VM is allowed so a half-created or damaged profile can be cleaned up; its Docker contents are not read.
   A `running` profile whose runtime is not Docker is allowed: there is no engine to count. If the VM stops between the status read and the Docker read, the answer is `409` with `details.status` `stopped`.
5. `502` `docker_unavailable`: the profile is `running` but its Docker contents could not be read (missing socket, daemon error, timeout, or a failure to resolve the socket). The delete fails closed: nothing is deleted, and no daemon text or path is returned.
6. `400` `bad_request`, `502` `colima_failed`, `503` `shutting_down`, `500` `internal_error` as in 3.4.

Order of checks: body shape, busy, profile lookup, confirmation, state (and for a running profile the Docker count), then the job is submitted. The busy check runs before the slow reads (`colima list`, then up to four Docker list calls) so a job that began meanwhile is seen. The job re-reads the status inside the profile slot, right before running `colima delete`, and for a running profile counts the Docker objects again. If the state no longer allows the delete (for example the VM was started or stopped from a terminal in between), the count is not zero, or the count cannot be made (it fails closed, like the route), nothing is deleted and the job ends as `failed` with one closing line in its output. Its action log row says `not run: docker not empty`, `not run: could not check docker contents`, `not run: profile is stopped, docker contents cannot be checked` or `not run: profile state did not allow deleting`, never a Colima exit status.

Residual race: the count and `colima delete` are not atomic. A container, image or volume created (for example from a terminal) after the in-slot count and before the VM is removed is deleted with the VM. Like the busy check in 1.3, the guard is best effort against anything outside this backend's own jobs; it cannot be closed by a one-shot check.

A running profile can now be deleted: `colima delete -p <name> --force` stops and removes the VM. The job output, the cancel behaviour and the action log row are the same as for any delete. The profile leaves the list when the job finishes (the sampler is told to refresh and drops it), and the event stream carries the usual `job.finished` event.

Things to know:

1. While the delete is waiting for that status read, `GET /api/profiles` and a `409 profile_busy` answer can already show the job id, but `GET /api/jobs/{id}` answers `404 job_not_found` for it until the job exists (up to 35 seconds). Clients should retry a `404` for an id they just received.
2. The action log row (`delete`, 3.11) carries `with runtime data` as its detail when `deleteData` was `true`. Once the profile is gone, `GET /api/profiles/{name}/actions` answers `404` for it, so the row cannot be read through the API any more. History is keyed by profile name only, so a profile created later with the same name shows the older rows too.
3. The UI asks `GET /api/profiles/{name}/contents` (3.37) when its delete dialog opens, to show what blocks a delete. That answer is advisory; this route decides.
4. A cancelled, interrupted (backend shutdown) or failed delete can leave a half-deleted profile that Colima lists as `broken`. Delete it again; that is why `broken` is allowed.


### 3.33 `GET /api/profiles/{name}/kubernetes`

Token: yes. Request body: none. Tells the UI whether to show the Kubernetes actions. The answer is the profile's configured `kubernetes.enabled`, read through the config service. It is not whether the cluster is running: `colima status` reports `kubernetes: false` for a stopped cluster, which would make Start unreachable, so the status output is never used for this. A profile without a configuration file or without the key is not enabled. Because the flag is the configured one, hand-editing the YAML while the VM runs can make it drift from the real cluster: `true` while the VM booted without Kubernetes means Start may run against a VM that has none, and `false` while a cluster is running means the UI cannot stop or delete that cluster. It is a read: no busy check, no action log row.

Response `200`:

```json
{ "enabled": true }
```

Errors:

1. `404` `profile_not_found`.
2. `409` `invalid_state`: the profile is not running (`details.status` is exactly what the profile list reports). The configuration is not read.
3. `500` `internal_error`: the configuration could not be read.
4. `502` `colima_failed`: Colima could not be queried for the profile's state.

### 3.34 `POST /api/profiles/{name}/kubernetes/{start|stop|reset|delete}`

Token: yes. Each route runs one fixed command, `colima kubernetes <verb> -p <profile>`, as a job with `action` `kubernetes-start`, `kubernetes-stop`, `kubernetes-reset` or `kubernetes-delete`. The job holds the profile slot, streams output, can be cancelled with `POST /api/jobs/{id}/cancel` and writes an action log row whose `detail` names the verb. The call answers `202` once the job exists: like a delete (3.32) it waits for the in-slot checks below.

Request body:

1. `start` and `stop`: none (an empty body or `{}`); anything else is `400`.
2. `reset` needs the flat body `{ "confirm": "reset-kubernetes" }` and `delete` needs `{ "confirm": "delete-kubernetes" }`. The key is exactly `confirm` (case-sensitive, at most once) and the text is compared exactly. The body is at most 1 KiB. An empty body, `{}`, a missing key, `{"confirm": null}` and a different text are `422 invalid_request` with `details.fields.confirm`. Any other key, a repeated key, a wrong type, trailing data and a non-object body (a bare `null` included) are `400 bad_request`. These checks run before anything else, so a refused body never starts a process.

```json
{ "confirm": "reset-kubernetes" }
```

Response `202`:

```json
{ "jobId": "j_8f3a2c" }
```

Rules, in the order they are checked:

1. The profile name is validated first; an unknown or malformed name is `404` and never reaches a process.
2. A job already running on the profile is `409 profile_busy` (`details.jobId`), checked before the state reads.
3. The profile must be exactly `running`, otherwise `409 invalid_state` with `details.status`. A stopped, broken or unknown profile is refused.
4. Kubernetes must be enabled in the profile's configuration (3.33), otherwise `409 invalid_state` with `details.reason` `kubernetes_not_enabled` and `details.status` `running`. The cluster itself may be stopped: Start exists for that case. These routes never enable Kubernetes.
5. The job repeats both checks once it holds the profile slot. If either now fails, colima is not run and the job ends as `failed` with one closing line; its action log row says `not run: ...`. The checks fail closed: a status or configuration read that fails, or a backend wired without them, means colima is not run.

Things to know:

1. While the in-slot checks run (at most 15 seconds), `GET /api/profiles` and a `409 profile_busy` answer can already show the job id, but `GET /api/jobs/{id}` answers `404 job_not_found` for it until the job exists. Clients should retry a `404` for an id they just received; the web app does.
2. `reset` and `delete` destroy the cluster's workloads and state. A reset or delete that is cancelled, interrupted by a backend shutdown or fails after colima started may leave the cluster half done; the action log row says so, and the way forward is to run the action again.
3. A configuration read that fails inside the slot surfaces as `502 colima_failed` with no job and no action log row (the same holds for a failed status read).

Errors:

1. `404` `profile_not_found`.
2. `400` `bad_request`: the body is not valid for the route.
3. `422` `invalid_request` with `details.fields.confirm`: the confirmation text is missing or wrong (`reset` and `delete`).
4. `409` `profile_busy`, or `409` `invalid_state` (`details.status`, plus `details.reason` `kubernetes_not_enabled` when the cause is Kubernetes being off).
5. `500` `internal_error`: the configuration could not be read.
6. `502` `colima_failed`: Colima could not be queried or launched.
7. `503` `shutting_down`: the backend is stopping and accepts no new jobs.

### 3.35 `POST /api/colima/cache/prune`

Token: yes. Deletes Colima's cached downloaded assets (VM images) with `colima prune --force` as a job (action `cache-prune`), plus `--all` when `includeLima` is true, which also deletes Lima's cached assets. The cache is shared by every profile, so the route has no profile in its path and the job's `profile` is the empty string. `--force` is always passed so the process never waits on a prompt the UI cannot answer; the confirmation lives in this request instead. The only effect is that the next create or start may download its assets again. The vector is fixed: no other argument or flag can be sent.

Request body (at most 1 KiB, strict: exact keys, each at most once, case sensitive):

```json
{ "confirm": "delete-cached-assets", "includeLima": false }
```

1. `confirm` must equal `delete-cached-assets` exactly (case and spacing count). An empty body, `{}`, a missing or `null` key, an empty string and any other string answer `422 confirmation_required`; no process starts.
2. `includeLima` is optional (default `false`; `null` counts as absent). `true` adds `--all`.
3. Any other key (including a key with different letter case), a repeated key, a wrong type, trailing data, a body over 1 KiB and a non-object body are `400 bad_request`.

Response `202`:

```json
{
  "jobId": "j_8f3a2c",
  "action": "cache-prune",
  "profile": "",
  "state": "running"
}
```

`GET /api/jobs/{id}` (3.5) then returns the job, `POST /api/jobs/{id}/cancel` (3.6) cancels it, and `job.output` and `job.finished` (5.3) carry its output and its end, all with `action` `cache-prune` and `profile` `""`. A client that filters the event stream by profile does not see these events; one without a filter does.

The job:

1. It is a task job like an image pull (3.30): it holds no profile slot, so it never blocks stop, delete or update and is not blocked by them, and it is allowed while profiles run (item 3 lists what it does refuse and what refuses it). Its output is Colima's own output, streamed as it comes, followed on failure or cancel by one fixed closing line of the backend's own.
2. At most one cache prune runs at a time across the whole backend. A second request answers `409 conflict` until the first has ended and its final state is recorded, a moment after it finishes.
3. It is refused while a **create, start, restart or kubernetes-start** job runs on any profile, because those download the very assets a prune removes (`update` refreshes the runtime inside the VM, not the host cache, so it is not refused). The other way round, a prune that runs refuses those four actions with `409 invalid_state` (`details.reason` `cache_prune_running`, `details.status` `busy`, `details.jobId` the prune job) from the routes that start them (3.4, 3.17, 3.34); stop, delete, update and the other Kubernetes verbs are allowed. Both checks run under the manager's own lock, in the same step that reserves the profile slot or the single prune place, so neither side can slip past the other. A start from a terminal is not seen. `details.jobId` of a refusal is read from the profile slot, which exists a moment before the job is readable, so `GET /api/jobs/{id}` can answer `404` for it briefly. Colima removes the cache folder regardless of what is using it, which is why the two sides refuse each other instead of relying on files being closed.
4. Cancelling kills the Colima process and the job ends `cancelled` once the process is gone. **A cancelled or interrupted prune can leave a partial cache**: some files may already be removed. That is harmless, the next create or start downloads what is missing, and the last output line says so (`Cache prune cancelled. Some cached files may already have been removed.`). When the backend shuts down a running prune is cancelled and awaited like other jobs.
5. Action log row `cache-prune` with the outcome `success`, `failed` or `cancelled`, written when the job ends. The detail is the vector that ran (`colima prune --force` or `colima prune --force --all`), so `--all` is always visible. When Colima could not be started the detail starts with `not run:` and never claims an exit status; a cancelled prune says `some cached files may already be removed`. The row belongs to no profile, so it is stored under the reserved key `_global` (a name no profile can have). No route reads it yet: `GET /api/profiles/{name}/actions` (3.11) only serves real profiles.
6. A failed job has the exit code `1`: the backend does not pass Colima's status through for tasks. Colima's output can contain absolute paths of the cache folder, like the output of every other job. They are the user's own paths, and the UI and API are loopback only and need the token.

Errors:

1. `422` `confirmation_required`: `confirm` is missing or not the exact text.
2. `400` `bad_request`: the body rules above.
3. `409` `invalid_state` with `details.reason` `assets_in_use`, `details.status` `starting` and `details.jobId`: a create, start, restart or kubernetes-start job is running on some profile. Nothing starts.
4. `409` `conflict`: a cache prune is already running (`details.jobId` is that job, so a client can follow it after a reload).
5. `503` `shutting_down`: the backend is stopping and accepts no new jobs.
6. `500` `internal_error`: unexpected failure.

Order of checks: body shape (400), confirmation (422), then the job is submitted and the create/start check and the single-prune check happen together (409). Colima is not run before every check has passed.

### 3.36 `POST /api/profiles/{name}/containers/{id}/{start|stop|restart}`

Token: yes. Starts, stops or restarts one container. It follows the Docker write rules in 1.3 (profile, busy check, body, validation, one fixed Docker call, action log row) with these specifics.

1. **No body.** The request carries no data. An empty body is the only accepted one: a body of any kind, `{}` included, is `400 bad_request`, and the retry is the same request.
2. **The id.** `{id}` is the full container id: exactly 64 lowercase hex characters, the `fullId` field of 3.10. Anything else is `422 invalid_request` with `details.fields.id` before Docker is contacted: a container name, a 12 character short id (the `id` field), any other prefix, uppercase, and any character that is not `0-9a-f`. The request text is never echoed. Only a full id is accepted because Docker resolves a full id first, so a container whose name looks like an id cannot take the place of another one, and no request can be ambiguous.
3. **The call.** `POST /containers/{id}/start`, `/stop` or `/restart` on the pinned API v1.41, with no query: stop and restart send no `t` parameter, so Docker applies the container's own stop timeout, 10 seconds unless the container sets another. The route has a 60 second budget. The client cannot change the grace period.
4. **The answer.** Docker's `204` means it did the work and Docker's `304` means the container was already in that state (start on a running container, stop on a stopped one). Restart has no already-state, so a successful restart is always `changed: true`.

Response `200`:

```json
{ "changed": true }
```

`changed` is `false` for the `304` case: the request succeeded and nothing changed.

Errors, on top of the shared ones of 1.3:

| Status | `code` | When |
| --- | --- | --- |
| `404` | `not_found` | Docker does not know the id (`details.name` is the validated id) |
| `409` | `conflict` | Docker refused to act on the container as it is: a paused container, one marked for removal, or a start whose resource limits no longer fit the host (Docker's `409` and `400`). The message is fixed text, `Docker refused to change this container in its current state.`; Docker's own text is dropped (`details.name` is the validated id) |
| `409` | `invalid_state` | The VM is not running (`details.status`), or the profile does not use Docker (`details.runtime`) |
| `409` | `profile_busy` | A start, stop or restart job is running on the profile (advisory, see 1.3) |
| `422` | `invalid_request` | The id is not a container id (`details.fields.id`) |
| `502` | `docker_unavailable` | The socket is missing or not answering, Docker answered with an error, or it did not answer in time |

Docker's error text is never returned. One action log row is written per request that reaches Docker, with the action `docker.container.start`, `docker.container.stop` or `docker.container.restart`, and as `detail` the first 12 characters of the id followed by `changed=true`, `changed=false` or `changed=unknown`. The `304` case is a `success` row with `changed=false`. A `failed` row says `changed=false` when Docker refused (`404`, `409`, or the VM not running) and `changed=unknown` when the outcome is not known (the socket failed, Docker answered `5xx`, or the call timed out), because the container may have changed. A refused request (steps 1 to 4 of 1.3, a body, an invalid id) writes no row.

### 3.37 `GET /api/profiles/{name}/contents`

Token: yes. Request body: none. Counts the Docker objects inside a running profile, so the delete dialog (3.32) can say what blocks a delete. It uses the Docker list reads behind 3.10, 3.12 and 3.13: containers of every state (stopped ones count), images and volumes. It is a read: no busy check and no action log row. It makes four Docker list calls, so clients must ask once when the dialog opens and must not poll it. It returns counts only, never a name or an id.

Response `200`, a running profile with the Docker runtime:

```json
{ "applicable": true, "containers": 3, "images": 5, "volumes": 1 }
```

Response `200`, a running profile whose runtime is not Docker (there is no engine to count; a delete is allowed):

```json
{ "applicable": false }
```

Errors:

1. `404` `profile_not_found`: the name is unknown or malformed (it never reaches a process or Docker).
2. `409` `invalid_state`: the profile is not `running` (`details.status`, as in 3.10).
3. `502` `docker_unavailable`: the Docker socket is missing or not answering, or any of the three reads failed. Nothing partial is returned.
4. `502` `colima_failed` and `500` `internal_error` as in 3.4.

The numbers are a snapshot: the three reads are separate calls, and a delete (3.32) counts again on its own, so this answer never authorizes a delete.

## 4. Routes that do not exist

By design the API has no route for arbitrary commands (the Kubernetes routes of 3.34 run four fixed verbs, and none of them enables or configures Kubernetes), container control other than start, stop and restart (3.36), profile renaming, YAML editing, or VM and control logs. Profiles can be created (3.17) with the basic and advanced settings, host folder mounts and environment variables, and nothing else. These `colima start` options are deliberately not supported: `--disk-image` and `--force-disk-image` (a custom disk image is an arbitrary file), `--k3s-arg` (raw k3s arguments), `--edit`, `--editor` and `--foreground` (interactive editor and a process that never returns), `--template`, `--save-config` (Colima's default, saving the settings, is left as it is) and `--downloader`. Mounts and environment variables of an existing profile cannot be edited here either; they cannot be renamed here (Colima has no rename); a whole profile can be deleted (3.32), and Colima's download cache can be cleared (3.35), but no other Colima maintenance command (`template`, `update` of the Colima binary itself) is reachable. Container output is readable, but only as a bounded snapshot (see 3.16), never as a stream. Volumes can be created, inspected, removed and pruned (3.18 to 3.21), but volume drivers, driver options, copying data in or out, and the volume's host path are not available. Networks can be created (bridge driver only), inspected, removed and pruned (3.22 to 3.25), but other drivers, driver and IPAM options beyond one subnet, and connecting or disconnecting containers are not available. Images can be tagged, inspected, removed, pruned (3.26 to 3.29) and pulled by tag or digest from public registries (3.30), but push, save, load, import, build, registry login and credentials (private registries), pulling every tag of a repository, and choosing a platform are not available. A separate "current usage" route is also not defined, see 3.9.

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
| Create profile dialog | `POST /api/profiles` (3.17), `GET /api/jobs/{id}`, `GET /api/events` (`job.output`, `job.finished`) |
| Configuration editor | `GET` and `PUT /api/profiles/{name}/config`, restart (3.4) |
| Operation log panel | `GET /api/jobs/{id}`, `POST /api/jobs/{id}/cancel`, `GET /api/events` (`job.output`, `job.finished`) |

## 7. Open points

- Finished job output after a backend restart is not kept. Section 3.5 describes the in-memory behaviour.
- Routes 3.10 and 3.11 are defined here and implemented by issues #38 and #39. No earlier issue built them.
