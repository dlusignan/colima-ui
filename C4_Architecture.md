# Colima UI: C4 Architecture Documentation

Status: Draft 0.3, 23 November 2026 (adds decisions on Colima versions, runtimes, metric history, profiles, platform and finished job output)
Scope: Levels 1 to 3 of the C4 model (Context, Containers, Components). Level 4 (code) is intentionally left out until the first implementation exists.

## 1. Purpose

Colima UI is a local web interface on top of Colima. Colima is a command line tool that runs a Lima virtual machine and a container runtime inside it. The UI has two goals. The first is to make the VM layer easier to operate: profiles, start, stop, restart, and configuration. The second is to make capacity and limits visible, so the user can see what the VM is allocated, what it actually uses, and which containers are responsible.

The tool runs only on the developer's own machine. It is not a hosted service and it has no multi user concerns.

## 2. Level 1: System Context

The developer opens the UI in a browser. The UI does not manage containers or VMs by itself. It delegates to Colima for the VM lifecycle and to the Docker Engine API for container and usage details.

```mermaid
C4Context
    title Colima UI, System Context

    Person(dev, "Developer", "Runs containers locally and wants to manage and understand the Colima VM")

    System(ui, "Colima UI", "Local web interface for managing Colima profiles and visualizing VM capacity")

    System_Ext(colima, "Colima CLI", "Starts, stops and configures the Lima VM and its container runtime")
    System_Ext(docker, "Docker Engine API", "Container, image and resource usage data, reached through the socket Colima exposes")
    System_Ext(files, "Colima config files", "One YAML file per profile in the user's Colima folder")

    Rel(dev, ui, "Uses", "Browser, localhost")
    Rel(ui, colima, "Runs whitelisted subcommands", "Process execution")
    Rel(ui, docker, "Reads containers and stats", "Unix socket")
    Rel(ui, files, "Reads and edits", "Filesystem")
```

## 3. Level 2: Containers

In C4, a container is a separately running piece of the system, not a Docker container. Colima UI has three: the React single page application, the Go backend, and a SQLite database file. The backend serves the built frontend from embedded assets, so the whole product ships as one binary.

```mermaid
C4Container
    title Colima UI, Containers

    Person(dev, "Developer", "Uses the UI in a browser")

    System_Boundary(sys, "Colima UI") {
        Container(spa, "Web Application", "React, Tailwind CSS, shadcn/ui", "Dashboard, profile detail, capacity view, configuration editor, terminal style operation log, command palette")
        Container(api, "Backend", "Go", "REST API, event stream, process supervision, metrics sampling. Serves the embedded frontend")
        ContainerDb(db, "History Database", "SQLite", "Metric samples and action log. Never holds current state")
    }

    System_Ext(colima, "Colima CLI", "Lifecycle and configuration")
    System_Ext(docker, "Docker Engine API", "Containers and stats")
    System_Ext(files, "Colima config files", "Profile YAML")

    Rel(dev, spa, "Uses", "HTTP, 127.0.0.1")
    Rel(spa, api, "Calls", "REST and Server Sent Events")
    Rel(api, db, "Reads and writes history", "SQL, embedded")
    Rel(api, colima, "Executes", "os/exec, whitelisted")
    Rel(api, docker, "Queries", "Docker Engine API, Unix socket")
    Rel(api, files, "Reads and writes", "Filesystem")
```

### Container responsibilities

| Container | Technology | Responsibility |
| --- | --- | --- |
| Web Application | React, Tailwind CSS, shadcn/ui (Base UI primitives), built to static files | Presents state, sends user intent to the API, shows streamed progress. Holds no business rules beyond input validation for convenience. |
| Backend | Go, standard library HTTP, a small hand-rolled Docker Engine API client | Owns every interaction with the machine. Validates requests, runs Colima commands, streams output, samples metrics, enforces security rules. |
| History Database | SQLite through a pure Go driver (modernc.org/sqlite), no cgo | Stores time series samples and an action log so history survives restarts. |

### Source of truth

Current state is always read live from Colima, the config files and the Docker API. The database is never used to answer "what is the state right now". This avoids drift when the user runs Colima from a terminal while the UI is open.

## 4. Level 3: Components of the Backend

