package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
	"github.com/supabase/cli/internal/utils"
	"golang.org/x/term"
)

var (
	passwdFolder   string
	passwdDB       bool
	passwdStudio   bool
	passwdUser     string
	passwdGenerate bool
	passwdPassword string
	passwdRestart  bool

	passwdCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "passwd",
		Short:   "Change self-hosted Supabase passwords",
		Long: `Change passwords for self-hosted Supabase instances.

This command can change:
  - Database password (POSTGRES_PASSWORD) with --db
  - Studio dashboard password (DASHBOARD_PASSWORD) with --studio

The password can be provided via:
  - Interactive prompt (recommended, most secure)
  - --generate flag (auto-generate secure password)
  - --password flag (not recommended for security reasons)

After changing passwords, you must restart the affected containers.`,
		Example: `  # Change postgres database password (interactive prompt)
  supabase passwd --folder ~/supabase/docker --db

  # Change specific database user password
  supabase passwd --folder ~/supabase/docker --db --user authenticator

  # Auto-generate a new studio password
  supabase passwd --folder ~/supabase/docker --studio --generate

  # Change both database and studio passwords
  supabase passwd --folder ~/supabase/docker --db --studio --generate

  # Non-interactive mode with automatic container restart
  supabase passwd --folder ~/supabase/docker --studio --generate --restart`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if passwdFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			if !passwdDB && !passwdStudio {
				return fmt.Errorf("must specify --db and/or --studio")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPasswd(cmd)
		},
	}
)

func init() {
	flags := passwdCmd.Flags()
	flags.StringVar(&passwdFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.BoolVar(&passwdDB, "db", false, "Change database password (POSTGRES_PASSWORD)")
	flags.BoolVar(&passwdStudio, "studio", false, "Change studio dashboard password (DASHBOARD_PASSWORD)")
	flags.StringVar(&passwdUser, "user", "postgres", "Database user to change password for")
	flags.BoolVar(&passwdGenerate, "generate", false, "Auto-generate a secure password")
	flags.StringVar(&passwdPassword, "password", "", "New password (not recommended, use prompt instead)")
	flags.BoolVar(&passwdRestart, "restart", false, "Non-interactive mode: skip prompts and restart containers automatically")

	passwdCmd.MarkFlagRequired("folder")
	passwdCmd.MarkFlagsMutuallyExclusive("generate", "password")

	rootCmd.AddCommand(passwdCmd)
}

func runPasswd(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Warn about backup
	if !passwdRestart {
		fmt.Println(utils.Yellow("Warning: You should backup your .env file before making changes."))
		fmt.Print("Continue? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Get password if not generating and not provided
	password := passwdPassword
	if !passwdGenerate && password == "" {
		var err error
		password, err = promptPassword("Enter new password: ")
		if err != nil {
			return err
		}
		confirm, err := promptPassword("Confirm new password: ")
		if err != nil {
			return err
		}
		if password != confirm {
			return fmt.Errorf("passwords do not match")
		}
	}

	opts := selfhost.PasswdOptions{
		FolderPath:   passwdFolder,
		ChangeDB:     passwdDB,
		ChangeStudio: passwdStudio,
		DBUser:       passwdUser,
		Generate:     passwdGenerate,
		Password:     password,
		SkipConfirm:  passwdRestart,
	}

	result, err := selfhost.RunPasswd(ctx, opts)
	if err != nil {
		return err
	}

	selfhost.PrintPasswdResult(result)

	// Handle container restart
	if passwdRestart {
		// Non-interactive mode: restart automatically
		if err := selfhost.RestartContainers(passwdFolder); err != nil {
			return err
		}
	} else {
		// Interactive mode: prompt user
		fmt.Print("\nWould you like to restart containers now? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) == "y" || strings.ToLower(response) == "yes" {
			if err := selfhost.RestartContainers(passwdFolder); err != nil {
				return err
			}
		}
	}

	return nil
}

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // Add newline after password input
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	return string(password), nil
}
