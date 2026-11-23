package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Gate is the per-profile exclusion Apply runs inside. The Operation Manager
// satisfies it structurally, so this package never imports it.
type Gate interface {
	// TryExclusive runs fn while holding the profile's lock. It returns
	// held=false without running fn when a job is running or starting, or
	// another holder has the profile.
	TryExclusive(profile string, fn func() error) (held bool, err error)
}

// Changes is the set of edits for Apply. A nil field is left alone, matching
// PUT /api/profiles/{name}/config (absent fields untouched).
type Changes struct {
	CPU    *int
	Memory *float64 // GiB, may be fractional
	Disk   *int     // GiB
}

// Apply validates ch, then writes it to the profile's colima.yaml. It returns
// the fields that need a restart (or a recreate), computed by Diff, and an
// empty list when nothing changed (the file is then not touched).
//
// Validation covers only the changed fields, so an unrelated hand-edited bad
// value does not block the edit. The whole read, validate and write sequence
// runs inside the Gate's hold; *BusyError is returned when the hold is not
// granted. A failed validation returns *ValidationError and writes nothing.
// Other errors: ErrInvalidProfileName, ErrProfileNotFound, ErrNoGate,
// ErrMultiDocument, ErrUnwritableTarget, ErrConflict, *ReadError, *ParseError,
// *TypeError, *WriteError.
//
// The VM probe runs before the hold, so a start job that finishes between the
// probe and TryExclusive can make the restart list come back empty for a VM
// that is now running. The probe is skipped when ch changes nothing.
func (s *Service) Apply(profile string, ch Changes) ([]Change, error) {
	if err := ValidateProfileName(profile); err != nil {
		return nil, err
	}
	if s.gate == nil {
		return nil, ErrNoGate
	}
	// Probe before taking the hold: it may shell out. An error or a missing
	// probe counts as running, which over-reports the restart list.
	running := true
	if s.vmRunning != nil && (ch.CPU != nil || ch.Memory != nil || ch.Disk != nil) {
		if r, err := s.vmRunning(profile); err == nil {
			running = r
		}
	}
	var changes []Change
	held, err := s.gate.TryExclusive(profile, func() error {
		var aerr error
		changes, aerr = s.applyLocked(profile, ch, running)
		return aerr
	})
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, &BusyError{Profile: profile}
	}
	return changes, nil
}

// fileState is what was read from the real target of colima.yaml.
type fileState struct {
	target string // resolved path of the real file
	data   []byte
	hash   [sha256.Size]byte
	mode   fs.FileMode
}

// readTarget resolves colima.yaml through symlinks and reads it.
func readTarget(dir string) (*fileState, error) {
	link := filepath.Join(dir, ConfigFileName)
	if _, err := os.Lstat(link); err != nil {
		return nil, notFoundOrRead(err)
	}
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrUnwritableTarget // dangling link
		}
		return nil, &ReadError{Err: stripPath(err)}
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, &ReadError{Err: stripPath(err)}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, &ReadError{Err: stripPath(err)}
	}
	if !st.Mode().IsRegular() || hardLinked(st) {
		return nil, ErrUnwritableTarget
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, &ReadError{Err: stripPath(err)}
	}
	return &fileState{target: target, data: data, hash: sha256.Sum256(data), mode: st.Mode().Perm()}, nil
}