```mermaid
C4Component
    title Colima UI, Backend Components

    Container(spa, "Web Application", "React", "Browser client")
    ContainerDb(db, "History Database", "SQLite", "Samples and action log")
    System_Ext(colima, "Colima CLI", "")
    System_Ext(docker, "Docker Engine API", "")
    System_Ext(files, "Colima config files", "")

    Container_Boundary(api, "Backend") {
        Component(router, "HTTP Layer", "net/http", "Routing, token authentication, origin and host checks, static asset serving")
        Component(profiles, "Profile Service", "Go", "Lists profiles and reports status, merges CLI and config data")
        Component(config, "Config Service", "Go, YAML", "Reads, validates and writes profile configuration, flags changes that need a restart")
        Component(adapter, "Colima Adapter", "Go, os/exec", "Only place that runs the colima binary. Whitelist of subcommands and arguments, parses JSON output, isolates version differences")
        Component(ops, "Operation Manager", "Go, goroutines", "Tracks long running jobs such as start and stop, supports cancellation, captures output")
        Component(events, "Event Stream", "Server Sent Events", "Pushes operation output and live metrics to connected browsers")
        Component(dockerc, "Docker Client", "Docker Engine API, hand-rolled client over the Colima Unix socket", "Containers, per container stats, disk usage, container log snapshots")
        Component(sampler, "Metrics Sampler", "Go, ticker", "Collects VM and container usage at a fixed interval")
        Component(store, "History Store", "Ring buffer and SQLite", "Short window in memory, long history on disk, retention cleanup")
    }

    Rel(spa, router, "Requests", "REST")
    Rel(spa, events, "Subscribes", "SSE")
    Rel(router, profiles, "Uses")
    Rel(router, config, "Uses")
    Rel(router, ops, "Starts and cancels jobs")
    Rel(profiles, adapter, "Queries status")
    Rel(config, files, "Reads and writes")
    Rel(ops, adapter, "Runs commands")
    Rel(ops, events, "Publishes output")
    Rel(sampler, dockerc, "Reads stats")
    Rel(sampler, adapter, "Reads VM details")
    Rel(sampler, store, "Writes samples")
    Rel(sampler, events, "Publishes live values")
    Rel(store, db, "Persists")
    Rel(dockerc, docker, "Calls")
    Rel(adapter, colima, "Executes")
```

### Component notes

The HTTP Layer is thin. It decodes requests, checks authentication, and hands off. It contains no Colima knowledge.

The Colima Adapter is the single choke point for process execution. Every subcommand is an explicit Go function with typed arguments, never a string assembled from user input. When Colima changes its output between versions, this is the only component that should need an update.

The Config Service treats the YAML files as the authority. It validates changes, shows which ones require a VM restart, and refuses to write while an operation is running on the same profile.

The Operation Manager serializes work per profile, so two starts or a start and a stop can never run at once on the same VM. Each job has an identifier, a status, captured output, and a context that cancels the child process.

The Metrics Sampler feeds the capacity view. It records what the VM is using and what each container is using. Allocated figures are not stored: the capacity view takes the limits from the profile and plots the recorded usage against them, which is what lets the UI show the gap between limit and reality.

### Frontend structure (proposed)

The React application is organized around five views: a dashboard of all profiles, a profile detail page, a capacity view with charts, a configuration editor, and an operation log panel that follows a running job. A command palette, opened with Cmd+K, gives keyboard access to every action. Server data is fetched through a query cache library, and live data arrives through one Server Sent Events connection.

### Frontend visual design

The interface targets developers, so the look is a dense, dark, terminal inspired style, built on Tailwind CSS and shadcn/ui. shadcn/ui components are copied into the repository instead of installed as a dependency, which keeps full control over every detail and avoids fighting a heavy component library. The Base UI primitives underneath (the shadcn/ui `base-nova` style) provide accessibility for dialogs, menus, and focus handling.

Theming is driven entirely by CSS variables, so a theme is one file. Dark is the default. The planned palettes are Catppuccin Mocha, Tokyo Night, Gruvbox, Nord, and Dracula, selectable by the user. A light theme is built from the start so that contrast and accessibility problems do not hide in a dark only design.

Typography pairs a monospace font for numbers, profile names, and any data (JetBrains Mono, Geist Mono, or IBM Plex Mono, all free) with a clean sans serif for labels and prose.

Layout favors compact tables over padded cards, status dots for state, and usage bars that resemble terminal meters. Interaction is keyboard first: shortcuts for start, stop, and restart, plus the command palette (the cmdk library).

Two specialized libraries cover the rich parts. The operation log uses xterm.js, so streamed Colima output renders exactly like a real terminal, colors included. The capacity charts use uPlot for fast live time series, with thin lines, no gradients, and subtle grid lines. Recharts is the fallback if uPlot proves too manual to style.

