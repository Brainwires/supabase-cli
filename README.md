# Supabase CLI

[![Coverage Status](https://coveralls.io/repos/github/supabase/cli/badge.svg?branch=main)](https://coveralls.io/github/supabase/cli?branch=main) [![Bitbucket Pipelines](https://img.shields.io/bitbucket/pipelines/supabase-cli/setup-cli/master?style=flat-square&label=Bitbucket%20Canary)](https://bitbucket.org/supabase-cli/setup-cli/pipelines) [![Gitlab Pipeline Status](https://img.shields.io/gitlab/pipeline-status/sweatybridge%2Fsetup-cli?label=Gitlab%20Canary)
](https://gitlab.com/sweatybridge/setup-cli/-/pipelines)

[Supabase](https://supabase.io) is an open source Firebase alternative. We're building the features of Firebase using enterprise-grade open source tools.

This repository contains all the functionality for Supabase CLI.

- [x] Running Supabase locally
- [x] Managing database migrations
- [x] Creating and deploying Supabase Functions
- [x] Generating types directly from your database schema
- [x] Making authenticated HTTP requests to [Management API](https://supabase.com/docs/reference/api/introduction)

## Getting started

### Install the CLI

Available via [NPM](https://www.npmjs.com) as dev dependency. To install:

```bash
npm i supabase --save-dev
```

When installing with yarn 4, you need to disable experimental fetch with the following nodejs config.

```
NODE_OPTIONS=--no-experimental-fetch yarn add supabase
```

> **Note**
For Bun versions below v1.0.17, you must add `supabase` as a [trusted dependency](https://bun.sh/guides/install/trusted) before running `bun add -D supabase`.

<details>
  <summary><b>macOS</b></summary>

  Available via [Homebrew](https://brew.sh). To install:

  ```sh
  brew install supabase/tap/supabase
  ```

  To install the beta release channel:
  
  ```sh
  brew install supabase/tap/supabase-beta
  brew link --overwrite supabase-beta
  ```
  
  To upgrade:

  ```sh
  brew upgrade supabase
  ```
</details>

<details>
  <summary><b>Windows</b></summary>

  Available via [Scoop](https://scoop.sh). To install:

  ```powershell
  scoop bucket add supabase https://github.com/supabase/scoop-bucket.git
  scoop install supabase
  ```

  To upgrade:

  ```powershell
  scoop update supabase
  ```
</details>

<details>
  <summary><b>Linux</b></summary>

  Available via [Homebrew](https://brew.sh) and Linux packages.

  #### via Homebrew

  To install:

  ```sh
  brew install supabase/tap/supabase
  ```

  To upgrade:

  ```sh
  brew upgrade supabase
  ```

  #### via Linux packages

  Linux packages are provided in [Releases](https://github.com/supabase/cli/releases). To install, download the `.apk`/`.deb`/`.rpm`/`.pkg.tar.zst` file depending on your package manager and run the respective commands.

  ```sh
  sudo apk add --allow-untrusted <...>.apk
  ```

  ```sh
  sudo dpkg -i <...>.deb
  ```

  ```sh
  sudo rpm -i <...>.rpm
  ```

  ```sh
  sudo pacman -U <...>.pkg.tar.zst
  ```
</details>

<details>
  <summary><b>Other Platforms</b></summary>

  You can also install the CLI via [go modules](https://go.dev/ref/mod#go-install) without the help of package managers.

  ```sh
  go install github.com/supabase/cli@latest
  ```

  Add a symlink to the binary in `$PATH` for easier access:

  ```sh
  ln -s "$(go env GOPATH)/bin/cli" /usr/bin/supabase
  ```

  This works on other non-standard Linux distros.
</details>

<details>
  <summary><b>Community Maintained Packages</b></summary>

  Available via [pkgx](https://pkgx.sh/). Package script [here](https://github.com/pkgxdev/pantry/blob/main/projects/supabase.com/cli/package.yml).
  To install in your working directory:

  ```bash
  pkgx install supabase
  ```

  Available via [Nixpkgs](https://nixos.org/). Package script [here](https://github.com/NixOS/nixpkgs/blob/master/pkgs/development/tools/supabase-cli/default.nix).
</details>

### Run the CLI

```bash
supabase bootstrap
```

Or using npx:

```bash
npx supabase bootstrap
```

The bootstrap command will guide you through the process of setting up a Supabase project using one of the [starter](https://github.com/supabase-community/supabase-samples/blob/main/samples.json) templates.

## Docs

Command & config reference can be found [here](https://supabase.com/docs/reference/cli/about).

## Breaking changes

We follow semantic versioning for changes that directly impact CLI commands, flags, and configurations.

However, due to dependencies on other service images, we cannot guarantee that schema migrations, seed.sql, and generated types will always work for the same CLI major version. If you need such guarantees, we encourage you to pin a specific version of CLI in package.json.

## Brainwires Fork - Spock Bi-Directional Replication

This is a **Brainwires fork** of the Supabase CLI with added support for **Spock bi-directional PostgreSQL replication**.

### Features Added

- **Automatic Spock Detection** - Detects Spock from database connection (no config needed)
- **Automatic DDL Replication** - Wraps CREATE/ALTER/DROP statements with `spock.replicate_ddl()`
- **Auto Table Registration** - Automatically adds new tables to replication sets
- **New `db exec` Command** - Execute arbitrary SQL with Spock support
- **`--spock-remote-dsn` Flag** - Specify remote node for replication verification

### Quick Start

```bash
# Execute DDL with Spock replication
supabase db exec \
  --sql "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT)" \
  --db-url "postgresql://postgres:pass@primary:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:pass@standby:5432/postgres?sslmode=disable"

# Apply migrations with Spock replication
supabase migration up \
  --db-url "postgresql://postgres:pass@primary:5432/postgres?sslmode=disable" \
  --spock-remote-dsn "postgresql://postgres:pass@standby:5432/postgres?sslmode=disable"
```

### Documentation

**See [SPOCK-README.md](./SPOCK-README.md) for complete documentation:**
- Supported commands and flags
- DDL statement classification
- Sequence configuration for bi-directional replication
- Troubleshooting guide

**See [supabase-postgres-spock](https://github.com/Brainwires/supabase-postgres-spock) for Spock PostgreSQL setup:**
- Docker image with Spock extension
- Production deployment guide
- Monitoring and troubleshooting

## Self-Hosted Management Commands

This fork includes powerful CLI commands for managing self-hosted Supabase instances deployed via Docker Compose.

### Prerequisites

All self-hosted commands require the `--folder` flag pointing to your Docker Compose directory containing the `.env` file.

### Commands Overview

| Command | Description |
|---------|-------------|
| `supabase backup` | Create database and storage backups (supports full backups with roles/settings) |
| `supabase restore` | Restore from backup files (auto-detects full backups) |
| `supabase health` | Monitor service health status |
| `supabase logs` | View and follow service logs |
| `supabase passwd` | Change database and dashboard passwords |
| `supabase roll` | Regenerate API keys (JWT, anon, service role) |

---

### `supabase backup`

Create backups of your database and/or storage buckets.

**Backup Types:**
- **Standard** (default): Schema and data only using pg_dump. Fast and suitable for data migration between existing instances.
- **Full** (`--full`): Complete disaster recovery backup including roles, permissions, settings, extensions, and data. Use this for restoring to a fresh database.

```bash
# Standard database backup (schema + data)
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
supabase backup --folder ~/supabase/docker --db --data-only
```

**Full Backup Contents:**
When using `--full`, the backup creates a tar archive containing:
- `roles.sql` - All database roles and permissions
- `settings.sql` - Database configuration settings
- `extensions.sql` - Installed extensions list
- `database.dump` - Complete schema and data (pg_dump custom format)
- `MANIFEST.txt` - Backup metadata and restore instructions

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--db` - Backup PostgreSQL database using pg_dump
- `--storage` - Backup storage buckets as tar archive
- `--output, -o` - Output file path
- `--compress` - Compress output with gzip
- `--full` - Full backup including roles, settings, and extensions
- `--data-only` - Only backup data, no schema
- `--schema-only` - Only backup schema, no data

---

### `supabase restore`

Restore database and/or storage from backup files. Compressed backups (.gz) and full backups (tar with MANIFEST.txt) are automatically detected.

```bash
# Restore standard database backup
supabase restore --folder ~/supabase/docker --db /backups/backup.dump

# Restore full backup (auto-detected, restores roles first)
supabase restore --folder ~/supabase/docker --db /backups/full-backup.tar

# Restore compressed backup
supabase restore --folder ~/supabase/docker --db /backups/backup.dump.gz

# Restore storage buckets
supabase restore --folder ~/supabase/docker --storage /backups/storage.tar.gz

# Restore with clean (drop existing objects first)
supabase restore --folder ~/supabase/docker --db /backups/backup.dump --clean

# Skip confirmation prompt
supabase restore --folder ~/supabase/docker --db /backups/backup.dump --yes
```

**Full Backup Restore:**
When restoring a full backup (created with `--full`), the restore process automatically:
1. Detects the full backup format (tar archive with MANIFEST.txt)
2. Restores roles and permissions first (skipping existing roles)
3. Restores the database schema and data
4. Applies database settings

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--db` - Database backup file to restore
- `--storage` - Storage backup file to restore
- `--clean` - Drop existing objects before restore
- `--yes, -y` - Skip confirmation prompt

---

### `supabase health`

Display health status of all services in your self-hosted instance.

```bash
# Check health of all services
supabase health --folder ~/supabase/docker

# Output as JSON
supabase health --folder ~/supabase/docker --json

# Continuously monitor (refresh every 5 seconds)
supabase health --folder ~/supabase/docker --watch 5
```

**Example Output:**
```
╭───────────┬─────────┬─────────┬────────┬─────────────────────╮
│ SERVICE   │ STATUS  │ HEALTH  │ UPTIME │ IMAGE               │
├───────────┼─────────┼─────────┼────────┼─────────────────────┤
│ db        │ running │ healthy │ 2h 15m │ 15.8.1.060          │
│ auth      │ running │ healthy │ 2h 15m │ v2.184.0            │
│ kong      │ running │ healthy │ 2h 15m │ 2.8.1               │
│ rest      │ running │ healthy │ 2h 15m │ v12.2.8             │
│ realtime  │ running │ healthy │ 2h 15m │ v2.33.70            │
│ storage   │ running │ healthy │ 2h 15m │ v1.22.12            │
│ studio    │ running │ healthy │ 2h 15m │ 20250113-41c4d0f    │
╰───────────┴─────────┴─────────┴────────┴─────────────────────╯

Summary: 7 healthy, 0 unhealthy, 0 stopped
```

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--json` - Output as JSON
- `--watch, -w` - Continuously monitor (refresh every N seconds)

---

### `supabase logs`

View and follow logs from self-hosted services.

```bash
# View logs from database service
supabase logs --folder ~/supabase/docker -s db

# View logs from multiple services
supabase logs --folder ~/supabase/docker -s auth -s kong

# Follow logs (like tail -f)
supabase logs --folder ~/supabase/docker -s db -f

# Show logs from the last 10 minutes
supabase logs --folder ~/supabase/docker -s auth --since 10m

# Show logs from a specific date
supabase logs --folder ~/supabase/docker -s db --since 2024-01-01

# Show last 50 lines
supabase logs --folder ~/supabase/docker -s db -n 50

# View all service logs
supabase logs --folder ~/supabase/docker
```

**Valid Services:** `db`, `kong`, `auth`, `rest`, `realtime`, `storage`, `imgproxy`, `meta`, `functions`, `analytics`, `studio`, `vector`, `pooler`

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--service, -s` - Filter by service (can be specified multiple times)
- `--follow, -f` - Follow logs (tail -f style)
- `--since` - Show logs since (e.g., '10m', '1h', '2024-01-01')
- `--tail, -n` - Number of lines to show (default 100)

---

### `supabase passwd`

Change passwords for database and/or studio dashboard.

```bash
# Change database password (interactive prompt)
supabase passwd --folder ~/supabase/docker --db

# Change specific database user password
supabase passwd --folder ~/supabase/docker --db --user authenticator

# Auto-generate a new studio password
supabase passwd --folder ~/supabase/docker --studio --generate

# Change both database and studio passwords
supabase passwd --folder ~/supabase/docker --db --studio --generate

# Non-interactive mode with automatic restart
supabase passwd --folder ~/supabase/docker --studio --generate --restart
```

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--db` - Change database password (POSTGRES_PASSWORD)
- `--studio` - Change studio dashboard password (DASHBOARD_PASSWORD)
- `--user` - Database user to change password for (default: postgres)
- `--generate` - Auto-generate a secure password
- `--password` - New password (not recommended, use prompt instead)
- `--restart` - Non-interactive mode: skip prompts and restart containers

---

### `supabase roll`

Regenerate API keys (JWT secret, anon key, service role key) for your self-hosted instance.

```bash
# Regenerate all keys
supabase roll --folder ~/supabase/docker --all

# Regenerate only JWT secret
supabase roll --folder ~/supabase/docker --jwt

# Regenerate only anon key
supabase roll --folder ~/supabase/docker --anon

# Regenerate only service role key
supabase roll --folder ~/supabase/docker --service-role

# Non-interactive mode with automatic restart
supabase roll --folder ~/supabase/docker --all --restart
```

**Flags:**
- `--folder` (required) - Path to docker folder containing .env
- `--all` - Regenerate all keys (JWT, anon, service role)
- `--jwt` - Regenerate JWT secret
- `--anon` - Regenerate anon key
- `--service-role` - Regenerate service role key
- `--restart` - Non-interactive mode: skip prompts and restart containers

---

## Developing

To run from source:

```sh
# Go >= 1.22
go run . help
```
