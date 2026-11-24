// Package dockerctest is a fake dockerc.Client and dockerc.Provider for tests
// of code that talks to Docker, so no test needs a Docker socket.
//
// The Fake records every call in order, with its arguments, and can be told to
// fail a method. The write routes of the Docker manager (issues #117 to #120)
// use it to assert that a rejected request made no Docker call at all, and
// that an accepted one made exactly the expected call with the expected
// arguments.
//
// Every dockerc write method must validate every argument itself, before any
// request: names with ValidObjectName, references with ValidImageRef, and every
// value that ends up in a query string or path (tag targets, fromImage, ...),
// returning ErrBadName for an invalid one. Each method needs a test against the
// real client (a unix socket server, as in dockerc/write_test.go) that sends
// hostile values and checks that no request reaches the socket. The Fake mirrors
// the rule: its RecordObject and RecordRef validate like the real client, so a
// route that forgot to validate fails in the route's test too.
//
// Adding a write method to dockerc.Client breaks the build of this package
// (see the assertion below) until the Fake gets the method too. The new method
// should do nothing but return f.record("MethodName", args...), so it is
// recorded and can be failed like the others.
package dockerctest

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/dlusignan/colima-ui/internal/dockerc"
)

var (
	_ dockerc.Client   = (*Fake)(nil)
	_ dockerc.Provider = (*Provider)(nil)
)

// Call is one recorded call. Method is the Client method name; Args are its
// arguments after the context.
type Call struct {
	Method string
	Args   []any
}

// Fake implements dockerc.Client. The exported data fields are what the read
// methods return; set them before use, they are not guarded. Calls, CallsTo,
// Reset and FailWith are safe for concurrent use.
type Fake struct {
	ContainerList []dockerc.Container
	ImageList     []dockerc.Image
	VolumeList    []dockerc.Volume
	NetworkList   []dockerc.Network
	EngineInfo    dockerc.Engine
	Disk          dockerc.DiskUsage
	LogResult     dockerc.LogResult

	// Results of the volume write methods.
	CreatedVolume dockerc.Volume
	VolumeDetail  dockerc.VolumeDetail
	PruneResult   dockerc.PruneResult

	// Results of the network write methods.
	CreatedNetwork dockerc.Network
	NetworkDetail  dockerc.NetworkDetail
	NetworkPrune   dockerc.NetworkPruneResult

	// Results of the image write methods.
	ImageDetail  dockerc.ImageDetail
	ImageRemoval dockerc.ImageRemoval
	ImagePrune   dockerc.ImagePruneResult

	// PullLines are emitted by PullImage, in order, before it returns. PullFunc,
	// when set, replaces that: it receives the context and the emit function, so
	// a test can block until cancelled, fail, panic or emit hostile lines. Set
	// both before use; they are not guarded.
	PullLines []string
	PullFunc  func(ctx context.Context, emit func(string)) error

	mu    sync.Mutex
	calls []Call
	errs  map[string]error
}

// FailWith makes every later call of method return err; a nil err clears it.
func (f *Fake) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errs, method)
		return
	}
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[method] = err
}

// Calls returns a copy of every recorded call, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// CallsTo returns the recorded calls of one method.
func (f *Fake) CallsTo(method string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Reset forgets the recorded calls. Injected failures stay.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// record stores the call and returns the failure set for the method, if any.
func (f *Fake) record(method string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Method: method, Args: args})
	return f.errs[method]
}

