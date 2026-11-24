package colima

import "context"

// KubernetesStart runs `colima kubernetes start -p <profile>`.
func KubernetesStart(ctx context.Context, s Streamer, profile string) (Process, error) {
	return kubernetes(ctx, s, "start", profile)
}

// KubernetesStop runs `colima kubernetes stop -p <profile>`.
func KubernetesStop(ctx context.Context, s Streamer, profile string) (Process, error) {
	return kubernetes(ctx, s, "stop", profile)
}

// KubernetesReset runs `colima kubernetes reset -p <profile>`. It destroys the
// cluster's workloads and state and creates a fresh cluster.
func KubernetesReset(ctx context.Context, s Streamer, profile string) (Process, error) {
	return kubernetes(ctx, s, "reset", profile)
}

// KubernetesDelete runs `colima kubernetes delete -p <profile>`. It destroys
// the cluster's workloads and state and removes the cluster.
func KubernetesDelete(ctx context.Context, s Streamer, profile string) (Process, error) {
	return kubernetes(ctx, s, "delete", profile)
}

// kubernetes is only called with the four fixed verbs above, never with text
// from a caller. The profile is validated before any process starts.
func kubernetes(ctx context.Context, s Streamer, verb, profile string) (Process, error) {
	if err := ValidateProfile(profile); err != nil {
		return nil, err
	}
	return stream(ctx, s, []string{"kubernetes", verb, "-p", profile})
}
