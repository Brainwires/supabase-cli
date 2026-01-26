package spock

import (
	"context"
	"fmt"
	"os"

	"github.com/go-errors/errors"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/supabase/cli/internal/utils"
)

func RunDisable(ctx context.Context, force bool, config pgconn.Config, options ...func(*pgx.ConnConfig)) error {
	fmt.Fprintln(os.Stderr, "Connecting to database...")
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Check if Spock extension is installed
	var installed bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'spock')
	`).Scan(&installed)
	if err != nil {
		return errors.Errorf("failed to check extension status: %w", err)
	}

	if !installed {
		fmt.Fprintln(os.Stderr, utils.Yellow("Spock extension is not installed on this database."))
		return nil
	}

	// Get status first
	status, err := GetSpockStatus(ctx, conn)
	if err != nil {
		return errors.Errorf("failed to get Spock status: %w", err)
	}

	// Check for active subscriptions
	activeSubscriptions := 0
	for _, sub := range status.Subscriptions {
		if sub.Enabled {
			activeSubscriptions++
		}
	}

	// Confirm with user (unless --force)
	if !force {
		msg := buildDisableConfirmation(status, activeSubscriptions)
		if shouldDisable, err := utils.NewConsole().PromptYesNo(ctx, msg, false); err != nil {
			return err
		} else if !shouldDisable {
			return errors.New(context.Canceled)
		}
	}

	fmt.Fprintln(os.Stderr, "Disabling Spock replication...")

	// Disable and drop all subscriptions
	if len(status.Subscriptions) > 0 {
		fmt.Fprintln(os.Stderr, "Removing subscriptions...")
		for _, sub := range status.Subscriptions {
			fmt.Fprintf(os.Stderr, "  Disabling subscription '%s'...\n", sub.SubName)
			_, err = conn.Exec(ctx, "SELECT spock.sub_disable($1, true)", sub.SubName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not disable subscription: %s\n"), err.Error())
			}

			fmt.Fprintf(os.Stderr, "  Dropping subscription '%s'...\n", sub.SubName)
			_, err = conn.Exec(ctx, "SELECT spock.sub_drop($1)", sub.SubName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop subscription: %s\n"), err.Error())
			}
		}
	}

	// Drop replication sets (except default ones)
	if len(status.ReplicationSets) > 0 {
		fmt.Fprintln(os.Stderr, "Removing replication sets...")
		for _, rs := range status.ReplicationSets {
			// Skip dropping default sets
			if rs.SetName == "default" || rs.SetName == "default_insert_only" || rs.SetName == "ddl_sql" {
				continue
			}
			fmt.Fprintf(os.Stderr, "  Dropping replication set '%s'...\n", rs.SetName)
			_, err = conn.Exec(ctx, "SELECT spock.repset_drop($1)", rs.SetName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop replication set: %s\n"), err.Error())
			}
		}
	}

	// Drop local node
	if status.LocalNode != nil {
		fmt.Fprintf(os.Stderr, "Dropping local node '%s'...\n", status.LocalNode.NodeName)
		_, err = conn.Exec(ctx, "SELECT spock.node_drop($1, true)", status.LocalNode.NodeName)
		if err != nil {
			fmt.Fprintf(os.Stderr, utils.Yellow("Warning: Could not drop local node: %s\n"), err.Error())
		}
	}

	// Drop Spock extension
	fmt.Fprintln(os.Stderr, "Dropping Spock extension...")
	_, err = conn.Exec(ctx, "DROP EXTENSION IF EXISTS spock CASCADE")
	if err != nil {
		return errors.Errorf("failed to drop Spock extension: %w", err)
	}

	// Clean up replication slots
	fmt.Fprintln(os.Stderr, "Cleaning up replication slots...")
	for _, slot := range status.Slots {
		fmt.Fprintf(os.Stderr, "  Dropping slot '%s'...\n", slot.SlotName)
		_, err = conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot.SlotName)
		if err != nil {
			fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop replication slot: %s\n"), err.Error())
		}
	}

	// Reset conflict resolution setting
	fmt.Fprintln(os.Stderr, "Resetting configuration...")
	conn.Exec(ctx, "ALTER SYSTEM RESET spock.conflict_resolution")
	conn.Exec(ctx, "SELECT pg_reload_conf()")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, utils.Green("Spock replication disabled successfully!"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Note: Sequences may still have INCREMENT BY 2. Reset manually if needed.")
	return nil
}

func buildDisableConfirmation(status *SpockStatus, activeSubscriptions int) string {
	msg := fmt.Sprintln("Do you want to disable Spock replication?")
	msg += fmt.Sprintln()
	msg += fmt.Sprintln("This will remove:")

	if status.LocalNode != nil {
		msg += fmt.Sprintf(" • Local node: %s\n", utils.Bold(status.LocalNode.NodeName))
	}

	if len(status.Subscriptions) > 0 {
		msg += fmt.Sprintf(" • %d subscription(s):\n", len(status.Subscriptions))
		for _, sub := range status.Subscriptions {
			statusIndicator := ""
			if sub.Status == "replicating" {
				statusIndicator = utils.Green(" [active]")
			}
			msg += fmt.Sprintf("   - %s%s\n", utils.Bold(sub.SubName), statusIndicator)
		}
	}

	if len(status.ReplicationSets) > 0 {
		msg += fmt.Sprintf(" • %d replication set(s)\n", len(status.ReplicationSets))
	}

	if len(status.Tables) > 0 {
		msg += fmt.Sprintf(" • %d table(s) from replication\n", len(status.Tables))
	}

	if len(status.Slots) > 0 {
		msg += fmt.Sprintf(" • %d replication slot(s)\n", len(status.Slots))
	}

	msg += fmt.Sprintln()

	if activeSubscriptions > 0 {
		msg += fmt.Sprintf("%s Active subscriptions will be terminated. Remote nodes may lose sync.\n", utils.Red("WARNING:"))
	}

	msg += fmt.Sprintf("%s This action cannot be undone. All replication configuration will be lost.", utils.Yellow("WARNING:"))

	return msg
}
