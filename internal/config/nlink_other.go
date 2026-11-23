//go:build !unix

package config

import "io/fs"

// hardLinked cannot be determined on this platform; assume a single link.
func hardLinked(fs.FileInfo) bool { return false }
