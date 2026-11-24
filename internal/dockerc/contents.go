package dockerc

import (
	"context"
	"errors"
)

// Contents is how many Docker objects a profile's engine holds. Counts only:
// it carries no name and no id, so it is safe to hand to a browser.
type Contents struct {
	Containers int // every state, running or not
	Images     int
	Volumes    int
}

// Empty reports whether the engine holds nothing countable.
func (c Contents) Empty() bool { return c.Containers == 0 && c.Images == 0 && c.Volumes == 0 }

// contentsCounter is implemented by the real client: three plain list calls
// (containers of every state, images, volumes) and nothing else.
type contentsCounter interface {
	countContents(ctx context.Context) (Contents, error)
}

// CountContents counts containers (all states), images and volumes. It fails
// on the first error and returns no partial count, so a caller that refuses on
// error fails closed. The real client makes exactly three Docker calls:
// GET /containers/json?all=1, GET /images/json and GET /volumes. Intermediate
// image layers and build cache are not counted. Another Client (a test fake)
// is counted through its own list methods.
func CountContents(ctx context.Context, c Client) (Contents, error) {
	if cc, ok := c.(contentsCounter); ok {
		return cc.countContents(ctx)
	}
	cs, err := c.Containers(ctx)
	if err != nil {
		return Contents{}, err
	}
	imgs, err := c.Images(ctx)
	if err != nil {
		return Contents{}, err
	}
	vols, err := c.Volumes(ctx)
	if err != nil {
		return Contents{}, err
	}
	return Contents{Containers: len(cs), Images: len(imgs), Volumes: len(vols)}, nil
}

// ProfileContents resolves the profile's Docker client and counts its
// contents. applicable is false (and err nil) when the profile runs another
// runtime. ErrVMNotRunning and ErrDockerUnavailable pass through.
func ProfileContents(ctx context.Context, p Provider, profile string) (c Contents, applicable bool, err error) {
	client, err := p.For(ctx, profile)
	switch {
	case errors.Is(err, ErrRuntimeNotDocker):
		return Contents{}, false, nil
	case err != nil:
		return Contents{}, false, err
	}
	c, err = CountContents(ctx, client)
	if err != nil {
		return Contents{}, false, err
	}
	return c, true, nil
}
