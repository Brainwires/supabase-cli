package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
	"github.com/supabase/cli/internal/utils"
)

var (
	restoreFolder  string
	restoreDB      string
	restoreStorage string
	restoreClean   bool
	restoreYes     bool

	restoreCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "restore",
		Short:   "Restore self-hosted Supabase database and storage from backup",
		Long: `Restore self-hosted Supabase instances from backup files.

This command can restore:
  - PostgreSQL database from pg_dump backup (--db)
  - Storage buckets from tar archive (--storage)

Compressed backups (.gz) are automatically detected and decompressed.`,
		Example: `  # Restore database
  supabase restore --folder ~/supabase/docker --db /tmp/backup.dump

  # Restore database from compressed backup
  supabase restore --folder ~/supabase/docker --db /tmp/backup.dump.gz

  # Restore storage buckets
  supabase restore --folder ~/supabase/docker --storage /tmp/storage.tar.gz

  # Restore with clean (drop existing objects first)
  supabase restore --folder ~/supabase/docker --db /tmp/backup.dump --clean

  # Restore both database and storage
  supabase restore --folder ~/supabase/docker --db /tmp/backup.dump --storage /tmp/storage.tar`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if restoreFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			if restoreDB == "" && restoreStorage == "" {
				return fmt.Errorf("must specify --db and/or --storage backup file")
			}
			// Validate backup files exist
			if restoreDB != "" {
				if _, err := os.Stat(restoreDB); os.IsNotExist(err) {
					return fmt.Errorf("database backup file not found: %s", restoreDB)
				}
			}
			if restoreStorage != "" {
				if _, err := os.Stat(restoreStorage); os.IsNotExist(err) {
					return fmt.Errorf("storage backup file not found: %s", restoreStorage)
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Warn about destructive operation
			if !restoreYes {
				fmt.Println(utils.Yellow("Warning: This operation will overwrite existing data."))
				if restoreClean {
					fmt.Println(utils.Red("The --clean flag is set: existing objects will be dropped first."))
				}
				fmt.Print("Continue? [y/N]: ")
				var response string
				fmt.Scanln(&response)
				if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			opts := selfhost.RestoreOptions{
				FolderPath:        restoreFolder,
				DBBackupFile:      restoreDB,
				StorageBackupFile: restoreStorage,
				Clean:             restoreClean,
			}

			result, err := selfhost.RunRestore(cmd.Context(), opts)
			if err != nil {
				return err
			}

			selfhost.PrintRestoreResult(result)
			return nil
		},
	}
)

func init() {
	flags := restoreCmd.Flags()
	flags.StringVar(&restoreFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.StringVar(&restoreDB, "db", "", "Database backup file to restore")
	flags.StringVar(&restoreStorage, "storage", "", "Storage backup file to restore")
	flags.BoolVar(&restoreClean, "clean", false, "Drop existing objects before restore")
	flags.BoolVarP(&restoreYes, "yes", "y", false, "Skip confirmation prompt")

	restoreCmd.MarkFlagRequired("folder")

	rootCmd.AddCommand(restoreCmd)
}
