package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
)

var (
	healthFolder string
	healthJSON   bool
	healthWatch  int

	healthCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "health",
		Short:   "Check health status of self-hosted Supabase services",
		Long: `Display the health status of all services in a self-hosted Supabase instance.

Shows each service's running status, health check result, uptime, and image version.`,
		Example: `  # Check health of all services
  supabase health --folder ~/supabase/docker

  # Output as JSON
  supabase health --folder ~/supabase/docker --json

  # Continuously monitor (refresh every 5 seconds)
  supabase health --folder ~/supabase/docker --watch 5`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if healthFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			if healthWatch < 0 {
				return fmt.Errorf("--watch must be a positive number")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := selfhost.HealthOptions{
				FolderPath: healthFolder,
				JSON:       healthJSON,
				Watch:      healthWatch,
			}

			// If watch mode is enabled, use the watch function
			if opts.Watch > 0 {
				return selfhost.WatchHealth(cmd.Context(), opts, os.Stdout)
			}

			// Single health check
			result, err := selfhost.RunHealth(cmd.Context(), opts)
			if err != nil {
				return err
			}

			return selfhost.PrintHealthResult(os.Stdout, result, opts.JSON)
		},
	}
)

func init() {
	flags := healthCmd.Flags()
	flags.StringVar(&healthFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.BoolVar(&healthJSON, "json", false, "Output as JSON")
	flags.IntVarP(&healthWatch, "watch", "w", 0, "Continuously monitor (refresh every N seconds)")

	healthCmd.MarkFlagRequired("folder")

	rootCmd.AddCommand(healthCmd)
}
