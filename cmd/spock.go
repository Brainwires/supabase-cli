package cmd

import (
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/supabase/cli/internal/spock"
	"github.com/supabase/cli/internal/utils/flags"
)

var (
	spockCmd = &cobra.Command{
		GroupID: groupLocalDev,
		Use:     "spock",
		Short:   "Manage Spock bi-directional replication",
		Long: `Manage Spock bi-directional PostgreSQL replication.

Spock is a PostgreSQL extension that enables multi-master replication,
allowing writes on multiple nodes simultaneously with automatic conflict
resolution.

The Supabase CLI automatically detects Spock when connecting to a database
and wraps DDL statements in spock.replicate_ddl() for proper replication.`,
	}

	spockStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show detailed Spock replication status",
		Long: `Display detailed information about Spock replication configuration.

Shows:
  - Spock extension version
  - Local node information
  - Replication sets and their tables
  - Active subscriptions and their status
  - Replication slots
  - Conflict statistics`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return spock.RunStatus(cmd.Context(), flags.DbConfig)
		},
	}

	// Enable options
	spockNodeName        string
	spockNodeOffset      int
	spockReplicationSets []string

	spockEnableCmd = &cobra.Command{
		Use:   "enable",
		Short: "Enable Spock replication on a database",
		Long: `Enable Spock bi-directional replication on the specified database.

This command will:
  1. Create the Spock extension
  2. Create a local node with the specified name
  3. Create replication sets (default, ddl_sql)
  4. Configure conflict resolution to 'last_update_wins'
  5. Optionally configure sequences for bi-directional inserts

After enabling, you need to:
  1. Enable Spock on the remote node with a different node name and offset
  2. Create subscriptions between nodes
  3. Add tables to replication sets`,
		Example: `  # Enable on primary node (odd IDs)
  supabase spock enable --node-name primary --node-offset 1 \
    --db-url "postgresql://postgres:pass@localhost:5432/postgres"

  # Enable on standby node (even IDs)
  supabase spock enable --node-name standby --node-offset 2 \
    --db-url "postgresql://postgres:pass@standby:5432/postgres"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := spock.EnableOptions{
				NodeName:        spockNodeName,
				NodeOffset:      spockNodeOffset,
				ReplicationSets: spockReplicationSets,
			}
			return spock.RunEnable(cmd.Context(), flags.DbConfig, opts)
		},
	}

	spockForce bool

	spockDisableCmd = &cobra.Command{
		Use:   "disable",
		Short: "Disable Spock replication on a database",
		Long: `Disable Spock bi-directional replication on the specified database.

This command will:
  1. Disable and drop all subscriptions
  2. Remove tables from replication sets
  3. Drop custom replication sets
  4. Drop the local node
  5. Drop the Spock extension
  6. Clean up replication slots

WARNING: This will stop all replication and remove configuration.
Use --force to skip confirmation prompts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return spock.RunDisable(cmd.Context(), spockForce, flags.DbConfig)
		},
	}
)

func init() {
	// Status command flags
	statusFlags := spockStatusCmd.Flags()
	statusFlags.String("db-url", "", "Database connection string (must be percent-encoded).")
	statusFlags.Bool("linked", false, "Check status on the linked project.")
	statusFlags.Bool("local", true, "Check status on the local database.")
	spockStatusCmd.MarkFlagsMutuallyExclusive("db-url", "linked", "local")
	spockCmd.AddCommand(spockStatusCmd)

	// Enable command flags
	enableFlags := spockEnableCmd.Flags()
	enableFlags.StringVar(&spockNodeName, "node-name", "primary", "Name for this Spock node (e.g., primary, standby)")
	enableFlags.IntVar(&spockNodeOffset, "node-offset", 0, "Sequence offset: 1 for primary (odd IDs), 2 for standby (even IDs). 0 to skip sequence configuration.")
	enableFlags.StringSliceVar(&spockReplicationSets, "replication-sets", []string{"default", "ddl_sql"}, "Replication sets to create")
	enableFlags.String("db-url", "", "Database connection string (must be percent-encoded).")
	enableFlags.Bool("linked", false, "Enable on the linked project.")
	enableFlags.Bool("local", true, "Enable on the local database.")
	spockEnableCmd.MarkFlagsMutuallyExclusive("db-url", "linked", "local")
	spockCmd.AddCommand(spockEnableCmd)

	// Disable command flags
	disableFlags := spockDisableCmd.Flags()
	disableFlags.BoolVar(&spockForce, "force", false, "Force disable without confirmation (drops active subscriptions)")
	disableFlags.String("db-url", "", "Database connection string (must be percent-encoded).")
	disableFlags.Bool("linked", false, "Disable on the linked project.")
	disableFlags.Bool("local", true, "Disable on the local database.")
	spockDisableCmd.MarkFlagsMutuallyExclusive("db-url", "linked", "local")
	spockCmd.AddCommand(spockDisableCmd)

	rootCmd.AddCommand(spockCmd)
}

// Ensure flags package is imported for ParseDatabaseConfig
var _ = afero.NewOsFs()
