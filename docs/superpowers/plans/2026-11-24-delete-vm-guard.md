# Delete VM guarded by its Docker contents: implementation plan

**Goal:** a Delete button after Restart (dashboard rows and profile page action bar) that only allows deleting a VM when it holds no Docker containers, images or volumes, and otherwise tells the user to remove them first.

**Architecture:** a new read route reports what a running profile's Docker holds; the existing delete route (#144) enforces the same rule on the server and re-counts inside the operation slot; the UI dialog asks the server what is inside when it opens, then shows either the typed-name confirmation or the "remove these first" message.

**Spec:** this file (owner approved the design in conversation, 2026-11-24).

## Design decisions (owner approved; calls marked "mine" are proposals the owner did not object to)
1. Counts: all containers (any state), all images, all volumes. Networks are not counted (the Docker defaults always exist).
2. The Delete button sits after Restart in the shared `web/src/components/ProfileActions.tsx` (dashboard rows and profile page). The profile page Danger zone is removed.
3. Clicking Delete opens a dialog that fetches the contents once (no polling). Empty: type the profile name to confirm, runtime-data checkbox stays unchecked by default. Not empty: the message "Docker elements must be removed before this VM can be deleted" with the counts and a pointer to the Docker tab; no delete control.
4. Server rule at `POST /api/profiles/{name}/delete`: running Docker profile must count zero (else 409 `invalid_state`, `details.reason` `docker_not_empty` with the counts); the count is repeated inside the operation slot just before `colima delete`. A running delete is now allowed when empty (colima delete --force stops the VM). If Docker cannot be read, refuse (fail closed).
5. Mine: a stopped profile is refused with `details.reason` `cannot_verify` ("start the profile first so its Docker contents can be checked"). A broken profile is allowed (cannot be checked, and failed creates must stay removable). A running profile whose runtime is not Docker is allowed (no Docker in it).
6. Out of scope: networks, deleting Docker objects from the delete dialog, bulk delete.

## Tasks
1. Backend issue: contents route, delete rule, in-slot recount, docs, C4 decision row. Opus security review.
2. Frontend issue: button placement, dialog flow, remove Danger zone. Depends on 1.

## Review focus
1. Container created between the dialog check and delete: the in-slot recount refuses or the residual race is documented.
2. Docker unreachable on a running VM: delete refused, not allowed.
3. Stopped VM with data: never deletable from the UI.
4. A refusal never logged as a colima exit (NotRun), and the audit row names the reason.
5. Hostile profile names and a profile whose runtime differs in letter case.
