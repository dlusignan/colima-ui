package colima

import (
	"go/parser"
	"go/token"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// execImporters returns the module-relative paths of Go files under root that
// import os/exec, excluding files inside allowedDir (module-relative, slash
// separated). It parses every .go file regardless of build tags, tests included.
func execImporters(t *testing.T, root, allowedDir string) []string {
	t.Helper()
	var bad []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		f, perr := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if perr != nil {
			t.Errorf("parse %s: %v", rel, perr)
			return nil
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"os/exec"` && !strings.HasPrefix(rel, allowedDir+"/") {
				bad = append(bad, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bad
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// Only internal/colima may import os/exec (C4: the adapter is the only place
// that runs the colima binary).
func TestOnlyColimaPackageImportsOsExec(t *testing.T) {
	if bad := execImporters(t, moduleRoot(t), "internal/colima"); len(bad) > 0 {
		t.Errorf("os/exec imported outside internal/colima: %v", bad)
	}
}

// Negative case so the guard cannot silently pass.
func TestExecGuardDetectsViolations(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/colima/ok.go", "package colima\nimport _ \"os/exec\"\n")
	write("internal/colimax/sneaky.go", "package x\nimport _ \"os/exec\"\n")
	write("cmd/a/a_test.go", "package a\nimport _ \"os/exec\"\n")
	write("cmd/b/tagged.go", "//go:build ignore\n\npackage b\nimport _ \"os/exec\"\n")
	write("cmd/c/fine.go", "package c\nimport _ \"os\"\n")
	got := strings.Join(execImporters(t, root, "internal/colima"), ",")
	for _, want := range []string{"internal/colimax/sneaky.go", "cmd/a/a_test.go", "cmd/b/tagged.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("guard missed %s (got %q)", want, got)
		}
	}
	if strings.Contains(got, "internal/colima/ok.go") || strings.Contains(got, "fine.go") {
		t.Errorf("guard flagged allowed file: %q", got)
	}
}

var (
	docNets = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	ipv4Re     = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	ipv6Re     = regexp.MustCompile(`(?i)[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}`)
	homeRe     = regexp.MustCompile(`/home/([^/"\s]+)`)
	badPathsRe = regexp.MustCompile(`/Users/|/root/|/var/folders/|/private/`)
)

// fixtureProblems lists reasons content is not synthetic: a real-looking home
// path, or an address outside the documentation ranges.
func fixtureProblems(s string) []string {
	var out []string
	if m := badPathsRe.FindString(s); m != "" {
		out = append(out, "path "+m)
	}
	for _, m := range homeRe.FindAllStringSubmatch(s, -1) {
		if m[1] != "user" {
			out = append(out, "path "+m[0])
		}
	}
	for _, m := range append(ipv4Re.FindAllString(s, -1), ipv6Re.FindAllString(s, -1)...) {
		addr, err := netip.ParseAddr(m)
		if err != nil {
			continue
		}
		ok := false
		for _, n := range docNets {
			ok = ok || n.Contains(addr)
		}
		if !ok {
			out = append(out, "address "+m)
		}
	}
	return out
}

func TestFixturesAreSynthetic(t *testing.T) {
	files, err := filepath.Glob("testdata/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if p := fixtureProblems(string(b)); len(p) > 0 {
			t.Errorf("%s: %v", f, p)
		}
	}
}

func TestFixtureCheckerCatchesRealData(t *testing.T) {
	bad := []string{
		`unix:///Users/someone/.colima/x.sock`,
		`/home/alice/.colima`,
		`"address":"10.0.0.5"`,
		`"address":"8.8.8.8"`,
		`"address":"2606:4700::1111"`,
		`/var/folders/ab/cd`,
	}
	for _, s := range bad {
		if len(fixtureProblems(s)) == 0 {
			t.Errorf("checker missed %q", s)
		}
	}
	good := []string{`/home/user/.colima`, `"address":"192.0.2.10"`, `"address":"2001:db8::1"`, `colima version 0.10.3`}
	for _, s := range good {
		if p := fixtureProblems(s); len(p) != 0 {
			t.Errorf("checker flagged %q: %v", s, p)
		}
	}
}
