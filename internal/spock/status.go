package spock

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/supabase/cli/internal/utils"
)

type SpockStatus struct {
	Installed       bool
	Version         string
	LocalNode       *NodeInfo
	ReplicationSets []ReplicationSet
	Subscriptions   []Subscription
	Tables          []ReplicatedTable
	Slots           []ReplicationSlot
	Conflicts       int64
}

type NodeInfo struct {
	NodeID   int64
	NodeName string
	DSN      string
}

type ReplicationSet struct {
	SetID   int64
	SetName string
}

type Subscription struct {
	SubID        int64
	SubName      string
	ProviderDSN  string
	Enabled      bool
	Status       string
	ReceiverPID  *int32
	ReplayLSN    string
}

type ReplicatedTable struct {
	SetName   string
	Schema    string
	TableName string
}

type ReplicationSlot struct {
	SlotName    string
	Plugin      string
	Active      bool
	RestartLSN  string
	ConfirmedLSN string
}

func RunStatus(ctx context.Context, config pgconn.Config, options ...func(*pgx.ConnConfig)) error {
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	status, err := GetSpockStatus(ctx, conn)
	if err != nil {
		return err
	}

	printStatus(status)
	return nil
}

func GetSpockStatus(ctx context.Context, conn *pgx.Conn) (*SpockStatus, error) {
	status := &SpockStatus{}

	// Check if Spock extension is installed
	var extExists bool
	err := conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'spock')
	`).Scan(&extExists)
	if err != nil {
		return nil, err
	}
	status.Installed = extExists

	if !extExists {
		return status, nil
	}

	// Get Spock version
	err = conn.QueryRow(ctx, `SELECT spock.spock_version()`).Scan(&status.Version)
	if err != nil {
		status.Version = "unknown"
	}

	// Get local node info
	status.LocalNode, _ = getLocalNode(ctx, conn)

	// Get replication sets
	status.ReplicationSets, _ = getReplicationSets(ctx, conn)

	// Get subscriptions with status
	status.Subscriptions, _ = getSubscriptions(ctx, conn)

	// Get replicated tables
	status.Tables, _ = getReplicatedTables(ctx, conn)

	// Get replication slots
	status.Slots, _ = getReplicationSlots(ctx, conn)

	// Get conflict count
	status.Conflicts, _ = getConflictCount(ctx, conn)

	return status, nil
}

func getLocalNode(ctx context.Context, conn *pgx.Conn) (*NodeInfo, error) {
	var node NodeInfo
	err := conn.QueryRow(ctx, `
		SELECT n.node_id, n.node_name,
		       COALESCE(ni.if_dsn, '') as dsn
		FROM spock.local_node ln
		JOIN spock.node n ON ln.node_id = n.node_id
		LEFT JOIN spock.node_interface ni ON ln.node_local_interface = ni.if_id
	`).Scan(&node.NodeID, &node.NodeName, &node.DSN)
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func getReplicationSets(ctx context.Context, conn *pgx.Conn) ([]ReplicationSet, error) {
	rows, err := conn.Query(ctx, `
		SELECT set_id, set_name FROM spock.replication_set ORDER BY set_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sets []ReplicationSet
	for rows.Next() {
		var s ReplicationSet
		if err := rows.Scan(&s.SetID, &s.SetName); err != nil {
			continue
		}
		sets = append(sets, s)
	}
	return sets, nil
}

func getSubscriptions(ctx context.Context, conn *pgx.Conn) ([]Subscription, error) {
	// Use sub_show_status() function to get full subscription info
	rows, err := conn.Query(ctx, `
		SELECT subscription_name, status, provider_dsn
		FROM spock.sub_show_status()
	`)
	if err != nil {
		// Fallback to basic subscription table query
		rows, err = conn.Query(ctx, `
			SELECT s.sub_name, 'unknown', COALESCE(ni.if_dsn, '')
			FROM spock.subscription s
			LEFT JOIN spock.node_interface ni ON s.sub_origin_if = ni.if_id
			ORDER BY s.sub_name
		`)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var subs []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.SubName, &s.Status, &s.ProviderDSN); err != nil {
			continue
		}
		s.Enabled = true // If it shows up in sub_show_status, it's enabled
		subs = append(subs, s)
	}
	return subs, nil
}

