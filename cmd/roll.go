package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
	"github.com/supabase/cli/internal/utils"
)

var (
	rollFolder        string
	rollIncludeSecret bool
	rollRestart       bool

	rollCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "roll",
		Short:   "Regenerate self-hosted Supabase API keys",
		Long: `Regenerate JWT-based API keys for self-hosted Supabase instances.

This command regenerates:
  - ANON_KEY (public API key for client-side access)
  - SERVICE_ROLE_KEY (admin API key for server-side access)

With --include-secret, it also regenerates:
  - JWT_SECRET (invalidates ALL existing tokens!)

The new keys are generated using the current (or new) JWT_SECRET and
will have a 10-year expiration.

After rolling keys, you must:
  1. Restart all containers
  2. Update your applications with the new keys`,
		Example: `  # Roll API keys only (keeps JWT_SECRET)
  supabase roll --folder ~/supabase/docker

  # Full rotation including JWT_SECRET (invalidates all tokens!)
  supabase roll --folder ~/supabase/docker --include-secret

  # Non-interactive mode with automatic container restart
  supabase roll --folder ~/supabase/docker --restart`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if rollFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoll(cmd)
		},
	}
)

func init() {
	flags := rollCmd.Flags()
	flags.StringVar(&rollFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.BoolVar(&rollIncludeSecret, "include-secret", false, "Also regenerate JWT_SECRET (invalidates ALL existing tokens)")
	flags.BoolVar(&rollRestart, "restart", false, "Non-interactive mode: skip prompts and restart containers automatically")

	rollCmd.MarkFlagRequired("folder")

	rootCmd.AddCommand(rollCmd)
}

func runRoll(cmd *cobra.Command) error {
	// Warn about backup and consequences
	if !rollRestart {
		fmt.Println(utils.Yellow("Warning: You should backup your .env file before making changes."))
		if rollIncludeSecret {
			fmt.Println(utils.Red("WARNING: --include-secret will invalidate ALL existing JWT tokens!"))
			fmt.Println(utils.Red("         All users will need to re-authenticate."))
		}
		fmt.Print("Continue? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	opts := selfhost.RollOptions{
		FolderPath:    rollFolder,
		IncludeSecret: rollIncludeSecret,
		SkipConfirm:   rollRestart,
	}

	result, err := selfhost.RunRoll(opts)
	if err != nil {
		return err
	}

	selfhost.PrintRollResult(result)

	// Handle container restart
	if rollRestart {
		// Non-interactive mode: restart automatically
		if err := selfhost.RestartContainers(rollFolder); err != nil {
			return err
		}
	} else {
		// Interactive mode: prompt user
		fmt.Print("\nWould you like to restart containers now? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) == "y" || strings.ToLower(response) == "yes" {
			if err := selfhost.RestartContainers(rollFolder); err != nil {
				return err
			}
		}
	}

	return nil
}
