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
// `colima start -p <new name>` creates a profile and the API has no create
// route, so an unknown name must never get through.
type profileGate struct {
	runner colima.Runner
	files  profileLister // nil when no config folder is known
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
