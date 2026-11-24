package colima

import (
	"context"
	"errors"
)

// Start runs `colima start -p <profile>` plus the flags in opts. Profile and
// options are validated before any process starts. Colima's --save-config
// default is left on, so options persist to the profile's YAML file.
func Start(ctx context.Context, s Streamer, profile string, opts StartOptions, lim Limits) (Process, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	if err := opts.Validate(lim); err != nil {
		return nil, err
	}
	return stream(ctx, s, append([]string{"start", "-p", profile}, opts.args()...))
}

// Stop runs `colima stop -p <profile>`, with --force when force is set.
func Stop(ctx context.Context, s Streamer, profile string, force bool) (Process, error) {
	return stopLike(ctx, s, "stop", profile, force)
}

// Restart runs `colima restart -p <profile>`, with --force when force is set.
// Colima's restart takes no start flags; applying new resources is a stop
// followed by a Start, which is the caller's decision.
func Restart(ctx context.Context, s Streamer, profile string, force bool) (Process, error) {
	return stopLike(ctx, s, "restart", profile, force)
}

// Update runs `colima update -p <profile>`, which refreshes the container
// runtime inside a running VM. It takes no other arguments.
func Update(ctx context.Context, s Streamer, profile string) (Process, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	return stream(ctx, s, []string{"update", "-p", profile})
}

func stopLike(ctx context.Context, s Streamer, verb, profile string, force bool) (Process, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	args := []string{verb, "-p", profile}
	if force {
		args = append(args, "--force")
	}
	return stream(ctx, s, args)
}

func stream(ctx context.Context, s Streamer, args []string) (Process, error) {
	if s == nil {
		return nil, errors.New("colima: no streamer")
	}
	return s.Stream(ctx, args)
}
