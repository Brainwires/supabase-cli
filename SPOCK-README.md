# Spock Bi-Directional Replication Support

This document describes the Supabase CLI's integration with [Spock](https://github.com/pgEdge/spock), a PostgreSQL extension for multi-master bi-directional replication.

## Overview

The Supabase CLI automatically detects when a database has Spock replication enabled and handles DDL (Data Definition Language) statements appropriately. This allows migrations and schema changes to be replicated across all nodes in a Spock cluster.

## How It Works

### Automatic Spock Detection

The CLI detects Spock by querying the connected database:

```sql
SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'spock')
   AND EXISTS (SELECT 1 FROM spock.local_node)
```

This means:
- The CLI works with **both** Spock and non-Spock databases
- No configuration file changes are needed
- Detection happens at runtime based on the actual database state

### DDL Replication

Spock requires DDL statements to be wrapped in `spock.replicate_ddl()` for proper replication. The CLI handles this automatically:

**Before (what you write):**
```sql
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL
);
```

**After (what the CLI executes):**
```sql
SELECT spock.replicate_ddl(
    $spock_ddl$
    CREATE TABLE users (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL
    );
    $spock_ddl$,
    ARRAY['default', 'ddl_sql']
);
```

The CLI also automatically adds new tables to the replication set.

## Commands with Spock Support

### `supabase db exec`

Execute arbitrary SQL with automatic Spock DDL wrapping.

```bash
# DDL statement (automatically wrapped for Spock)
supabase db exec \
  --sql "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT)" \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"

# Execute from file
supabase db exec \
  --file schema.sql \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"

# Pipe from stdin
cat schema.sql | supabase db exec \
  --file - \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"
```

### `supabase migration up`

Apply pending migrations with Spock replication.

```bash
supabase migration up \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"
```

### `supabase migration down`

Revert migrations with Spock replication.

```bash
supabase migration down \
  --last 1 \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"
```

### `supabase db push`

Push migrations to remote database with Spock support.

```bash
supabase db push \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"
```

### `supabase db reset`

Reset database with Spock replication.

```bash
supabase db reset \
  --db-url "postgresql://postgres:password@localhost:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:password@standby:5432/postgres?sslmode=disable"
```

## The `--spock-remote-dsn` Flag

When connecting to a Spock-enabled database, you **must** provide the `--spock-remote-dsn` flag:

| Scenario | `--spock-remote-dsn` Required? |
|----------|-------------------------------|
| Database has Spock enabled | **Yes** - CLI will error without it |
| Database without Spock | No - flag is ignored |

The remote DSN should point to another node in the Spock cluster. This connection is used to:
1. Verify replication is working
2. Ensure DDL changes are visible on the remote node

### Connection String Format

```
postgresql://username:password@hostname:port/database?sslmode=disable
```

**Important:** When using Cloudflare tunnels or other proxies, use `sslmode=disable` as TLS is handled by the tunnel.

## Configuration (config.toml)

Optional Spock settings can be configured in `config.toml`:

```toml
[db.spock]
# Replication sets to use (default: ["default", "ddl_sql"])
replication_sets = ["default", "ddl_sql"]

# Default replication set for new tables (default: "default")
default_rep_set = "default"

# Automatically add new tables to replication set (default: true)
auto_add_tables = true

# Node offset for sequence ID generation (default: 1)
# Use 1 for primary (odd IDs), 2 for standby (even IDs)
node_offset = 1

# Maximum wait attempts for replication sync (default: 30)
max_wait_attempts = 30

# Base delay between wait attempts in ms (default: 100)
base_wait_delay_ms = 100

# Enable verbose logging (default: false)
verbose = false
```

## DDL Statement Classification

The CLI classifies SQL statements and only wraps DDL statements in `spock.replicate_ddl()`. DML statements (INSERT, UPDATE, DELETE, SELECT) are executed normally and replicated through Spock's standard logical replication.

### DDL Statements (wrapped)
- `CREATE TABLE`, `ALTER TABLE`, `DROP TABLE`
- `CREATE INDEX`, `DROP INDEX`
- `CREATE SCHEMA`, `DROP SCHEMA`
- `CREATE TYPE`, `ALTER TYPE`, `DROP TYPE`
- `CREATE FUNCTION`, `DROP FUNCTION`
- `CREATE TRIGGER`, `DROP TRIGGER`
- `CREATE SEQUENCE`, `ALTER SEQUENCE`, `DROP SEQUENCE`
- `CREATE VIEW`, `DROP VIEW`
- `CREATE EXTENSION`, `DROP EXTENSION`
- `GRANT`, `REVOKE`
- And more...

### DML Statements (not wrapped)
- `INSERT`, `UPDATE`, `DELETE`
- `SELECT`
- `COPY`
- Transaction control (`BEGIN`, `COMMIT`, `ROLLBACK`)