func (s *Service) applyLocked(profile string, ch Changes, running bool) ([]Change, error) {
	dir, err := s.resolveProfileDir(profile)
	if err != nil {
		return nil, err
	}
	state, err := readTarget(dir)
	if err != nil {
		return nil, err
	}
	// Two parses: current stays pristine for validation and Diff, work is
	// edited in place.
	current, err := Parse(state.data)
	if err != nil {
		return nil, err
	}
	if err := singleDocument(state.data); err != nil {
		return nil, err
	}
	probe := &Profile{CPU: ch.CPU, Memory: ch.Memory, Disk: ch.Disk}
	if fe := Validate(probe, s.limits, current); len(fe) > 0 {
		return nil, &ValidationError{Fields: fe}
	}
	work, err := Parse(state.data)
	if err != nil {
		return nil, err
	}
	if !applyToNode(work, current, ch) {
		return []Change{}, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(work.doc); err != nil {
		return nil, &WriteError{Err: errors.New("encode failed")}
	}
	if err := enc.Close(); err != nil {
		return nil, &WriteError{Err: errors.New("encode failed")}
	}
	out := buf.Bytes()
	if len(out) > maxFileSize {
		return nil, &WriteError{Err: errors.New("result too large")}
	}
	// Never write a file this package could not read back.
	next, err := Parse(out)
	if err != nil {
		return nil, &WriteError{Err: errors.New("result does not parse")}
	}
	if err := checkAliasEffects(current, next, ch); err != nil {
		return nil, err
	}
	if err := s.writeAtomic(dir, state, out); err != nil {
		return nil, err
	}
	if s.onWritten != nil {
		var fields []string
		for _, c := range Diff(current, next, true) {
			fields = append(fields, c.Field)
		}
		s.callOnWritten(profile, fields)
	}
	changes := Diff(current, next, running)
	if changes == nil {
		changes = []Change{}
	}
	return changes, nil
}

// checkAliasEffects compares the resolved profiles before and after the edit.
// Editing an anchored scalar also changes its aliases, and those fields skip
// validation, so any field outside ch that changed value is refused, and each
// requested field must have resolved to exactly the requested value.
func checkAliasEffects(current, next *Profile, ch Changes) error {
	if (ch.CPU != nil && (next.CPU == nil || *next.CPU != *ch.CPU)) ||
		(ch.Memory != nil && (next.Memory == nil || *next.Memory != *ch.Memory)) ||
		(ch.Disk != nil && (next.Disk == nil || *next.Disk != *ch.Disk)) {
		return ErrAliasedValue
	}
	for _, c := range Diff(current, next, true) {
		switch {
		case c.Field == "cpu" && ch.CPU != nil:
		case c.Field == "memory" && ch.Memory != nil:
		case c.Field == "disk" && ch.Disk != nil:
		default:
			return ErrAliasedValue
		}
	}
	return nil
}

// singleDocument rejects input with a second YAML document.
func singleDocument(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var first, second yaml.Node
	if err := dec.Decode(&first); err != nil {
		return nil // empty or comment-only; Parse already vetted the rest
	}
	if err := dec.Decode(&second); err != io.EOF {
		return ErrMultiDocument
	}
	return nil
}

// applyToNode edits work's mapping in place and reports whether anything
// differs from current. Keys are edited by name; nothing is marshalled from the
// typed struct, so unknown keys and comments stay as they were.
func applyToNode(work, current *Profile, ch Changes) bool {
	changed := false
	if ch.CPU != nil && !(current.CPU != nil && *current.CPU == *ch.CPU) {
		setScalar(work.node, "cpu", strconv.Itoa(*ch.CPU), "!!int")
		changed = true
	}
	if ch.Memory != nil && !(current.Memory != nil && *current.Memory == *ch.Memory) {
		v := strconv.FormatFloat(*ch.Memory, 'f', -1, 64)
		tag := "!!int"
		if *ch.Memory != float64(int64(*ch.Memory)) {
			tag = "!!float"
		}
		setScalar(work.node, "memory", v, tag)
		changed = true
	}
	if ch.Disk != nil && !(current.Disk != nil && *current.Disk == *ch.Disk) {
		setScalar(work.node, "disk", strconv.Itoa(*ch.Disk), "!!int")
		changed = true
	}
	return changed
}

// setScalar sets key to a plain scalar. An existing scalar keeps its comments
// and anchor; an alias is replaced by a plain scalar carrying its comments; a
// missing key is appended.
func setScalar(root *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		v := root.Content[i+1]
		if v.Kind == yaml.ScalarNode {
			v.Value, v.Tag, v.Style = value, tag, 0
			return
		}
		root.Content[i+1] = &yaml.Node{
			Kind: yaml.ScalarNode, Tag: tag, Value: value,
			HeadComment: v.HeadComment, LineComment: v.LineComment, FootComment: v.FootComment,
		}
		return
	}
	if len(root.Content) == 0 {
		root.Style = 0 // an empty {} would stay in flow style
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

// writeAtomic writes data next to the real target and renames it into place.
// The temp file lives in the target's own folder (rename needs one filesystem)
// and is removed on any failure. The file hash is re-checked immediately before
// the rename; a mismatch returns ErrConflict. A crash before the rename leaves
// the old file and a stray dot-file, never a partial colima.yaml.
func (s *Service) writeAtomic(dir string, state *fileState, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(state.target), "."+ConfigFileName+".*.tmp")
	if err != nil {
		return &WriteError{Err: stripPath(err)}
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return &WriteError{Err: stripPath(err)}
	}
	if err = tmp.Chmod(state.mode); err != nil {
		return &WriteError{Err: stripPath(err)}
	}
	if err = tmp.Sync(); err != nil { // F_FULLFSYNC on Darwin
		return &WriteError{Err: stripPath(err)}
	}
	if err = tmp.Close(); err != nil {
		return &WriteError{Err: stripPath(err)}
	}
	if s.beforeRename != nil {
		s.beforeRename()
	}
	again, rerr := readTarget(dir)
	if rerr != nil || again.target != state.target || again.hash != state.hash {
		err = ErrConflict
		return err
	}
	rename := os.Rename
	if s.renameFn != nil {
		rename = s.renameFn
	}
	if err = rename(tmpName, state.target); err != nil {
		err = &WriteError{Err: stripPath(err)}
		return err
	}
	// Best effort: the rename already took effect, and some filesystems refuse
	// to sync a folder, so a failure here is not reported.
	if d, derr := os.Open(filepath.Dir(state.target)); derr == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// callOnWritten runs the hook after the file was replaced. A panic is logged by
// type and swallowed: the write already happened, so it must not turn into a
// dropped connection. A hook that blocks holds the profile's slot.
func (s *Service) callOnWritten(profile string, fields []string) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("config written hook panicked", "panicType", fmt.Sprintf("%T", p))
		}
	}()
	s.onWritten(profile, fields)
}
