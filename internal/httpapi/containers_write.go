package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// containerActionTimeout bounds one start, stop or restart request. Docker
// waits out the container's grace period before it kills it, so this is longer
// than the default write deadline.
const containerActionTimeout = 60 * time.Second

const invalidContainerIDMessage = "Not a valid container id."

// containerActionJSON is the answer of a container action. Changed is false
// when Docker said the container was already in the asked state (304).
type containerActionJSON struct {
	Changed bool `json:"changed"`
}

// WithContainerActions registers the container write routes:
//
//	POST /api/profiles/{name}/containers/{id}/start
//	POST /api/profiles/{name}/containers/{id}/stop
//	POST /api/profiles/{name}/containers/{id}/restart
//
// They go through the shared Docker write rules (dockerwrite.go): profile
// gate, advisory busy check, an empty body only, validation of the id, one
// fixed Docker call and one action log row. The busy lookup is required; a nil
// audit skips the row.
func WithContainerActions(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, audit ActionRecorder) Option {
	r := &containerActionRoutes{w: newDockerWriter(gate, docker, busy, audit)}
	return func(rt *Router) {
		rt.Handle("POST /api/profiles/{name}/containers/{id}/start", r.start)
		rt.Handle("POST /api/profiles/{name}/containers/{id}/stop", r.stop)
		rt.Handle("POST /api/profiles/{name}/containers/{id}/restart", r.restart)
	}
}

type containerActionRoutes struct{ w *dockerWriter }

func (c *containerActionRoutes) start(w http.ResponseWriter, r *http.Request) error {
	return c.act(w, r, "docker.container.start", dockerc.Client.StartContainer)
}

func (c *containerActionRoutes) stop(w http.ResponseWriter, r *http.Request) error {
	return c.act(w, r, "docker.container.stop", dockerc.Client.StopContainer)
}

func (c *containerActionRoutes) restart(w http.ResponseWriter, r *http.Request) error {
	return c.act(w, r, "docker.container.restart", dockerc.Client.RestartContainer)
}

// act runs one container verb. The id is the only value taken from the
// request; it must be 12 to 64 lowercase hex characters, so nothing else can
// reach the log row, the Docker URL or an error answer. The row records the
// short id and whether anything changed, false for a 304 and for a failure.
func (c *containerActionRoutes) act(w http.ResponseWriter, r *http.Request, action string,
	do func(dockerc.Client, context.Context, string) (bool, error)) error {
	var out containerActionJSON
	return c.w.serve(w, r, writeSpec{
		Action: action, Timeout: containerActionTimeout, EmptyBodyOnly: true,
		Plan: func() (writePlan, error) {
			id := r.PathValue("id")
			if !dockerc.ValidContainerID(id) {
				return writePlan{}, errInvalidRequest(map[string]string{"id": invalidContainerIDMessage})
			}
			short := id[:12]
			return writePlan{
				Object: id,
				Result: &out,
				Run: func(ctx context.Context, cl dockerc.Client) error {
					changed, err := do(cl, ctx, id)
					out.Changed = err == nil && changed
					return err
				},
				AuditDetailAfter: func(error) string {
					if out.Changed {
						return short + " changed=true"
					}
					return short + " changed=false"
				},
			}, nil
		},
	})
}
