package config

import (
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// Class says what a changed field needs.
type Class string

const (
	// ClassRestart: takes effect on the next start (reported only when the VM runs).
	ClassRestart Class = "restart"
	// ClassRecreate: cannot change once the VM exists; the VM must be deleted and recreated.
	ClassRecreate Class = "recreate"
)

// Change is one changed field and its class.
type Change struct {
	Field string `json:"field"`
	Class Class  `json:"class"`
}

// immutable fields cannot change after the VM is created.
var immutable = map[string]bool{"arch": true, "runtime": true, "vmType": true, "mountType": true}

// Diff compares the stored profile (old) with a proposed one (next) and lists
// changed fields in a stable order. Colima reads the file only at start, so
// with vmRunning every change is restart-class; without it, restart-class
// changes are omitted. Recreate-class changes (the immutable four, and a disk
// decrease) are reported regardless, because the VM exists either way.
// Fields the struct does not type are compared through the retained nodes and
// count as restart-class.
func Diff(old, next *Profile, vmRunning bool) []Change {
	if old == nil || next == nil {
		return nil
	}
	var out []Change
	seen := map[string]bool{}
	add := func(field string, c Class) {
		if (c == ClassRestart && !vmRunning) || seen[field] {
			return
		}
		seen[field] = true
		out = append(out, Change{Field: field, Class: c})
	}
	cmp := func(field string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		if immutable[field] {
			add(field, ClassRecreate)
		} else {
			add(field, ClassRestart)
		}
	}
	cmp("cpu", old.CPU, next.CPU)
	cmp("memory", old.Memory, next.Memory)
	if !reflect.DeepEqual(old.Disk, next.Disk) {
		if old.Disk != nil && next.Disk != nil && *next.Disk < *old.Disk {
			add("disk", ClassRecreate)
		} else {
			add("disk", ClassRestart)
		}
	}
	cmp("rootDisk", old.RootDisk, next.RootDisk)
	cmp("arch", old.Arch, next.Arch)
	cmp("runtime", old.Runtime, next.Runtime)
	cmp("vmType", old.VMType, next.VMType)
	cmp("mountType", old.MountType, next.MountType)
	cmp("hostname", old.Hostname, next.Hostname)
	cmp("mounts", old.Mounts, next.Mounts)
	cmp("env", old.Env, next.Env)
	cmp("kubernetes", old.Kubernetes, next.Kubernetes)
	cmp("network", old.Network, next.Network)

	// Everything else: compare untyped top-level keys, plus the whole kubernetes
	// and network nodes (their untyped sub-keys such as k3sArgs).
	oa, na := untyped(old.node), untyped(next.node)
	keys := map[string]bool{}
	for k := range oa {
		keys[k] = true
	}
	for k := range na {
		keys[k] = true
	}
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if !reflect.DeepEqual(oa[k], na[k]) {
			add(k, ClassRestart)
		}
	}
	return out
}

// typedKeys are fully covered by the typed comparisons in Diff. kubernetes and
// network are typed only in part, so their nodes are compared too.
var typedKeys = map[string]bool{
	"cpu": true, "memory": true, "disk": true, "rootDisk": true, "arch": true,
	"runtime": true, "vmType": true, "mountType": true, "hostname": true,
	"mounts": true, "env": true,
}

func untyped(n *yaml.Node) map[string]any {
	out := map[string]any{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if typedKeys[k] {
			continue
		}
		var v any
		if err := n.Content[i+1].Decode(&v); err == nil {
			out[k] = v
		}
	}
	return out
}
