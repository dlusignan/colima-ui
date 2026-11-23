// Package config reads, validates and writes Colima profile YAML files, which are the source of truth.
//
// # Write policy
//
// Apply edits the retained yaml.Node in place, key by key, and never marshals
// the typed Profile. A round trip therefore keeps:
//
//   - keys the struct does not type, with their values, order and position,
//     including anchors and aliases, quote styles and block scalars;
//   - comments (head, line and foot), attached to the same keys;
//   - the leading comment of a file, even when a blank line follows it, and the
//     comments of a comment-only file (kept on the document node).
//
// It does not keep, and this list is exactly what the tests pin:
//
//   - blank lines between sections;
//   - extra spaces before an inline comment (alignment);
//   - the original indentation, which is normalised to two spaces (what Colima
//     writes);
//   - CRLF line endings, which become LF;
//   - the plain spelling of a merge key: "<<: *b" comes back as "!!merge <<: *b".
//
// Typed values are read from the whole file first, so a type error in an
// unrelated field (for example "rootDisk: 1.5") still blocks every edit. This
// is intended. Validation itself covers only the changed fields.
//
// The disk-shrink rule applies only when disk is present in the file; when it
// is absent, Colima's own default of 100 GiB applies and is not checked.
//
// Only a single-document file is accepted for writing (ErrMultiDocument
// otherwise). Editing a scalar that carries an anchor would change every alias
// of that anchor too, and those fields skip validation, so Apply compares the
// resolved profile before and after and refuses with ErrAliasedValue when any
// field outside the change set would change. Hard-linked files are refused too
// (ErrUnwritableTarget), since a rename would detach the other names.
//
// Colima itself rewrites the profile YAML on "colima start" by default, so the
// file can change underneath a pending edit after a start. Apply compares a
// SHA-256 of the file with the file just before the rename and returns
// ErrConflict on a mismatch (no flock: neither Colima nor editors take one).
//
// The VM state is probed before the gate is taken (it may shell out), so a
// start finishing between the probe and the hold can under-report the restart
// list. The design accepts this.
//
// A symlinked colima.yaml is written through: the temp file and rename happen
// in the real target's folder, so the link survives. A dangling link or a
// non-regular target is refused (ErrUnwritableTarget).
package config
