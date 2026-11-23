package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/httpapi"
	"github.com/dlusignan/colima-ui/internal/operations"
)

// creationManager is the part of *operations.Manager the creator uses.
type creationManager interface {
	Busy(profile string) (jobID string, running bool)
	Submit(r operations.Request) (operations.JobSummary, error)
}

// profileCreator is the production httpapi.ProfileCreator. It is the only
// place a creation request becomes colima.StartOptions: every value is checked
// before Submit, and the adapter checks the options again when it builds the
// command.
type profileCreator struct {
	mgr    creationManager
	exists func(ctx context.Context, name string) (bool, error)
	limits config.Limits
}

// reservedNameRule is the fixed message for a name Colima maps onto another
// profile.
const reservedNameRule = "must not be colima or start with colima-, which Colima maps to another profile"

// reservedName reports whether Colima would map name onto a different profile:
// "colima" is the default profile and a "colima-" prefix is stripped, so a
// create on either would run a start against an existing profile and rewrite
// its settings, under a slot key that differs from that profile's. The check
// ignores case because the Colima folder is case-insensitive by default.
func reservedName(name string) bool {
	l := strings.ToLower(name)
	return l == "colima" || strings.HasPrefix(l, "colima-")
}

const nameRule = "must start with a letter or digit and use only letters, digits, dots, underscores and hyphens, at most 64 characters"

// creationFieldNames maps the Config Service's field names to the creation
// body's keys. Unlisted names (arch, runtime, vmType, mountType) are the same.
var creationFieldNames = map[string]string{
	"cpu":                "cpus",
	"memory":             "memoryGiB",
	"disk":               "diskGiB",
	"rootDisk":           "rootDiskGiB",
	"kubernetes.version": "kubernetesVersion",
}

// Create implements httpapi.ProfileCreator. Order: validation (every bad field
// at once), busy, exists, then Submit, which stays authoritative for busy
// because the earlier steps are reads outside the slot.
func (c profileCreator) Create(ctx context.Context, p httpapi.NewProfile) (string, error) {
	fields := map[string]string{}
	switch {
	case p.Name == "":
		fields["name"] = "is required"
	// Both validators on purpose: config's is strictly stronger, colima's is
	// the adapter's own check and stays as defence in depth.
	case colima.ValidateProfile(p.Name) != nil || config.ValidateProfileName(p.Name) != nil:
		fields["name"] = nameRule
	case reservedName(p.Name):
		fields["name"] = reservedNameRule
	}
	prof := &config.Profile{
		CPU: p.CPUs, Memory: p.MemoryGiB, Disk: p.DiskGiB, RootDisk: p.RootDiskGiB,
		Arch: p.Arch, Runtime: p.Runtime, VMType: p.VMType, MountType: p.MountType,
		Kubernetes: config.Kubernetes{Enabled: &p.Kubernetes, Version: p.KubernetesVersion},
	}
	for _, fe := range config.ValidateNew(prof, c.limits) {
		key := fe.Field
		if w, ok := creationFieldNames[key]; ok {
			key = w
		}
		fields[key] = fe.Message
	}
	if len(fields) > 0 {
		return "", &httpapi.ValidationError{Fields: fields}
	}

	if id, running := c.mgr.Busy(p.Name); running {
		return "", &operations.BusyError{Profile: p.Name, JobID: id}
	}
	taken, err := c.exists(ctx, p.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", httpapi.ErrExistsCheckFailed, err)
	}
	if taken {
		return "", httpapi.ErrProfileExists
	}

	sum, err := c.mgr.Submit(operations.Request{
		Action:  operations.ActionCreate,
		Profile: p.Name,
		Options: colima.StartOptions{
			CPUs: p.CPUs, MemoryGiB: p.MemoryGiB, DiskGiB: p.DiskGiB, RootDiskGiB: p.RootDiskGiB,
			Runtime: colima.Runtime(p.Runtime), VMType: colima.VMType(p.VMType),
			Arch: colima.Arch(p.Arch), MountType: colima.MountType(p.MountType),
			Kubernetes: p.Kubernetes, KubernetesVersion: p.KubernetesVersion,
		},
	})
	if err != nil {
		return "", err
	}
	return sum.ID, nil
}

// colimaLimits maps the Config Service's host caps to the adapter's, so the
// creation route and the config PUT share the same ranges.
func colimaLimits(l config.Limits) colima.Limits {
	return colima.Limits{MaxCPU: l.MaxCPU, MaxMemoryGiB: l.MaxMemoryGiB, MaxDiskGiB: l.MaxDiskGiB}
}
