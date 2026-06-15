package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var prClear bool

var prCmd = &cobra.Command{
	Use:   "pr [url-or-number]",
	Short: "Attach a pull request to this session (shown in 'claudes ls')",
	Long: `Attach a GitHub pull request to the current session so it shows up beside the
model in 'claudes ls' and the dashboard. In the TUI, arrow right onto the #ID
and press enter to open it in your browser.

  claudes pr                       # auto-detect the PR for the current branch (gh)
  claudes pr 123                   # resolve PR #123 in the session's repo (gh)
  claudes pr https://github.com/o/r/pull/123   # attach an explicit URL
  claudes pr --clear               # detach the PR

Targets the session you're running inside (same resolution as 'claudes whoami').`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		name := currentSessionName(cfg)
		if name == "" {
			return fmt.Errorf("not inside a claudes session")
		}
		client := newClient(cfg)
		full := session.FullName(cfg.Prefix, name)

		if prClear {
			_ = client.UnsetSessionEnv(full, "@claudes-pr")
			fmt.Printf("%s: pr cleared\n", name)
			return nil
		}

		arg := ""
		if len(args) == 1 {
			arg = strings.TrimSpace(args[0])
		}
		url, err := resolvePRURL(arg)
		if err != nil {
			return err
		}
		if err := client.SetSessionEnv(full, "@claudes-pr", url); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", name, url)
		return nil
	},
}

// resolvePRURL turns the command argument into a canonical PR URL:
//   - a full http(s) URL is taken as-is;
//   - a bare number or empty arg is resolved through `gh pr view`, which reads
//     the number (or the current branch's PR when empty) in the session's cwd.
func resolvePRURL(arg string) (string, error) {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		return arg, nil
	}
	ghArgs := []string{"pr", "view", "--json", "url", "--jq", ".url"}
	if arg != "" {
		ghArgs = []string{"pr", "view", arg, "--json", "url", "--jq", ".url"}
	}
	out, err := exec.Command("gh", ghArgs...).Output()
	if err != nil {
		if arg == "" {
			return "", fmt.Errorf("no PR found for the current branch (pass a URL or number): %w", err)
		}
		return "", fmt.Errorf("could not resolve PR %q via gh: %w", arg, err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("gh returned no PR url")
	}
	return url, nil
}

func init() {
	prCmd.Flags().BoolVar(&prClear, "clear", false, "Detach the PR from this session")
	rootCmd.AddCommand(prCmd)
}