Retro component libraries such as NES.css or 98.css are deliberately avoided. The personality comes from theme and typography, while the layout stays conventional enough to be pleasant in daily use.

## 5. Key Flows

Starting a profile. The user clicks start. The browser posts to the API. The HTTP Layer authenticates and passes the request to the Operation Manager, which checks that no other job is running on that profile and asks the Colima Adapter to run the start command. Output lines are published through the Event Stream, and the browser renders them as they arrive. When the process exits, the job closes with a final status, the action is written to the log, and the dashboard refreshes its state from Colima.

Viewing capacity. The Metrics Sampler wakes on its interval, reads VM and container figures, writes them to the History Store, and publishes the latest values. The capacity view loads recent history over REST, then keeps itself current from the event stream.

Creating a profile. The user submits a name and basic settings. The route validates every value, refuses a name that already exists (in Colima's list or as a folder, in any letter case) and asks the Operation Manager for a `create` job, which runs `colima start` on the new name. From there it behaves like a start.

Editing configuration. The user changes CPU, memory or disk. The Config Service validates the values, writes the YAML, and returns a list of changes that need a restart. The UI shows that cost and offers a restart, which runs as a normal operation.

## 6. Security Boundaries

This application can control the VM and, through the Docker socket, effectively the machine, so the backend is the security perimeter.

The server binds to 127.0.0.1 only and never to all interfaces. Every API call requires a random token generated at startup. The HTTP Layer checks the Host and Origin headers to defend against DNS rebinding and cross site requests. There is no endpoint that accepts an arbitrary command, only the fixed set of operations the Colima Adapter exposes. Configuration values are validated against allowed ranges before they touch disk or a command line.

## 7. Data Model (History Database)

```sql
CREATE TABLE metric_sample (
    id            INTEGER PRIMARY KEY,
    profile       TEXT    NOT NULL,
    taken_at      INTEGER NOT NULL,   -- unix seconds
    scope         TEXT    NOT NULL,   -- 'vm' or a container id
    cpu_percent   REAL,
    memory_bytes  INTEGER,
    disk_bytes    INTEGER
);
CREATE INDEX metric_sample_lookup ON metric_sample (profile, scope, taken_at);

-- Rollup of raw samples older than 24 hours, one row per minute.
CREATE TABLE metric_sample_1m (
    profile       TEXT    NOT NULL,
    scope         TEXT    NOT NULL,
    bucket_start  INTEGER NOT NULL,   -- unix seconds, multiple of 60
    cpu_percent   REAL,               -- average over the bucket
    memory_bytes  INTEGER,
    disk_bytes    INTEGER,
    samples       INTEGER NOT NULL,   -- raw samples folded into the bucket
    cpu_samples   INTEGER NOT NULL,   -- how many of them had a cpu value
    mem_samples   INTEGER NOT NULL,   -- ... a memory value
    disk_samples  INTEGER NOT NULL,   -- ... a disk value
    PRIMARY KEY (profile, scope, bucket_start)
) WITHOUT ROWID;

CREATE TABLE action_log (
    id          INTEGER PRIMARY KEY,
    profile     TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    action      TEXT    NOT NULL,     -- 'start', 'stop', 'config_change', ...
    outcome     TEXT    NOT NULL,     -- 'success', 'failed', 'cancelled'
    detail      TEXT
);
CREATE INDEX action_log_recent ON action_log (profile, started_at DESC, id DESC);
```

The schema is created by embedded SQL migrations (`internal/store/migrations`). The version lives in `PRAGMA user_version`, and each migration runs in its own `BEGIN IMMEDIATE` transaction together with its version bump. Opening a database already at the current version writes nothing, and a database written by a newer program is refused (there are no down migrations; the file holds history only and can be deleted). The file is `<UserConfigDir>/colima-ui/history.db` (override with `COLIMA_UI_DATA_DIR`), the folder is mode 0700 and the file is created 0600. The database uses WAL, `busy_timeout` 5000, `synchronous` NORMAL, foreign keys on and incremental auto vacuum, with one writer connection and a pool of 4 readers.

Retention follows the decision in section 9 and is applied by a periodic cleanup (first run shortly after start, then hourly):

1. Raw samples older than now minus 24 hours (rounded down to a whole minute) are folded into `metric_sample_1m` with `ON CONFLICT DO NOTHING` and deleted, one hour of raw time per transaction with a pause between slices, so a rerun is a no-op and the writer is never held for long. A late raw sample for an already folded minute is merged into that bucket (weighted by the per-field counts) instead of being dropped.
2. `metric_sample_1m` rows older than 30 days are deleted in chunks of 5,000.
3. `action_log` keeps the newest 1,000 rows per profile. The decision does not cover the action log, so this cap is an addition that stops the table growing without bound. The `action_log_recent` index serves the "recent actions" query.
4. Free pages are returned with an incremental vacuum and the WAL is checkpointed.

Recent history is also held in memory: one ring of 17,280 slots (24 hours at 5 seconds) per (profile, scope), allocated lazily, dropped after 10 idle minutes and capped at 256 scopes. It is write-through and rehydrated from `metric_sample` when the store opens, so a query that starts within the last 24 hours is answered without touching the disk. Older ranges read the 1 minute table plus any unfolded raw rows from one snapshot and are averaged down to at most `maxPoints` (2,000 at most) by one shared function. The 1 minute tier stores averages, so short spikes older than 24 hours are not visible in charts.

## 8. Deployment

One Go binary containing the embedded React build. It runs as a normal process on the developer's machine, either started by hand or by a login item. It needs read access to the Colima config folder and access to the Docker socket. Packaging as a menu bar or desktop app (for example with Tauri) is possible later without changing the architecture.

## 9. Decisions

| Decision | Choice | Reason |
| --- | --- | --- |
| Backend language | Go | Same language as Colima, Lima and Docker. Strong process supervision and a single binary. |
| Frontend | React | Team preference and a large ecosystem for charts and data views. |
| Storage | SQLite, history only | Embedded, no server to run. Current state is never cached, which prevents drift. |
| Container data | Docker Engine API (small hand-rolled client over the Colima Unix socket, pinned to /v1.41), not CLI parsing | Stable, typed, documented. The Docker Go SDK would add about 20 modules and is pre-1.0 on its maintained path. |
| Colima data | CLI JSON output plus config files | Colima has no stable API, so access is isolated in one adapter. |
| Live updates | Server Sent Events | One way streaming is enough, simpler than WebSockets. |
| Styling | Tailwind CSS with shadcn/ui | Components live in the repo, so every detail can be restyled. Accessible Base UI primitives underneath. |
| Visual identity | Dark terminal style, themeable through CSS variables | Appeals to the developer audience. A palette is a single file swap, and a light theme ships too. |
| Operation log | xterm.js | Renders streamed command output, colors included, like a real terminal. |
| Charts | uPlot, Recharts as fallback | uPlot is small and fast for live time series. Recharts is easier to theme if needed. |
| Navigation | Command palette (cmdk) and shortcuts | Keyboard first interaction suits the audience and speeds up frequent actions. |
| Colima versions | Built and tested against Colima 0.10.x. The adapter detects the version with `colima version`, parses tolerantly, and keeps working on other versions | A version outside 0.10.x shows a warning in the UI and an unrecognised output shape returns a clear error. The tool never refuses to start because of the version. |
| Runtimes in the first release | Docker only. Profiles that use containerd, Incus or Kubernetes are listed and managed (start, stop, configuration) but show no container data | The Docker Client uses the Docker Engine API. Other runtimes are a later addition. |
| Metric history | Sample every 5 seconds. Keep raw samples for 24 hours, then 1 minute averages for 30 days, then delete. Not user configurable in the first release | Keeps the SQLite file small while showing recent detail and a month of trend. |
| Profiles | All profiles are listed and managed at launch, and every profile route carries the profile name | The dashboard is a table of profiles by design, so several profiles cost nothing extra. |
| Platform | macOS only in the first release | Socket and configuration folder discovery assume macOS. Linux is a later addition. |
| History database failure | A history database that cannot be opened, including one written by a newer version, stops the backend at startup with a clear message | The file holds history only and can be deleted, so failing fast is simpler than running without history. The same stance as a missing `colima` binary. |
| Observer fan-out | The Operation Manager calls three observers in order for every job: the action log recorder (bounded queue, own writer goroutine), the event stream (non-blocking publish) and a sampler refresh signal | Each observer is non-blocking, so a slow database or browser never stalls a job's output. A finished job also makes a start or stop show up in sampling within seconds. |
| Shutdown order | Event streams end first, then the HTTP server stops, then the sampler, the Operation Manager, the action log recorder and finally the History Store | An open stream would otherwise hold the HTTP shutdown for its whole timeout, and the recorder must drain after the manager so the last finished job is written before the store closes. |
| Finished job output | Kept in memory only. After a backend restart a finished job shows its action log summary and no output | Matches the API contract, which already treats output after a restart as unavailable. |
| Container logs | A read-only snapshot of the last 200 lines by default (at most 1000) per container, fetched on demand. No streaming and no follow mode | Container output can contain secrets, and a live stream needs bounds on followers and retained output. The snapshot keeps the backend stateless and bounded (64 KiB per frame, 4000 characters per line, 512 KiB per response). A follow mode can be added later on top of it. |
| Profile creation | Allowed from the UI, basic settings first (name, CPUs, memory, disk, root disk, runtime, VM type, architecture, mount type, Kubernetes). The route also takes the advanced settings (hostname, DNS servers and hosts, SSH agent, Rosetta, port forwarder, inotify, network address, mode and interface, CPU type); mounts and environment variables are accepted under strict validation (see the next row). Colima has no rename. Creation is a `create` job (a `colima start` on a new name) through the Operation Manager, refused when the name exists in any letter case. A failed creation can leave a half-created profile, which the user removes with the delete route (next row) | Creation is the first thing a new user needs. Reusing the job pipeline gives streamed output and an action log row for free. Colima accepts any start on an unknown name, so the route itself must refuse existing names or a create would silently rewrite a profile's settings. The operation slots are keyed by lowercase name because the default macOS filesystem treats `Dev` and `dev` as one folder. |
| Profile deletion | Allowed from the UI as a `delete` job (`colima delete -p <name> --force`, plus `--data` when the user asks for the runtime data to go too). `--force` is always passed so the process never waits for a prompt the UI cannot answer; the confirmation is the profile name typed in the dialog and sent as `confirm`. Only a `stopped` or `broken` VM can be deleted (an allowlist: a running, starting or unknown state is refused with 409 `invalid_state`), the status is read again inside the profile slot right before the command runs (a refused re-check is a failed job that never ran colima), and the profile must exist as a Colima listing or a real directory in Colima's folder (`default` is not special). History rows stay | `colima delete` is irreversible and a running VM must be stopped by the user first. The in-slot re-check closes the window left by the slow `colima list` read, but stays best effort against `colima` runs in a terminal. A stray file in Colima's folder must not pass for a profile. |
| Kubernetes cluster control | Start, stop, reset and delete of the cluster are four fixed `colima kubernetes <verb>` jobs through the Operation Manager, allowed only on a running profile whose configuration has `kubernetes.enabled` true (read through the Config Service, never by parsing YAML in a route). The cluster's own state is not the gate: `colima status` reports `kubernetes: false` for a stopped cluster, which would make Start unreachable. The UI never enables Kubernetes through them. Reset and delete need an exact confirmation text, and a cancelled or failed one can leave the cluster half done | These are the destructive cluster operations Colima offers. The job repeats the running and enabled checks inside the profile slot and fails closed (a refusal is a failed job that never runs colima, like a refused delete), because the route's reads happen outside the slot. |
| Profile creation: mounts, environment and exclusions | Mounts and environment variables are accepted on creation. A mount is an absolute, clean path with no comma, quote, colon, brace, dollar sign or control character, at most 16, no duplicates, existing and a directory on the host at request time, with symlinks resolved and the resolved path passed to Colima. The root, the home folder and the folders above it are refused when writable (decided by file identity, so firmlinks and case variants are caught) and allowed read-only. Environment variables are `KEY=value` with a `[A-Za-z_][A-Za-z0-9_]*` key, a value up to 4 KiB without NUL, at most 32; a value is refused when it ends in a double quote or combines `=` with a comma, quote or line break, because pflag would parse it differently. Neither is ever logged or repeated in an error. Not supported by design: `--disk-image`, `--force-disk-image`, `--k3s-arg`, `--edit`, `--editor`, `--foreground`, `--template`, `--save-config` and `--downloader` | A mount exposes a host folder to the VM and every container in it, and a disk image is an arbitrary file, so the conservative choice ships first and can be widened later. Colima parses `--mount` as a CSV list, so a comma in a path would add a second mount. The Config Service and the adapter apply the same syntax rules, pinned together by a parity test |

## 10. Open Questions

The first release ships Catppuccin Mocha, Tokyo Night, Gruvbox, Nord, Dracula and a light theme, and the theme system follows the operating system setting by default. Each palette meets the contrast test in `web/src/styles/themes/contrast.test.ts`.
