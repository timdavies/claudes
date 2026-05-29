package cmd

import (
	"os"
	"path/filepath"

	"github.com/timdavies/claudes/internal/daemon"
	"github.com/timdavies/claudes/internal/iterm2"
)

// iterm2Registry opens the iTerm2 session→tab registry under the cache dir.
// The generic tab helpers in tabs.go wrap it via iterm2TabRegistry.
func iterm2Registry() (*iterm2.Registry, error) {
	dir, err := daemon.CacheDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return iterm2.NewRegistry(filepath.Join(dir, "iterm2-tabs.json")), nil
}
