package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

// networksTimeout bounds the whole request.
var networksTimeout = 15 * time.Second

// networkJSON is one entry of the networks response.
type networkJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Internal   bool   `json:"internal"`
	Containers int    `json:"containers"`
}

// networkDetailJSON is the inspect answer. It has no options, labels or other
// container fields, and no free text from the engine: subnets and addresses
// are re-parsed, container names are plain names or short ids.
type networkDetailJSON struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Driver     string                 `json:"driver"`
	Scope      string                 `json:"scope"`
	Internal   bool                   `json:"internal"`
	Subnets    []networkSubnetJSON    `json:"subnets"`
	Containers []networkContainerJSON `json:"containers"`
}

type networkSubnetJSON struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}

type networkContainerJSON struct {
	Name        string `json:"name"`
	IPv4Address string `json:"ipv4Address"`
	IPv6Address string `json:"ipv6Address"`
}

// networkPruneJSON is the prune answer. RemovedCount is null, and
// ResultComplete false, when the prune ran but Docker's answer could not be
// read; the networks are gone anyway. Truncated is true when Removed lists
// fewer names than RemovedCount.
type networkPruneJSON struct {
	Removed        []string `json:"removed"`
	RemovedCount   *int     `json:"removedCount"`
	Truncated      bool     `json:"truncated"`
	ResultComplete bool     `json:"resultComplete"`
}

// networkPruneConfirmation is the fixed string a network prune must carry.
const networkPruneConfirmation = "delete-unused-networks"

const (
	// createNetworkBodyLimit is far above a name, a flag and a subnet.
	createNetworkBodyLimit = 4 << 10
	invalidSubnetMessage   = "Not an allowed subnet: use a CIDR such as 172.28.0.0/16 (IPv4 /8 to /28, IPv6 /48 to /120) outside loopback, link-local, multicast and reserved ranges."
)

// WithNetworks registers the network routes:
//
//	GET    /api/profiles/{name}/networks
//	POST   /api/profiles/{name}/networks
//	GET    /api/profiles/{name}/networks/{network}
//	DELETE /api/profiles/{name}/networks/{network}
//	POST   /api/profiles/{name}/networks/prune
//
// The list only reads. The other four go through the shared Docker write rules
// (dockerwrite.go): busy check, strict body, validation, action log row (inspect
// is a read: no busy check and no row). The busy lookup is required; a nil
// audit skips the row.
func WithNetworks(gate ProfileGate, docker dockerc.Provider, busy ConfigBusy, audit ActionRecorder) Option {
	r := &networksRoutes{gate: gate, docker: docker, w: newDockerWriter(gate, docker, busy, audit)}
	return func(rt *Router) {
		rt.Handle("GET /api/profiles/{name}/networks", r.list)
		rt.Handle("POST /api/profiles/{name}/networks", r.create)
		rt.Handle("GET /api/profiles/{name}/networks/{network}", r.inspect)
		rt.Handle("DELETE /api/profiles/{name}/networks/{network}", r.remove)
		rt.Handle("POST /api/profiles/{name}/networks/prune", r.prune)
	}
}

type networksRoutes struct {
	gate   ProfileGate
	docker dockerc.Provider
	w      *dockerWriter
}

// create: POST /api/profiles/{name}/networks, body
// {"name": "...", "internal": false, "subnet": "172.28.0.0/16"}. Only the bridge
// driver exists. subnet is optional (absent or null: Docker chooses); host bits
// are ignored (see dockerc.NormalizeSubnet).
func (n *networksRoutes) create(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name     string
		Internal bool
		Subnet   *string
	}
	var out networkJSON
	return n.w.serve(w, r, writeSpec{
		Action: "docker.network.create", BodyLimit: createNetworkBodyLimit,
		Body: map[string]any{"name": &in.Name, "internal": &in.Internal, "subnet": &in.Subnet},
		Plan: func() (writePlan, error) {
			name, err := requireObjectName("name", in.Name)
			if err != nil {
				return writePlan{}, err
			}
			subnet := ""
			if in.Subnet != nil {
				norm, ok := dockerc.NormalizeSubnet(*in.Subnet)
				if !ok {
					return writePlan{}, errInvalidRequest(map[string]string{"subnet": invalidSubnetMessage})
				}
				subnet = norm
			}
			internal := in.Internal
			return writePlan{
				Object: name, Status: http.StatusCreated, Result: &out,
				Run: func(ctx context.Context, c dockerc.Client) error {
					nw, err := c.CreateNetwork(ctx, name, internal, subnet)
					if err != nil {
						return err
					}
					out = networkJSON{
						ID: nw.ID, Name: nw.Name, Driver: nw.Driver, Scope: nw.Scope,
						Internal: nw.Internal, Containers: nw.Containers,
					}
					return nil
				},
			}, nil
		},
	})
}

