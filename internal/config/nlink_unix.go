//go:build unix

package config

import (
	"io/fs"
	"syscall"
)

// hardLinked reports whether the file has more than one name.
func hardLinked(st fs.FileInfo) bool {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(sys.Nlink) > 1
	}
	return false
}
