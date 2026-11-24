package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dlusignan/colima-ui/internal/colima"
	"github.com/dlusignan/colima-ui/internal/config"
	"github.com/dlusignan/colima-ui/internal/httpapi"
)

// profileLister lists the profiles that have a config file.
// *config.Service satisfies it.
type profileLister interface{ ListProfiles() ([]string, error) }

// profileGate is the production httpapi.ProfileGate. It decides which names
// the operation routes may pass to colima. That matters because
// `colima start -p <new name>` creates a profile, so an unknown name must never
// get through those routes; creation has its own route and checks (ProfileExists).
type profileGate struct {
	runner  colima.Runner
	files   profileLister // nil when no config folder is known
	folders folderChecker // nil when no config folder is known
}

// folderChecker reports whether the Colima folder holds an entry for a name in
// any letter case. *config.Service satisfies it.
type folderChecker interface {
	ProfileFolderExists(name string) (bool, error)
}

// ProfileExists reports whether a creation must treat name as taken: it is in
// `colima list`, or the Colima folder holds an entry for it, in either case
// comparing names ignoring letter case (the default macOS filesystem does).
// Unlike ProfileStatus, "default" is not special: it exists only if listed or
// it has a folder. The name must pass both validators first.
func (g profileGate) ProfileExists(ctx context.Context, name string) (bool, error) {
	if colima.ValidateProfile(name) != nil || config.ValidateProfileName(name) != nil {
		return false, httpapi.ErrInvalidProfileName
	}
	list, err := colima.List(ctx, g.runner)
	if err != nil {
		return false, fmt.Errorf("list profiles: %w", err)
	}
	for _, in := range list {
		if strings.EqualFold(in.Name, name) {
			return true, nil
		}
	}
	if g.folders != nil {
		taken, err := g.folders.ProfileFolderExists(name)
		if err != nil {
			return false, fmt.Errorf("scan profile folders: %w", err)
		}
		return taken, nil
	}
	return false, nil
}

// ProfileStatus implements httpapi.ProfileGate.
//
// A name must pass both validators: they differ (the config one also rejects
// ".." inside a name, which the colima one allows). A profile reported by
// `colima list` takes its status from there, lowercased. A profile that only
// has a config file counts as stopped. The "default" profile always exists:
// a first-run user has neither a VM nor a YAML file for it.
func (g profileGate) ProfileStatus(ctx context.Context, name string) (string, error) {
	if colima.ValidateProfile(name) != nil || config.ValidateProfileName(name) != nil {
		return "", httpapi.ErrInvalidProfileName
	}
	list, err := colima.List(ctx, g.runner)
	if err != nil {
		return "", fmt.Errorf("list profiles: %w", err)
	}
	for _, in := range list {
		if in.Name == name {
			return strings.ToLower(in.Status), nil
		}
	}
	if g.files != nil {
		names, err := g.files.ListProfiles()
		if err != nil {
			return "", fmt.Errorf("list profile files: %w", err)
		}
		for _, n := range names {
			if n == name {
				return "stopped", nil
			}
		}
	}
	if name == colima.DefaultProfile {
		return "stopped", nil
	}
	return "", httpapi.ErrProfileNotFound
}

// ProfileStatusForDelete implements httpapi.DeleteGate. A delete is the one
// action that must not guess: the name has to be listed by `colima list` or
// have an entry in Colima's folder, and "default" gets no special treatment.
// Names are compared ignoring letter case, because on the default macOS
// filesystem `-p Dev` addresses the folder of `dev`; a listed profile that
// differs only in case reports its own status, so a running VM is never
// mistaken for a stopped one.
func (g profileGate) ProfileStatusForDelete(ctx context.Context, name string) (string, error) {
	if colima.ValidateProfile(name) != nil || config.ValidateProfileName(name) != nil {
		return "", httpapi.ErrInvalidProfileName
	}
	list, err := colima.List(ctx, g.runner)
	if err != nil {
		return "", fmt.Errorf("list profiles: %w", err)
	}
	for _, in := range list {
		if in.Name == name {
			return strings.ToLower(in.Status), nil
		}
	}
	for _, in := range list {
		if strings.EqualFold(in.Name, name) {
			return strings.ToLower(in.Status), nil
		}
	}
	if g.files != nil {
		names, err := g.files.ListProfiles()
		if err != nil {
			return "", fmt.Errorf("list profile files: %w", err)
		}
		for _, n := range names {
			if strings.EqualFold(n, name) {
				return "stopped", nil
			}
		}
	}
	if g.folders != nil {
		found, err := g.folders.ProfileFolderExists(name)
		if err != nil {
			return "", fmt.Errorf("scan profile folders: %w", err)
		}
		if found {
			return "stopped", nil
		}
	}
	return "", httpapi.ErrProfileNotFound
}