## Sequence Configuration for Bi-Directional Replication

To avoid ID conflicts in bi-directional replication, sequences must be configured with offsets:

**Primary Node (node_offset=1):** Generates odd IDs (1, 3, 5, 7...)
```sql
ALTER SEQUENCE table_id_seq INCREMENT BY 2;
SELECT setval('table_id_seq', (SELECT COALESCE(MAX(id), 0) FROM table) + 1);
```

**Standby Node (node_offset=2):** Generates even IDs (2, 4, 6, 8...)
```sql
ALTER SEQUENCE table_id_seq INCREMENT BY 2;
SELECT setval('table_id_seq', (SELECT COALESCE(MAX(id), 0) FROM table) + 2);
```

The CLI can automatically configure sequences when creating tables if `auto_add_tables` is enabled.

## Error Handling

### Missing `--spock-remote-dsn`

If you connect to a Spock-enabled database without providing `--spock-remote-dsn`:

```
Error: Spock replication is enabled on this database. Use --spock-remote-dsn to specify the remote node, or connect to a non-Spock database
```

### Connection Failures

If the remote Spock node is unreachable:

```
Error: failed to connect to remote Spock node: dial tcp: connection refused
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Supabase CLI                             │
├─────────────────────────────────────────────────────────────────┤
│  pkg/spock/                                                     │
│  ├── classifier.go   - Detects DDL vs DML statements            │
│  ├── transformer.go  - Wraps DDL in spock.replicate_ddl()       │
│  └── executor.go     - Executes with Spock support              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Primary Database                            │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐         │
│  │   Spock     │───▶│  WAL Sender │───▶│ Replication │         │
│  │  Extension  │    │             │    │    Slot     │         │
│  └─────────────┘    └─────────────┘    └─────────────┘         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ Logical Replication
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Standby Database                            │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐         │
│  │   Spock     │◀───│ WAL Receiver│◀───│ Subscription│         │
│  │  Extension  │    │             │    │             │         │
│  └─────────────┘    └─────────────┘    └─────────────┘         │
└─────────────────────────────────────────────────────────────────┘
```

## Testing

### Verify Spock Detection

```bash
# Should detect Spock and require --spock-remote-dsn
supabase db exec --sql "SELECT 1" --db-url "postgresql://..."

# Error: Spock replication is enabled on this database...
```

### Verify DDL Replication

```bash
# Create table on primary
supabase db exec \
  --sql "CREATE TABLE test (id SERIAL PRIMARY KEY, data TEXT)" \
  --db-url "postgresql://...@primary:5432/postgres" \
  --spock-remote-dsn "postgresql://...@standby:5432/postgres"

# Verify on standby
psql "postgresql://...@standby:5432/postgres" -c "\d test"
```

### Verify Data Replication

```bash
# Insert on primary
supabase db exec \
  --sql "INSERT INTO test (data) VALUES ('from primary')" \
  --db-url "postgresql://...@primary:5432/postgres" \
  --spock-remote-dsn "postgresql://...@standby:5432/postgres"

# Verify on standby
psql "postgresql://...@standby:5432/postgres" -c "SELECT * FROM test"
```

## Troubleshooting

### DDL Not Replicating

1. Verify Spock extension is installed: `SELECT * FROM pg_extension WHERE extname = 'spock'`
2. Verify local node exists: `SELECT * FROM spock.local_node`
3. Check subscription status: `SELECT * FROM spock.sub_show_status()`
4. Verify table is in replication set: `SELECT * FROM spock.tables`

### Sequence Conflicts

If you see duplicate key errors:
1. Check sequence increment: `SELECT increment_by FROM pg_sequences WHERE sequencename = 'table_id_seq'`
2. Verify node offsets are different on each node
3. Reset sequences with proper offsets

### Replication Lag

Monitor replication status:
```sql
SELECT * FROM spock.sub_show_status();
SELECT * FROM pg_replication_slots WHERE slot_name LIKE 'spk%';
```

## Important Notes

1. **Never use `migration down` for reverting** - Use `migration repair --status reverted` instead
2. **Always use `sslmode=disable`** when connecting through Cloudflare tunnels
3. **Sequences must be configured** with INCREMENT BY 2 for bi-directional replication
4. **DDL changes require both nodes** to be accessible for proper replication
5. **Conflict resolution** defaults to `last_update_wins` - configure via `spock.conflict_resolution`

## Related Documentation

- [Spock GitHub Repository](https://github.com/pgEdge/spock)
- [Supabase Self-Hosting Guide](https://supabase.com/docs/guides/self-hosting)
- [PostgreSQL Logical Replication](https://www.postgresql.org/docs/current/logical-replication.html)
