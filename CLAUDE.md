# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

Colima UI is a local web interface on top of Colima (VM profiles: start/stop/restart/config, plus visibility into VM capacity vs. actual usage per container). **No code exists yet.** The repo contains only `C4_Architecture.md` (Draft 0.1, C4 levels 1–3; level 4 intentionally deferred until an implementation exists). There are no build, lint, or test commands yet; add them here once the Go module and React app are scaffolded. It is not a git repository yet.

`C4_Architecture.md` is the design source of truth. Keep it updated when decisions change, and check its "Open Questions" section (supported Colima versions, Incus/Kubernetes scope, history retention, multi-profile at launch, Linux support) before assuming an answer.

## Planned architecture

Three C4 "containers": a React SPA, a Go backend (standard library `net/http`, Docker Go SDK), and a SQLite history file (pure-Go `modernc.org/sqlite`, no cgo). The backend embeds the built frontend, so the product ships as one binary running on the developer's machine only.

Invariants that span multiple components:

- **Live state is never read from the database.** Current state comes from Colima, the profile YAML files, and the Docker API, so it cannot drift when the user also runs `colima` in a terminal. SQLite holds only `metric_sample` and `action_log` history.
- **Colima Adapter is the only code that executes the `colima` binary.** Every subcommand is an explicit Go function with typed arguments, never a string built from user input. No endpoint accepts an arbitrary command. Version differences in Colima's output are isolated here.
- **Operation Manager serializes work per profile** (no concurrent start/stop on one VM). Jobs have an ID, status, captured output, and a context that cancels the child process. Output is published to the SSE Event Stream.
- **Config Service treats the YAML files as authority.** It validates values against allowed ranges, reports which changes need a VM restart, and refuses to write while an operation is running on that profile.
- **Metrics Sampler** (ticker) records allocated (config) vs. used (VM and per-container) figures into the History Store (in-memory ring buffer plus SQLite, with retention cleanup) and publishes live values over SSE.
- **HTTP Layer is thin**: routing, auth, static assets; no Colima knowledge.
- Container and image data uses the Docker Engine API (Go SDK over the Colima-exposed Unix socket), not CLI parsing.
- Frontend: React with a query cache library for REST data and a single SSE connection for live data. Planned views: dashboard, profile detail, capacity charts, config editor, operation log.

## Security requirements

The backend is the security perimeter, since controlling the VM and Docker socket is effectively controlling the machine:

- Bind to `127.0.0.1` only, never all interfaces.
- Require a random per-startup token on every API call.
- Check `Host` and `Origin` headers (DNS rebinding and cross-site request defense).
- Validate config values before they touch disk or a command line.

## Agent toolkit bindings

`.claude/` holds skills, agents and commands vendored from `skyfox675/agents-skills` (upstream commit `6b9d66f`), plus `caveman` (`JuliusBrussee/caveman` `70f4f34`, SKILL.md only, installer and hooks deliberately not used) and `humanizer` (`blader/humanizer` `a32f244`). Vendored files are plain copies: edit them freely, and keep the upstream commits above when re-syncing. They are project-agnostic and refer to the placeholders below.

| Placeholder | Value |
| --- | --- |
| `<repo-slug>` / `<owner>/<repo>` | `dlusignan/colima-ui` (GitHub account `dlusignan`; `gh auth switch -u dlusignan` if another account is active) |
| `<integration-branch>` | `main` (the repo default, so `Closes #N` auto-closes issues) |
| `<merge-strategy>` | Undecided. No merge queue. |
| `<local-gate>`, `<required-checks>`, `<derived-artifacts>` | Undecided. No code or CI yet. |
| `<bot-reviewer>`, `<ai-mention-trigger>`, `<per-pr-deploy>`, `<flake-ledger>`, `<failure-catalog>` | None |
| `<force-push-policy>` | `--force-with-lease` on feature branches only, never on `main` |
| `<commit-convention>` | Undecided |
| `<traceability-scheme>`, `<second-tracker>` | None |
| Labels | Not yet created in the repo. `.claude/skills/gh-issue-labels/scripts/bootstrap-labels.sh` creates them (writes to GitHub: run only with the owner's approval). |

The PR watcher lanes (`pr-comments`, `pr-checks`, `pr-cleanup`, `pr-dispatcher`) assume CI and PR review; they have nothing to do until those exist. `/gh-issue`, `/gh-issue-groom` and `/gh-issue-recon` work now.