func getReplicatedTables(ctx context.Context, conn *pgx.Conn) ([]ReplicatedTable, error) {
	rows, err := conn.Query(ctx, `
		SELECT set_name, nspname, relname
		FROM spock.tables
		ORDER BY set_name, nspname, relname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []ReplicatedTable
	for rows.Next() {
		var t ReplicatedTable
		if err := rows.Scan(&t.SetName, &t.Schema, &t.TableName); err != nil {
			continue
		}
		tables = append(tables, t)
	}
	return tables, nil
}

func getReplicationSlots(ctx context.Context, conn *pgx.Conn) ([]ReplicationSlot, error) {
	rows, err := conn.Query(ctx, `
		SELECT slot_name, plugin, active,
		       COALESCE(restart_lsn::text, 'N/A'),
		       COALESCE(confirmed_flush_lsn::text, 'N/A')
		FROM pg_replication_slots
		WHERE slot_name LIKE 'spk%' OR plugin = 'spock_output'
		ORDER BY slot_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var slots []ReplicationSlot
	for rows.Next() {
		var s ReplicationSlot
		if err := rows.Scan(&s.SlotName, &s.Plugin, &s.Active, &s.RestartLSN, &s.ConfirmedLSN); err != nil {
			continue
		}
		slots = append(slots, s)
	}
	return slots, nil
}

func getConflictCount(ctx context.Context, conn *pgx.Conn) (int64, error) {
	var count int64
	err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(conflict_count), 0)
		FROM spock.stat_subscription
	`).Scan(&count)
	if err != nil {
		// Table might not exist in older versions
		return 0, nil
	}
	return count, nil
}

func printStatus(status *SpockStatus) {
	fmt.Println()
	fmt.Println(utils.Bold("=== Spock Replication Status ==="))
	fmt.Println()

	// Installation status
	if !status.Installed {
		fmt.Println(utils.Yellow("Spock extension is NOT installed"))
		fmt.Println()
		fmt.Println("To enable Spock, run:")
		fmt.Println("  supabase spock enable --db-url <connection-string>")
		return
	}

	fmt.Printf("%s %s\n", utils.Green("Spock Version:"), status.Version)
	fmt.Println()

	// Local Node
	fmt.Println(utils.Bold("Local Node:"))
	if status.LocalNode != nil {
		fmt.Printf("  Node ID:   %d\n", status.LocalNode.NodeID)
		fmt.Printf("  Node Name: %s\n", status.LocalNode.NodeName)
		if status.LocalNode.DSN != "" {
			fmt.Printf("  DSN:       %s\n", maskPassword(status.LocalNode.DSN))
		}
	} else {
		fmt.Println(utils.Yellow("  No local node configured"))
	}
	fmt.Println()

	// Replication Sets
	fmt.Println(utils.Bold("Replication Sets:"))
	if len(status.ReplicationSets) == 0 {
		fmt.Println(utils.Yellow("  No replication sets found"))
	} else {
		for _, rs := range status.ReplicationSets {
			fmt.Printf("  - %s (ID: %d)\n", rs.SetName, rs.SetID)
		}
	}
	fmt.Println()

	// Subscriptions
	fmt.Println(utils.Bold("Subscriptions:"))
	if len(status.Subscriptions) == 0 {
		fmt.Println(utils.Yellow("  No subscriptions found"))
	} else {
		for _, sub := range status.Subscriptions {
			enabledStr := utils.Red("disabled")
			if sub.Enabled {
				enabledStr = utils.Green("enabled")
			}
			statusStr := sub.Status
			if sub.Status == "replicating" {
				statusStr = utils.Green(sub.Status)
			} else if sub.Status == "down" || sub.Status == "unknown" {
				statusStr = utils.Red(sub.Status)
			} else {
				statusStr = utils.Yellow(sub.Status)
			}
			fmt.Printf("  - %s [%s] status=%s\n", sub.SubName, enabledStr, statusStr)
			fmt.Printf("    Provider: %s\n", maskPassword(sub.ProviderDSN))
		}
	}
	fmt.Println()

	// Replicated Tables
	fmt.Println(utils.Bold("Replicated Tables:"))
	if len(status.Tables) == 0 {
		fmt.Println(utils.Yellow("  No tables in replication sets"))
	} else {
		tablesBySet := make(map[string][]string)
		for _, t := range status.Tables {
			tablesBySet[t.SetName] = append(tablesBySet[t.SetName], fmt.Sprintf("%s.%s", t.Schema, t.TableName))
		}
		for setName, tables := range tablesBySet {
			fmt.Printf("  [%s] %d tables\n", setName, len(tables))
			// Show first 5 tables
			for i, t := range tables {
				if i >= 5 {
					fmt.Printf("    ... and %d more\n", len(tables)-5)
					break
				}
				fmt.Printf("    - %s\n", t)
			}
		}
	}
	fmt.Println()

	// Replication Slots
	fmt.Println(utils.Bold("Replication Slots:"))
	if len(status.Slots) == 0 {
		fmt.Println(utils.Yellow("  No Spock replication slots found"))
	} else {
		for _, slot := range status.Slots {
			activeStr := utils.Red("inactive")
			if slot.Active {
				activeStr = utils.Green("active")
			}
			fmt.Printf("  - %s [%s]\n", slot.SlotName, activeStr)
			fmt.Printf("    Restart LSN: %s, Confirmed: %s\n", slot.RestartLSN, slot.ConfirmedLSN)
		}
	}
	fmt.Println()

	// Conflict count
	if status.Conflicts > 0 {
		fmt.Printf("%s %d\n", utils.Yellow("Conflicts detected:"), status.Conflicts)
	} else {
		fmt.Printf("%s %d\n", utils.Green("Conflicts:"), status.Conflicts)
	}
	fmt.Println()
}

func maskPassword(dsn string) string {
	// Simple password masking for display
	if strings.Contains(dsn, "password=") {
		parts := strings.Split(dsn, " ")
		for i, part := range parts {
			if strings.HasPrefix(part, "password=") {
				parts[i] = "password=***"
			}
		}
		return strings.Join(parts, " ")
	}
	// Handle URL format
	if strings.Contains(dsn, "://") && strings.Contains(dsn, "@") {
		// postgresql://user:pass@host -> postgresql://user:***@host
		atIdx := strings.LastIndex(dsn, "@")
		schemeEnd := strings.Index(dsn, "://")
		if schemeEnd != -1 && atIdx > schemeEnd {
			userPass := dsn[schemeEnd+3 : atIdx]
			if colonIdx := strings.Index(userPass, ":"); colonIdx != -1 {
				return dsn[:schemeEnd+3] + userPass[:colonIdx+1] + "***" + dsn[atIdx:]
			}
		}
	}
	return dsn
}
