// Package web embeds the built frontend (web/dist) into the Go binary.
package web

import (
	"embed"
	"io/fs"
)

// The all: prefix lets the tracked, empty dist/.gitkeep satisfy the pattern on
// a clean checkout, so go vet, go test and go build work before any frontend
// build. Without a build the handler serves a 503 pointing at `make build`.
//
//go:embed all:dist
var dist embed.FS

// UI returns the built frontend rooted at dist.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: "dist" is a constant that the embed directive guarantees.
		panic(err)
	}
	return sub
}
