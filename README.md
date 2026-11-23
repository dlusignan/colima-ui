# colima-ui

A local web interface for Colima. Manage your profiles, see what your VM is allocated versus what it really uses, and stop guessing why your containers are slow.

Colima UI runs on your machine as a single binary. It reads Colima's own state (the `colima` CLI, the profile YAML files and the Docker API), so it stays correct when you also use `colima` in a terminal. It never holds a copy of the current state.

## Status

The project is under construction. The backend and the frontend shell exist, but not everything the design describes is reachable yet. What works in the binary built from `main` today:

1. A loopback HTTP server with the token, Host and Origin checks described below.
2. `GET /healthz`, which needs no token.
3. Profile list and detail: `GET /api/profiles` and `GET /api/profiles/{name}`.
4. Start, stop and restart of a profile as jobs, plus job status and cancel: `GET /api/jobs/{id}` and `POST /api/jobs/{id}/cancel`. Jobs run one at a time per profile.
5. Live per-container usage for a profile: `GET /api/profiles/{name}/containers`, read from the Docker API over the Colima socket.
6. Reading and editing a profile's resources: `GET` and `PUT /api/profiles/{name}/config`. A write is validated, applied atomically to the profile file and refused while a job runs on that profile.
7. The embedded React app, with a working operation log view. The dashboard, profile, capacity and config views are placeholders.

8. The event stream: `GET /api/events` (Server Sent Events) carries `job.output` and `job.finished` for every job, and a `metric` event for each sample.
9. The metrics sampler, which reads VM and per-container usage every 5 seconds and refreshes when a job finishes, and the history route `GET /api/profiles/{name}/metrics`.
10. The action log: every finished job and every applied config change is recorded, and `GET /api/profiles/{name}/actions` lists the recent rows.

Every backend component in the design is wired. If the history file cannot be opened (for example a newer version wrote it), the binary refuses to start and says so, because the file holds history only and can be deleted to start fresh.

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
