package colima

import (
	"context"
	"fmt"
)

// run executes one fixed argument vector and converts the outcome into the
// package's typed errors. Cancellation is reported as a wrapped ctx.Err().
func run(ctx context.Context, r Runner, args []string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("colima: %w", err)
	}
	res, err := r.Run(ctx, args)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return res, fmt.Errorf("colima: %w", cerr)
		}
		return res, err
	}
	if res.ExitCode != 0 {
		return res, &ExitError{Args: args, ExitCode: res.ExitCode, Stderr: string(res.Stderr)}
	}
	return res, nil
}
