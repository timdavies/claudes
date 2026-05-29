package cmd

import (
	"os"
	"path/filepath"

	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/macuake"
)

// macuakeRegistry opens the macuake session→tab registry under the cache dir.
// The generic tab helpers in tabs.go wrap it via macuakeTabRegistry.
func macuakeRegistry() (*macuake.Registry, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return macuake.NewRegistry(filepath.Join(dir, "macuake-tabs.json")), nil
}
