package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/timdavies/claudes/internal/session"
)

var (
	logsLines  int
	logsFollow bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [name]",
	Short: "View recent output from a session",
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
		full := session.FullName(cfg.Prefix, target.Name)

		out, err := client.CapturePane(full, logsLines)
		if err != nil {
			return err
		}
		fmt.Print(out)

		if !logsFollow {
			return nil
		}

		// Follow: pipe-pane to a fifo and tail.
		dir, err := os.MkdirTemp("", "claudes-logs-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		fifo := filepath.Join(dir, "pipe")
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			return err
		}

		if err := client.PipePaneStart(full, fifo); err != nil {
			return err
		}
		defer client.PipePaneStop(full)

		f, err := os.OpenFile(fifo, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			<-sigCh
			close(done)
			f.Close()
		}()

		r := bufio.NewReader(f)
		for {
			select {
			case <-done:
				return nil
			default:
			}
			line, err := r.ReadString('\n')
			if line != "" {
				fmt.Print(line)
			}
			if err == io.EOF {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err != nil {
				return nil
			}
		}
	},
}

func init() {
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 50, "Number of lines to show")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow output")
	rootCmd.AddCommand(logsCmd)
}
