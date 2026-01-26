package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/selfhost"
)

var (
	backupFolder     string
	backupDB         bool
	backupStorage    bool
	backupOutput     string
	backupCompress   bool
	backupDataOnly   bool
	backupSchemaOnly bool
	backupFull       bool

	backupCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "backup",
		Short:   "Backup self-hosted Supabase database and storage",
		Long: `Create backups of self-hosted Supabase instances.

This command can backup:
  - PostgreSQL database using pg_dump (--db)
  - Storage buckets as tar archive (--storage)

Backup types:
  - Standard (default): Schema and data only (pg_dump)
  - Full (--full): Includes roles, permissions, settings, extensions, and data

Use --full for complete disaster recovery backups that can restore to a fresh database.
Standard backups are faster and suitable for data migration between existing instances.`,
		Example: `  # Standard database backup (schema + data)
  supabase backup --folder ~/supabase/docker --db

  # Full database backup (roles, settings, extensions, schema, data)
  supabase backup --folder ~/supabase/docker --db --full

  # Full backup with compression
  supabase backup --folder ~/supabase/docker --db --full --compress

  # Backup storage buckets
  supabase backup --folder ~/supabase/docker --storage

  # Complete backup (database + storage)
  supabase backup --folder ~/supabase/docker --db --storage --full --compress

  # Backup to specific output file
  supabase backup --folder ~/supabase/docker --db --output /backups/mybackup.dump

  # Schema only (no data)
  supabase backup --folder ~/supabase/docker --db --schema-only

  # Data only (no schema)
  supabase backup --folder ~/supabase/docker --db --data-only`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if backupFolder == "" {
				return fmt.Errorf("--folder is required")
			}
			if !backupDB && !backupStorage {
				return fmt.Errorf("must specify --db and/or --storage")
			}
			if backupDataOnly && backupSchemaOnly {
				return fmt.Errorf("--data-only and --schema-only are mutually exclusive")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := selfhost.BackupOptions{
				FolderPath:    backupFolder,
				BackupDB:      backupDB,
				BackupStorage: backupStorage,
				Output:        backupOutput,
				Compress:      backupCompress,
				DataOnly:      backupDataOnly,
				SchemaOnly:    backupSchemaOnly,
				Full:          backupFull,
			}

			result, err := selfhost.RunBackup(cmd.Context(), opts)
			if err != nil {
				return err
			}

			selfhost.PrintBackupResult(result)
			return nil
		},
	}
)

func init() {
	flags := backupCmd.Flags()
	flags.StringVar(&backupFolder, "folder", "", "Path to docker folder containing .env (required)")
	flags.BoolVar(&backupDB, "db", false, "Backup database")
	flags.BoolVar(&backupStorage, "storage", false, "Backup storage buckets")
	flags.StringVarP(&backupOutput, "output", "o", "", "Output file path")
	flags.BoolVar(&backupCompress, "compress", false, "Compress output with gzip")
	flags.BoolVar(&backupDataOnly, "data-only", false, "Only backup data, no schema")
	flags.BoolVar(&backupSchemaOnly, "schema-only", false, "Only backup schema, no data")
	flags.BoolVar(&backupFull, "full", false, "Full backup including roles, settings, and extensions")

	backupCmd.MarkFlagRequired("folder")

	rootCmd.AddCommand(backupCmd)
}