// inspect: GET /api/profiles/{name}/networks/{network}. A read: no busy check
// and no action log row.
func (n *networksRoutes) inspect(w http.ResponseWriter, r *http.Request) error {
	var out networkDetailJSON
	return n.w.serve(w, r, writeSpec{
		Action: "docker.network.inspect", ReadOnly: true,
		Plan: func() (writePlan, error) {
			ref, err := requireObjectName("network", r.PathValue("network"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{
				Object: ref, Result: &out,
				Run: func(ctx context.Context, c dockerc.Client) error {
					d, err := c.InspectNetwork(ctx, ref)
					if err != nil {
						return err
					}
					out = networkDetailJSON{
						ID: d.ID, Name: d.Name, Driver: d.Driver, Scope: d.Scope, Internal: d.Internal,
						Subnets:    make([]networkSubnetJSON, 0, len(d.Subnets)),
						Containers: make([]networkContainerJSON, 0, len(d.Containers)),
					}
					for _, s := range d.Subnets {
						out.Subnets = append(out.Subnets, networkSubnetJSON{Subnet: s.Subnet, Gateway: s.Gateway})
					}
					for _, ct := range d.Containers {
						out.Containers = append(out.Containers, networkContainerJSON{
							Name: ct.Name, IPv4Address: ct.IPv4Address, IPv6Address: ct.IPv6Address,
						})
					}
					return nil
				},
			}, nil
		},
	})
}

// remove: DELETE /api/profiles/{name}/networks/{network}. Never forced; the
// predefined networks and networks with containers are refused by dockerc.
func (n *networksRoutes) remove(w http.ResponseWriter, r *http.Request) error {
	return n.w.serve(w, r, writeSpec{
		Action: "docker.network.remove",
		Plan: func() (writePlan, error) {
			ref, err := requireObjectName("network", r.PathValue("network"))
			if err != nil {
				return writePlan{}, err
			}
			return writePlan{Object: ref, Run: func(ctx context.Context, c dockerc.Client) error {
				return c.RemoveNetwork(ctx, ref)
			}}, nil
		},
	})
}

// prune: POST /api/profiles/{name}/networks/prune. Removing networks cannot be
// undone, so the body must be {"confirm":"delete-unused-networks"}: an empty
// body, a missing key or any other string is a 422 naming confirm and Docker is
// not reached. No filters exist.
func (n *networksRoutes) prune(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Confirm string }
	var out networkPruneJSON
	return n.w.serve(w, r, writeSpec{
		Action: "docker.network.prune", Timeout: pruneRouteTimeout, EmptyBodyIsObject: true,
		BodyLimit: 1 << 10, Body: map[string]any{"confirm": &in.Confirm},
		Plan: func() (writePlan, error) {
			if in.Confirm != networkPruneConfirmation {
				return writePlan{}, errInvalidRequest(map[string]string{
					"confirm": "Send the confirmation text to delete all unused networks.",
				})
			}
			return writePlan{Result: &out, Run: func(ctx context.Context, c dockerc.Client) error {
				res, err := c.PruneNetworks(ctx)
				if err != nil {
					return err
				}
				removed := res.Removed
				if removed == nil {
					removed = []string{}
				}
				out = networkPruneJSON{
					Removed: removed, RemovedCount: res.RemovedCount, Truncated: res.Truncated,
					ResultComplete: res.Complete,
				}
				return nil
			}}, nil
		},
	})
}

// list checks the profile through the gate first, because the provider cannot
// tell a stopped VM from an unknown profile.
func (v *networksRoutes) list(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), networksTimeout)
	defer cancel()

	gctx, gcancel := context.WithTimeout(ctx, gateTimeout)
	status, err := v.gate.ProfileStatus(gctx, name)
	gcancel()
	switch {
	case errors.Is(err, ErrInvalidProfileName):
		return profileNotFound("")
	case errors.Is(err, ErrProfileNotFound):
		return profileNotFound(name)
	case err != nil:
		slog.Error("profile lookup failed", "route", "networks", "error", err)
		return errColimaFailed()
	}
	if status = strings.ToLower(status); status != "running" {
		return notRunning(status)
	}

	client, err := v.docker.For(ctx, name)
	switch {
	case errors.Is(err, dockerc.ErrVMNotRunning):
		return notRunning("stopped")
	case errors.Is(err, dockerc.ErrRuntimeNotDocker):
		writeJSON(w, http.StatusOK, map[string][]networkJSON{"networks": {}})
		return nil
	case err != nil:
		return dockerError("docker client", err)
	}

	list, err := client.Networks(ctx)
	if err != nil {
		return dockerError("list networks", err)
	}
	out := make([]networkJSON, 0, len(list))
	for _, x := range list {
		out = append(out, networkJSON{
			ID: x.ID, Name: x.Name, Driver: x.Driver, Scope: x.Scope,
			Internal: x.Internal, Containers: x.Containers,
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Name != out[b].Name {
			return out[a].Name < out[b].Name
		}
		return out[a].ID < out[b].ID
	})
	writeJSON(w, http.StatusOK, map[string][]networkJSON{"networks": out})
	return nil
}
