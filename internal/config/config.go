package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

// maxFileSize bounds how much of a profile file is read.
const maxFileSize = 1 << 20

// Mount is one host folder shared into the VM.
type Mount struct {
	Location   string `yaml:"location"`
	MountPoint string `yaml:"mountPoint,omitempty"`
	Writable   bool   `yaml:"writable"`
}

// Kubernetes is the kubernetes block. Keys not listed stay in the node.
type Kubernetes struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Version string `yaml:"version,omitempty"`
}

// Network is the network block. Keys not listed stay in the node.
type Network struct {
	Mode string `yaml:"mode,omitempty"`
}

// Profile holds the typed view of a profile plus the retained YAML node.
// Pointer fields are nil when the key is absent. Memory is GiB and may be
// fractional. Keys the struct does not know, and all comments, live only in the
// node, so a later write (issue #13) can edit the node and encode it back.
type Profile struct {
	CPU        *int              `yaml:"cpu"`
	Memory     *float64          `yaml:"memory"`
	Disk       *int              `yaml:"disk"`
	RootDisk   *int              `yaml:"rootDisk"`
	Arch       string            `yaml:"arch"`
	Runtime    string            `yaml:"runtime"`
	VMType     string            `yaml:"vmType"`
	MountType  string            `yaml:"mountType"`
	Hostname   string            `yaml:"hostname"`
	Mounts     []Mount           `yaml:"mounts"`
	Env        map[string]string `yaml:"env"`
	Kubernetes Kubernetes        `yaml:"kubernetes"`
	Network    Network           `yaml:"network"`

	node *yaml.Node
}

// Node returns the retained root mapping node (never nil for a parsed
// profile). Mutating it changes what a later encode produces.
func (p *Profile) Node() *yaml.Node { return p.node }

var lineRE = regexp.MustCompile(`line (\d+)`)

// Parse decodes profile YAML. It never panics: a recover guard turns any
// decoder panic into a *ParseError. Errors are *ParseError for malformed YAML
// and *TypeError for wrong-typed values.
func Parse(data []byte) (p *Profile, err error) {
	defer func() {
		if r := recover(); r != nil {
			p, err = nil, &ParseError{Message: "unreadable document"}
		}
	}()
	if len(data) > maxFileSize {
		return nil, &ParseError{Message: "file too large"}
	}
	var doc yaml.Node
	if uerr := yaml.Unmarshal(data, &doc); uerr != nil {
		return nil, toParseError(uerr)
	}
	root := &doc
	switch {
	case doc.Kind == 0: // empty file
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case doc.Kind == yaml.DocumentNode && len(doc.Content) == 1:
		root = doc.Content[0]
	}
	if root.Kind == yaml.AliasNode || root.Kind != yaml.MappingNode {
		return nil, &ParseError{Line: root.Line, Message: "top level must be a mapping"}
	}
	problems := intKeyProblems(root)
	p = &Profile{node: root}
	if derr := root.Decode(p); derr != nil {
		var te *yaml.TypeError
		if !errors.As(derr, &te) {
			return nil, toParseError(derr)
		}
		problems = append(problems, te.Errors...)
	}
	if len(problems) > 0 {
		return nil, &TypeError{Problems: problems}
	}
	return p, nil
}

func toParseError(err error) *ParseError {
	msg := err.Error()
	pe := &ParseError{Message: trimYAMLPrefix(msg)}
	if m := lineRE.FindStringSubmatch(msg); m != nil {
		pe.Line, _ = strconv.Atoi(m[1])
	}
	return pe
}

func trimYAMLPrefix(s string) string {
	const pre = "yaml: "
	if len(s) > len(pre) && s[:len(pre)] == pre {
		return s[len(pre):]
	}
	return s
}

// Read loads <base>/<name>/colima.yaml. Errors: ErrInvalidProfileName,
// ErrProfileNotFound, *ReadError, *ParseError, *TypeError.
func (s *Service) Read(name string) (*Profile, error) {
	dir, err := s.resolveProfileDir(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return nil, notFoundOrRead(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, &ReadError{Err: stripPath(err)}
	}
	if !st.Mode().IsRegular() {
		return nil, &ReadError{Err: errors.New("not a regular file")}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, &ReadError{Err: stripPath(err)}
	}
	return Parse(data)
}

// intKeys must hold whole numbers. yaml.v3 silently truncates 2.5 into an int
// field, so the node tag is checked before decoding.
var intKeys = map[string]bool{"cpu": true, "disk": true, "rootDisk": true}

func intKeyProblems(root *yaml.Node) []string {
	var out []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if intKeys[k.Value] && v.Kind == yaml.ScalarNode && v.Tag != "!!int" && v.Tag != "!!null" {
			out = append(out, fmt.Sprintf("line %d: %s must be a whole number", v.Line, k.Value))
		}
	}
	return out
}
