// Package dockerctest is a fake dockerc.Client and dockerc.Provider for tests
// of code that talks to Docker, so no test needs a Docker socket.
//
// The Fake records every call in order, with its arguments, and can be told to
// fail a method. The write routes of the Docker manager (issues #117 to #120)
// use it to assert that a rejected request made no Docker call at all, and
// that an accepted one made exactly the expected call with the expected
// arguments.
//
// Adding a write method to dockerc.Client breaks the build of this package
// (see the assertion below) until the Fake gets the method too. The new method
// should do nothing but return f.record("MethodName", args...), so it is
// recorded and can be failed like the others.
package dockerctest

import (
	"context"
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

func (f *Fake) Containers(context.Context) ([]dockerc.Container, error) {
	return f.ContainerList, f.record("Containers")
}

func (f *Fake) Stats(_ context.Context, id string) (dockerc.Reading, error) {
	return dockerc.Reading{}, f.record("Stats", id)
}

func (f *Fake) DiskUsage(context.Context) (dockerc.DiskUsage, error) {
	return f.Disk, f.record("DiskUsage")
}

func (f *Fake) Info(context.Context) (dockerc.Engine, error) {
	return f.EngineInfo, f.record("Info")
}

func (f *Fake) Images(context.Context) ([]dockerc.Image, error) {
	return f.ImageList, f.record("Images")
}

func (f *Fake) Volumes(context.Context) ([]dockerc.Volume, error) {
	return f.VolumeList, f.record("Volumes")
}

func (f *Fake) Networks(context.Context) ([]dockerc.Network, error) {
	return f.NetworkList, f.record("Networks")
}

func (f *Fake) Logs(_ context.Context, id string, tail int) (dockerc.LogResult, error) {
	return f.LogResult, f.record("Logs", id, tail)
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
