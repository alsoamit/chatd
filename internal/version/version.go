// Package version exposes build metadata to every chatd binary.
// Version and Commit are overridden at build time via -ldflags
// "-X github.com/cedrx/chatd/internal/version.Version=v0.1.0 ...".
package version

import (
	"fmt"
	"runtime"
)

var (
	Version = "dev"
	Commit  = "unknown"
)

// String returns a single-line version banner including the platform
// and Go runtime.
func String(name string) string {
	return fmt.Sprintf("%s %s (commit %s, %s/%s, %s)",
		name, Version, Commit, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
