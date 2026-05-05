package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:       "completion [bash|zsh|fish]",
	Short:     "Generate shell completion script",
	Hidden:    true,
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	ValidArgs: []string{"bash", "zsh", "fish"},
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}

// sessionNameCompletion returns the names of running claudes sessions.
func sessionNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	client := newClient(cfg)
	infos, err := client.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var names []string
	for _, i := range infos {
		if len(i.Name) >= len(cfg.Prefix) && i.Name[:len(cfg.Prefix)] == cfg.Prefix {
			names = append(names, i.Name[len(cfg.Prefix):])
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	openCmd.ValidArgsFunction = sessionNameCompletion
	stopCmd.ValidArgsFunction = sessionNameCompletion
	killCmd.ValidArgsFunction = sessionNameCompletion
	sendCmd.ValidArgsFunction = sessionNameCompletion
	logsCmd.ValidArgsFunction = sessionNameCompletion
}
