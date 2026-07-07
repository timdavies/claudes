package cmd

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/config"
	"github.com/timdavies/claudes/internal/picker"
	"github.com/timdavies/claudes/internal/pinned"
	"github.com/timdavies/claudes/internal/session"
	"github.com/timdavies/claudes/internal/tmux"
)

var archiveCmd = &cobra.Command{
	Use:   "archive [name]",
	Short: "Park a session: stop its process but keep it restorable with 'claudes unarchive'",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)
		target, err := pickSession(client, cfg, args)
		if err != nil {
			return err
		}
		if target == nil {
			return nil
		}
		if err := archiveSession(client, cfg, target); err != nil {
			return err
		}
		fmt.Println(target.Name)
		return nil
	},
}

var unarchiveCmd = &cobra.Command{
	Use:   "unarchive [name]",
	Short: "Restore an archived session, reattaching its conversation",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)
		reg, err := pinnedRegistry()
		if err != nil {
			return err
		}
		name, err := resolveArchivedName(reg, args)
		if err != nil {
			return err
		}
		if name == "" {
			return nil // picker cancelled
		}
		if err := unarchiveSession(client, cfg, reg, name); err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	},
}

var archiveLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List archived sessions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		reg, err := pinnedRegistry()
		if err != nil {
			return err
		}
		entries := archivedEntries(reg)
		if len(entries) == 0 {
			fmt.Println("no archived sessions")
			return nil
		}
		for _, ae := range entries {
			line := ae.name
			if ae.entry.Group != "" {
				line += "  [" + ae.entry.Group + "]"
			}
			if ae.entry.PR != "" {
				line += "  " + ae.entry.PR
			}
			fmt.Println(line)
		}
		return nil
	},
}

func init() {
	archiveCmd.AddCommand(archiveLsCmd)
	rootCmd.AddCommand(archiveCmd)
	rootCmd.AddCommand(unarchiveCmd)
}

// archiveSession persists the target's full state to the pin registry with the
// archived flag, then stops its live process. A session already paused/pinned
// is archived in place (just flip the flag).
func archiveSession(client *tmux.Client, cfg *config.Config, target *session.Session) error {
	reg, err := pinnedRegistry()
	if err != nil {
		return err
	}
	full := session.FullName(cfg.Prefix, target.Name)
	live, _ := client.Has(full)
	if !live {
		if reg.Has(target.Name) {
			return reg.SetArchived(target.Name, true)
		}
		return fmt.Errorf("agent %q is not running and not pinned; nothing to archive", target.Name)
	}

	// Snapshot everything needed to resurrect with context from the live env.
	env, _ := client.SessionEnv(full)
	entry := pinned.Entry{
		Project:         env["CLAUDES_PROJECT"],
		Model:           env["CLAUDES_MODEL"],
		Dir:             env["CLAUDES_DIR"],
		Group:           session.NormalizeGroup(env["CLAUDES_GROUP"]),
		DefaultArgs:     decodeJSONStrings(env["CLAUDES_DEFAULT_ARGS"]),
		PassthroughArgs: decodeJSONStrings(env["CLAUDES_PASSTHROUGH"]),
		SessionID:       env["CLAUDES_SESSION_ID"],
		PR:              env["@claudes-pr"],
		Description:     firstNonEmpty(env["@claudes-self-description"], env["@claudes-description"]),
		Archived:        true,
		ArchivedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if prev, ok := reg.Get(target.Name); ok {
		entry.Order = prev.Order // keep its place in the pinned block
	}
	if entry.Dir == "" {
		entry.Dir = target.Dir
	}
	if err := reg.Set(target.Name, entry); err != nil {
		return err
	}
	return stopResolved(client, cfg, target, false)
}

// unarchiveSession clears the archived flag and resurrects the session.
// resurrectPin auto-appends `--resume <SessionID>`, so the conversation reloads.
func unarchiveSession(client *tmux.Client, cfg *config.Config, reg *pinned.Registry, name string) error {
	entry, ok := reg.Get(name)
	if !ok || !entry.Archived {
		return fmt.Errorf("no archived agent named %q", name)
	}
	full := session.FullName(cfg.Prefix, name)
	if has, _ := client.Has(full); has {
		// Already running somehow — just clear the flag so it rejoins the roster.
		return reg.SetArchived(name, false)
	}
	if err := reg.SetArchived(name, false); err != nil {
		return err
	}
	return resurrectPin(client, cfg, name, false)
}

type archivedEntry struct {
	name  string
	entry pinned.Entry
}

func archivedEntries(reg *pinned.Registry) []archivedEntry {
	var out []archivedEntry
	for name, e := range reg.All() {
		if e.Archived {
			out = append(out, archivedEntry{name: name, entry: e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// resolveArchivedName returns the archived agent to act on: the name arg if
// given, else a picker over archived entries. "" (nil error) means cancelled.
func resolveArchivedName(reg *pinned.Registry, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	entries := archivedEntries(reg)
	if len(entries) == 0 {
		return "", errors.New("no archived agents")
	}
	sessions := make([]session.Session, len(entries))
	for i, ae := range entries {
		sessions[i] = session.Session{
			Name: ae.name, Project: ae.entry.Project, Model: ae.entry.Model,
			Dir: ae.entry.Dir, Group: ae.entry.Group, Status: session.StatusPaused,
		}
	}
	chosen, err := picker.Pick(sessions)
	if err != nil {
		if errors.Is(err, picker.ErrCancelled) {
			return "", nil
		}
		return "", err
	}
	return chosen.Name, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