// zeroOnErr returns v, or the zero value when err is set, like the real client.
func zeroOnErr[T any](v T, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

func nilOnErr[T any](v []T, err error) ([]T, error) { return zeroOnErr(v, err) }

// RecordObject is what a write method taking an object name calls: it records
// the call (with the name first in Args, then extra), and returns ErrBadName
// when name fails dockerc.ValidObjectName, as the real client would before
// making any request, else the failure set for the method.
func (f *Fake) RecordObject(method, name string, extra ...any) error {
	err := f.record(method, append([]any{name}, extra...)...)
	if !dockerc.ValidObjectName(name) {
		return dockerc.ErrBadName
	}
	return err
}

// RecordRef is RecordObject for an image reference (dockerc.ValidImageRef).
func (f *Fake) RecordRef(method, ref string, extra ...any) error {
	err := f.record(method, append([]any{ref}, extra...)...)
	if !dockerc.ValidImageRef(ref) {
		return dockerc.ErrBadName
	}
	return err
}

func (f *Fake) Containers(context.Context) ([]dockerc.Container, error) {
	return nilOnErr(f.ContainerList, f.record("Containers"))
}

func (f *Fake) Stats(_ context.Context, id string) (dockerc.Reading, error) {
	return dockerc.Reading{}, f.record("Stats", id)
}

func (f *Fake) DiskUsage(context.Context) (dockerc.DiskUsage, error) {
	return zeroOnErr(f.Disk, f.record("DiskUsage"))
}

func (f *Fake) Info(context.Context) (dockerc.Engine, error) {
	return zeroOnErr(f.EngineInfo, f.record("Info"))
}

func (f *Fake) Images(context.Context) ([]dockerc.Image, error) {
	return nilOnErr(f.ImageList, f.record("Images"))
}

func (f *Fake) Volumes(context.Context) ([]dockerc.Volume, error) {
	return nilOnErr(f.VolumeList, f.record("Volumes"))
}

func (f *Fake) Networks(context.Context) ([]dockerc.Network, error) {
	return nilOnErr(f.NetworkList, f.record("Networks"))
}

func (f *Fake) Logs(_ context.Context, id string, tail int) (dockerc.LogResult, error) {
	return zeroOnErr(f.LogResult, f.record("Logs", id, tail))
}

// CreateVolume validates like the real client: the name and the labels, else
// ErrBadName. The labels are recorded as a copy.
func (f *Fake) CreateVolume(_ context.Context, name string, labels map[string]string) (dockerc.Volume, error) {
	err := f.RecordObject("CreateVolume", name, maps.Clone(labels))
	if err == nil && !dockerc.ValidLabels(labels) {
		err = dockerc.ErrBadName
	}
	return zeroOnErr(f.CreatedVolume, err)
}

func (f *Fake) InspectVolume(_ context.Context, name string) (dockerc.VolumeDetail, error) {
	return zeroOnErr(f.VolumeDetail, f.RecordObject("InspectVolume", name))
}

func (f *Fake) RemoveVolume(_ context.Context, name string) error {
	return f.RecordObject("RemoveVolume", name)
}

func (f *Fake) PruneVolumes(context.Context) (dockerc.PruneResult, error) {
	return zeroOnErr(f.PruneResult, f.record("PruneVolumes"))
}

// CreateNetwork validates like the real client: the name and a non-empty
// subnet, else ErrBadName.
func (f *Fake) CreateNetwork(_ context.Context, name string, internal bool, subnet string) (dockerc.Network, error) {
	err := f.RecordObject("CreateNetwork", name, internal, subnet)
	if err == nil && dockerc.ReservedNetworkName(name) {
		err = dockerc.ErrBadName
	}
	if err == nil && subnet != "" {
		if _, ok := dockerc.NormalizeSubnet(subnet); !ok {
			err = dockerc.ErrBadName
		}
	}
	return zeroOnErr(f.CreatedNetwork, err)
}

func (f *Fake) InspectNetwork(_ context.Context, ref string) (dockerc.NetworkDetail, error) {
	return zeroOnErr(f.NetworkDetail, f.RecordObject("InspectNetwork", ref))
}

func (f *Fake) RemoveNetwork(_ context.Context, ref string) error {
	return f.RecordObject("RemoveNetwork", ref)
}

func (f *Fake) PruneNetworks(context.Context) (dockerc.NetworkPruneResult, error) {
	return zeroOnErr(f.NetworkPrune, f.record("PruneNetworks"))
}

// TagImage validates like the real client: the reference, the repository and
// the tag, else ErrBadName.
func (f *Fake) TagImage(_ context.Context, ref, repository, tag string) error {
	err := f.RecordRef("TagImage", ref, repository, tag)
	if err == nil && !dockerc.ValidImageTarget(repository, tag) {
		err = dockerc.ErrBadName
	}
	return err
}

func (f *Fake) InspectImage(_ context.Context, ref string) (dockerc.ImageDetail, error) {
	return zeroOnErr(f.ImageDetail, f.RecordRef("InspectImage", ref))
}

func (f *Fake) RemoveImage(_ context.Context, ref string) (dockerc.ImageRemoval, error) {
	return zeroOnErr(f.ImageRemoval, f.RecordRef("RemoveImage", ref))
}

func (f *Fake) PruneImages(_ context.Context, all bool) (dockerc.ImagePruneResult, error) {
	return zeroOnErr(f.ImagePrune, f.record("PruneImages", all))
}

// PullImage validates like the real client (ValidPullRef: a reference with a
// tag or a digest, else ErrBadName, before anything is emitted), records the
// call, then runs PullFunc or emits PullLines.
func (f *Fake) PullImage(ctx context.Context, ref string, emit func(string)) error {
	err := f.record("PullImage", ref)
	if !dockerc.ValidPullRef(ref) {
		return dockerc.ErrBadName
	}
	if err != nil {
		return err
	}
	if f.PullFunc != nil {
		return f.PullFunc(ctx, emit)
	}
	for _, l := range f.PullLines {
		emit(l)
	}
	return nil
}

// Provider implements dockerc.Provider. It records the profile of every For
// call and returns Client, or Err when set.
type Provider struct {
	Client dockerc.Client
	Err    error

	mu       sync.Mutex
	profiles []string
}

// For records the profile and returns Err, or Client.
func (p *Provider) For(_ context.Context, profile string) (dockerc.Client, error) {
	p.mu.Lock()
	p.profiles = append(p.profiles, profile)
	p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	return p.Client, nil
}

// Profiles returns the profile names For was called with, oldest first.
func (p *Provider) Profiles() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.profiles)
}
