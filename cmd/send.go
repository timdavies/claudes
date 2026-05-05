package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var sendCmd = &cobra.Command{
	Use:   "send [name] <message>",
	Short: "Send a message to a session",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		client := newClient(cfg)

		var name, message string
		if len(args) == 2 {
			name, message = args[0], args[1]
		} else {
			// One arg = message, picker for name
			message = args[0]
			s, err := pickSession(client, cfg, nil)
			if err != nil {
				return err
			}
			if s == nil {
				return nil
			}
			name = s.Name
		}
		full := session.FullName(cfg.Prefix, name)
		if err := client.SendKeys(full, message); err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sendCmd)
}
