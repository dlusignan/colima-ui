# colima-ui

A local web interface for Colima. Manage your profiles, see what your VM is allocated versus what it really uses, and stop guessing why your containers are slow.

Colima UI runs on your machine as a single binary. It reads Colima's own state (the `colima` CLI, the profile YAML files and the Docker API), so it stays correct when you also use `colima` in a terminal. It never holds a copy of the current state.

## New features

### Done: available in the binary built from `main`

1. **Dashboard.** One row per Colima profile with its status, runtime, CPUs, memory, disk, Colima version, source and warnings. Start, Stop and Restart buttons act on the real VM; Stop and Restart ask for confirmation first.
2. **Profile page.** Status header with the same actions, a configuration summary, the containers of the VM (name, image, CPU and memory) and the recent action log, with links to the other tabs. Containers show their status text (for example "Exited (0) 3 days ago", with a text marker when a container crashed), their ports and when they were created.
3. **Configuration editor.** Change a profile's CPUs, memory and disk with validation in the browser and again on the server. It tells you when a change needs a restart and offers to restart, and it warns before you leave with unsaved changes.
4. **Capacity view.** What the VM is allocated versus what it really uses, as live charts for CPU, memory and disk over a chosen time range, plus a per-container table. Each chart has a "Show data" table for screen readers.
5. **Operation log.** The output of every start, stop and restart in a terminal view, live while the job runs and available after it finishes.
6. **Command palette and shortcuts.** `Cmd+K` opens a palette to jump to any profile tab, start, stop or restart a profile, or switch theme. `Cmd+/` lists the keyboard shortcuts.
7. **Themes.** Six themes (Mocha, Light, Tokyo Night, Gruvbox, Nord and Dracula) plus System, which follows your operating system.
8. **Live updates.** One event stream carries job output, job completion and metric samples to the browser, so the operation log and the capacity charts update as things happen. The dashboard also refreshes its profile list every few seconds.
9. **History.** A metrics sampler records VM and per-container usage every 5 seconds, and an action log records every finished job and every applied config change. Both live in a local SQLite file.
10. **Docker tab.** A profile tab with an Images section: tags, image ID, size, when each image was created and how many containers use it, with unused images marked. It refreshes every 30 seconds.
11. **Volumes and Networks in the Docker tab.** A Volumes section (name, driver, size, when created and how many containers mount each, with unused volumes marked and a dash where the engine has no figure) and a Networks section (name, driver, scope, whether it is internal and how many containers are attached, stopped ones included). Each section loads, fails and retries on its own.
12. **Engine overview in the Docker tab.** An Overview card at the top with the Docker and API versions, OS and architecture, container counts, image count and the engine's CPUs and memory, plus a disk usage breakdown (images, containers, volumes and build cache) as a stacked bar with a text legend. A profile that does not use Docker shows a short note instead. It refreshes every 30 seconds.
13. **Container logs.** A Logs button on each container row opens a read-only snapshot of its recent output (the last 200 lines, or 100, 500 or 1000), with a Refresh button and a Copy button. Stopped containers work too. Lines show as plain text with Docker's timestamps, and the dialog says when some output is not shown. There is no live follow mode.
14. **New profile dialog.** A New profile button on the dashboard and a Create profile command in the palette open a dialog with the basic settings: name, CPUs, memory, disk, root disk, runtime, VM type, architecture, mount type and Kubernetes with an optional version. A field left empty uses Colima's default. The browser and the server both validate, and the server's messages show next to the field. The first start downloads a VM image and can take several minutes, so on success the app opens that job's operation log.
15. **Toast notifications.** Short messages appear in the top left corner of the window: success and info toasts disappear after 5 seconds, error toasts stay until you close them. They pause while you hover over them, focus them or switch to another tab, show at most three at once (the rest wait their turn), and `F6` moves keyboard focus into them.
16. **Job results and action errors as toasts.** When a start, stop, restart or create finishes, a toast says so on whatever page you are on (`default: stop succeeded`, `default: start failed (exit 1)`, `default: restart cancelled`) with a View log link. A failure stays until you close it. A refused action (for example starting a profile that is already running) also shows as an error toast. The dashboard row and the profile page no longer add text under the status, so the layout does not shift.

If the history file cannot be opened (for example a newer version wrote it), the binary refuses to start and says so, because the file holds history only and can be deleted to start fresh.

### Added to the backlog this round: planned, not built yet

