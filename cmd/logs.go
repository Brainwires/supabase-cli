package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
)

var (
	logsFolder   string
	logsServices []string
	logsFollow   bool
	logsSince    string
	logsTail     int

	logsCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "logs",
		Short:   "View logs from self-hosted Supabase services",
		Long: `View and follow logs from self-hosted Supabase services.

Valid services: db, kong, auth, rest, realtime, storage, imgproxy, meta, functions, analytics, studio, vector, pooler`,
		Example: `  # View logs from database service
  supabase logs --folder ~/supabase/docker -s db

  # View logs from multiple services
  supabase logs --folder ~/supabase/docker -s auth -s kong

  # Follow logs (like tail -f)
  supabase logs --folder ~/supabase/docker -s db -f

  # Show logs from the last 10 minutes
  supabase logs --folder ~/supabase/docker -s auth --since 10m

  # Show logs from a specific time
  supabase logs --folder ~/supabase/docker -s db --since 2024-01-01

  # Show last 50 lines
  supabase logs --folder ~/supabase/docker -s db -n 50

  # View all service logs
  supabase logs --folder ~/supabase/docker`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if logsFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			// Validate services
			for _, svc := range logsServices {
				if !selfhost.IsValidService(svc) {
					return fmt.Errorf("invalid service: %s. Valid services: %s", svc, strings.Join(selfhost.ValidServices, ", "))
				}
			}
			if logsTail < 0 {
				return fmt.Errorf("--tail must be a positive number")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := selfhost.LogsOptions{
				FolderPath: logsFolder,
				Services:   logsServices,
				Follow:     logsFollow,
				Since:      logsSince,
				Tail:       logsTail,
			}

			return selfhost.RunLogs(cmd.Context(), opts, os.Stdout)
		},
	}
)

func init() {
	flags := logsCmd.Flags()
	flags.StringVar(&logsFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.StringArrayVarP(&logsServices, "service", "s", nil, "Filter by service (can be specified multiple times)")
	flags.BoolVarP(&logsFollow, "follow", "f", false, "Follow logs (tail -f style)")
	flags.StringVar(&logsSince, "since", "", "Show logs since (e.g., '10m', '1h', '2024-01-01')")
	flags.IntVarP(&logsTail, "tail", "n", 100, "Number of lines to show")

	logsCmd.MarkFlagRequired("folder")

	rootCmd.AddCommand(logsCmd)
}