The enhanced Docker view (issues #78 to #86 and #92) is done. Nothing else from that plan is left.

Colima has no rename command, so renaming is not planned. Mounts and environment variables are accepted by `POST /api/profiles` under strict validation (the New profile form for them is planned). Security note: a mount exposes a host folder to the VM and every container in it, so the API accepts only existing directories, refuses the root, the home folder and the folders above it when writable, and never logs or echoes a path or an environment value. Custom disk images and raw k3s arguments are deliberately not supported.

This list moves from "planned" to "done" as each issue merges.

## What the backend serves

1. A loopback HTTP server with the token, Host and Origin checks described below.
2. `GET /healthz`, which needs no token.
3. Profile list and detail: `GET /api/profiles` and `GET /api/profiles/{name}`.
4. Start, stop and restart of a profile as jobs, plus job status and cancel: `GET /api/jobs/{id}` and `POST /api/jobs/{id}/cancel`. Jobs run one at a time per profile.
5. Live per-container usage for a profile, plus each container's status text, creation time and ports: `GET /api/profiles/{name}/containers`, read from the Docker API over the Colima socket.
6. Reading and editing a profile's resources: `GET` and `PUT /api/profiles/{name}/config`. A write is validated, applied atomically to the profile file and refused while a job runs on that profile.
7. The event stream: `GET /api/events` (Server Sent Events) carries `job.output` and `job.finished` for every job, and a `metric` event for each sample.
8. The history route `GET /api/profiles/{name}/metrics`, fed by the sampler.
9. The action log route `GET /api/profiles/{name}/actions`.
10. The Docker images of a profile, with tags, size and how many containers use each: `GET /api/profiles/{name}/images`.
11. The Docker volumes of a profile, with driver, creation time, size and how many containers mount each (size and count are `null` when the engine has no figure): `GET /api/profiles/{name}/volumes`.
12. The Docker networks of a profile, with driver, scope and how many containers are attached, stopped ones included: `GET /api/profiles/{name}/networks`.
13. The Docker engine of a profile, with its version, resources, container and image counts and a disk usage breakdown: `GET /api/profiles/{name}/engine`. A running profile that does not use Docker gets `409`.
14. The recent output of one container, as a bounded snapshot with Docker's timestamps (the last 200 lines by default, at most 1000), stopped containers included: `GET /api/profiles/{name}/containers/{id}/logs?tail=N`. There is no streaming.
15. Creating a profile with the basic settings (name, CPUs, memory, disk, root disk, runtime, VM type, architecture, mount type and Kubernetes) and the advanced ones (hostname, DNS servers and DNS hosts, SSH agent, Rosetta, port forwarder, inotify, network address, mode and interface, CPU type), host folder mounts and environment variables: `POST /api/profiles`. It starts a new VM as a `create` job that streams like any other job, and it refuses a name that already exists in any letter case. There is no route to delete or rename a profile.

The full contract is in [`docs/api.md`](docs/api.md).

## Requirements

1. macOS. The first release only supports macOS, because socket and configuration folder discovery assume it. Linux is a later addition.
2. [Colima](https://github.com/abiosoft/colima) on your `PATH`. It is built and tested against Colima 0.10.x. Other versions are not refused, but the output the tool parses may differ.
3. The Docker runtime in the Colima profile if you want container data. The Docker socket is the one Colima exposes for that profile. Profiles using containerd, Incus or Kubernetes are listed and managed but show no container data.
4. To build: Go 1.24 or newer, Node 22 or newer with npm, and GNU Make.

The backend exits at start with an error if it cannot find the `colima` binary.

## Build and run

```sh
make build        # installs frontend dependencies, builds the UI, then bin/colima-ui with the UI embedded
./bin/colima-ui   # listens on 127.0.0.1:8787
```

On start the program prints a line like this:

```
open http://127.0.0.1:8787/#token=<random token>
```

Open that URL in your browser. Use another port with `./bin/colima-ui -port 9000` or `COLIMA_UI_PORT=9000`.

Without `make build` you can still run `go run ./cmd/colima-ui`, but the UI is not embedded in that case and `/` answers 503 with a pointer to `make build`.

For frontend work, run the backend as above and start the Vite dev server from `web/` with `npm run dev`. It proxies `/api` to `http://127.0.0.1:8787`, or to whatever `COLIMA_UI_API` says.

## Security model

Colima UI controls a VM and the Docker socket, which is close to controlling your machine, so the backend is the security boundary and is strict about who can talk to it.

1. **Loopback only.** The server binds to `127.0.0.1` and never to all interfaces.
2. **Per-startup token.** Each start generates a fresh random token. Every `/api` call must send it as `Authorization: Bearer <token>`. There is no query-string fallback. `/healthz` and the embedded UI files need no token, because the page has to load before it can read the token.
3. **Token in the URL fragment.** The token is printed to stdout, never to the log, as part of the `#token=` fragment. Browsers do not send fragments to the server, so the token stays out of access logs and `Referer` headers. The page reads it from the fragment and attaches it to its API calls. Restarting the program invalidates the old token.
4. **Host check.** Every request must carry a loopback `Host` (`127.0.0.1:<port>` or `localhost:<port>`). This defends against DNS rebinding.
5. **Origin check.** Any `Origin` header must be the server's own. This blocks cross-site requests from other pages in your browser.
6. **No arbitrary commands.** Only the Colima adapter runs the `colima` binary, through typed Go functions with fixed arguments. No endpoint accepts a command string. Config values are validated before they touch disk or a command line.

## Where history lives

Current state is never stored. Only history is, in a SQLite file: metric samples (every 5 seconds, kept raw for 24 hours, then as 1 minute averages for 30 days) and an action log. The file is `<user config dir>/colima-ui/history.db`, overridable with `COLIMA_UI_DATA_DIR` (an absolute path to a folder used only for this). The binary creates the folder with mode 0700 and the file with mode 0600 on start.

## Development

```sh
make setup   # once per clone: npm ci in web/, then installs the pre-push hook
make check   # the local gate: gofmt, go vet, go test -race, web lint, test and build, CGO_ENABLED=0 go build
```

`make check` is also what the pre-push hook runs. There is no hosted CI.

## More

1. [`C4_Architecture.md`](C4_Architecture.md) is the design source of truth: context, containers, components, data model and decisions.
2. [`docs/api.md`](docs/api.md) is the REST and event stream contract.
