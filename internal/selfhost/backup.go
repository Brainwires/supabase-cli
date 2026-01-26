package selfhost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-errors/errors"
	"github.com/supabase/cli/internal/utils"
)

// BackupOptions configures backup behavior
type BackupOptions struct {
	FolderPath    string
	BackupDB      bool
	BackupStorage bool
	Output        string
	Compress      bool
	DataOnly      bool
	SchemaOnly    bool
	Full          bool // Full backup includes roles, settings, and all global objects
}

// BackupResult contains the results of a backup operation
type BackupResult struct {
	DBBackupPath      string
	StorageBackupPath string
	RolesIncluded     bool
	ObjectCounts      map[string]int
}

// RunBackup performs database and/or storage backup
func RunBackup(ctx context.Context, opts BackupOptions) (*BackupResult, error) {
	// Read .env file
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return nil, err
	}

	result := &BackupResult{
		ObjectCounts: make(map[string]int),
	}

	// Perform database backup
	if opts.BackupDB {
		var dbPath string
		var err error

		if opts.Full {
			dbPath, err = backupDatabaseFull(ctx, env, opts, result)
		} else {
			dbPath, err = backupDatabase(ctx, env, opts)
		}

		if err != nil {
			return nil, errors.Errorf("database backup failed: %w", err)
		}
		result.DBBackupPath = dbPath
	}

	// Perform storage backup
	if opts.BackupStorage {
		storagePath, err := backupStorage(ctx, env, opts)
		if err != nil {
			return nil, errors.Errorf("storage backup failed: %w", err)
		}
		result.StorageBackupPath = storagePath
	}

	return result, nil
}

// backupDatabaseFull creates a complete database backup including roles and global objects
func backupDatabaseFull(ctx context.Context, env *EnvFile, opts BackupOptions, result *BackupResult) (string, error) {
	projectName := GetProjectName(env)

	// Find the db container
	containerID, err := GetContainerIDByService(ctx, projectName, "db")
	if err != nil {
		return "", err
	}

	// Determine output filename
	outputPath := opts.Output
	if outputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("backup-full-%s.tar", timestamp)
		if opts.Compress {
			outputPath += ".gz"
		}
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", errors.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	var tarWriter *tar.Writer

	if opts.Compress {
		gzWriter := gzip.NewWriter(outFile)
		defer gzWriter.Close()
		tarWriter = tar.NewWriter(gzWriter)
	} else {
		tarWriter = tar.NewWriter(outFile)
	}
	defer tarWriter.Close()

	password := env.GetEnvValue("POSTGRES_PASSWORD", "")

	// 1. Backup roles using pg_dumpall --roles-only
	fmt.Println("  Backing up roles and global objects...")
	rolesData, err := execCommand(ctx, containerID, []string{
		"pg_dumpall", "-U", "postgres", "--roles-only",
	}, password)
	if err != nil {
		return "", errors.Errorf("failed to backup roles: %w", err)
	}

	// Count roles
	roleCount := strings.Count(rolesData, "CREATE ROLE") + strings.Count(rolesData, "ALTER ROLE")
	result.ObjectCounts["roles"] = roleCount
	result.RolesIncluded = true

	// Write roles to tar
	if err := addToTar(tarWriter, "roles.sql", []byte(rolesData)); err != nil {
		return "", errors.Errorf("failed to write roles to archive: %w", err)
	}

	// 2. Backup database settings
	fmt.Println("  Backing up database settings...")
	settingsData, err := execCommand(ctx, containerID, []string{
		"psql", "-U", "postgres", "-c",
		`SELECT 'ALTER DATABASE "' || datname || '" SET ' ||
		 setconfig[1] || ';'
		 FROM pg_db_role_setting s
		 JOIN pg_database d ON d.oid = s.setdatabase
		 WHERE setdatabase != 0;`,
	}, password)
	if err != nil {
		// Non-fatal - some DBs might not have custom settings
		settingsData = "-- No custom database settings found\n"
	}

	if err := addToTar(tarWriter, "settings.sql", []byte(settingsData)); err != nil {
		return "", errors.Errorf("failed to write settings to archive: %w", err)
	}

	// 3. Backup extensions list (for reference)
	fmt.Println("  Backing up extensions list...")
	extensionsData, err := execCommand(ctx, containerID, []string{
		"psql", "-U", "postgres", "-t", "-c",
		`SELECT 'CREATE EXTENSION IF NOT EXISTS "' || extname || '" WITH SCHEMA ' ||
		 nspname || ';'
		 FROM pg_extension e
		 JOIN pg_namespace n ON e.extnamespace = n.oid
		 WHERE extname NOT IN ('plpgsql');`,
	}, password)
	if err != nil {
		extensionsData = "-- Failed to list extensions\n"
	}

	extCount := strings.Count(extensionsData, "CREATE EXTENSION")
	result.ObjectCounts["extensions"] = extCount

	if err := addToTar(tarWriter, "extensions.sql", []byte(extensionsData)); err != nil {
		return "", errors.Errorf("failed to write extensions to archive: %w", err)
	}

	// 4. Backup database using pg_dump
	fmt.Println("  Backing up database schema and data...")
	cmd := []string{"pg_dump", "-U", "postgres", "-Fc"}

	if opts.DataOnly {
		cmd = append(cmd, "--data-only")
	}
	if opts.SchemaOnly {
		cmd = append(cmd, "--schema-only")
	}

	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"PGPASSWORD=" + password},
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", errors.Errorf("failed to create exec: %w", err)
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return "", errors.Errorf("failed to attach exec: %w", err)
	}

	// Read pg_dump output into buffer
	var dumpBuf bytes.Buffer
	var errBuf strings.Builder
	_, err = stdcopy.StdCopy(&dumpBuf, &errBuf, resp.Reader)
	resp.Close()
	if err != nil {
		return "", errors.Errorf("failed to copy output: %w", err)
	}

	// Check exit code
	inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", errors.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		return "", errors.Errorf("pg_dump failed with exit code %d: %s", inspectResp.ExitCode, errBuf.String())
	}

	// Write database dump to tar
	if err := addToTar(tarWriter, "database.dump", dumpBuf.Bytes()); err != nil {
		return "", errors.Errorf("failed to write database to archive: %w", err)
	}

	// 5. Create manifest file
	fmt.Println("  Creating backup manifest...")
	manifest := fmt.Sprintf(`# Supabase Full Backup Manifest
# Created: %s
# Type: full

[backup]
format = tar
compression = %v
includes_roles = true
includes_settings = true
includes_extensions = true
includes_database = true

[counts]
roles = %d
extensions = %d

[files]
roles.sql = PostgreSQL roles (pg_dumpall --roles-only)
settings.sql = Database-level settings
extensions.sql = Installed extensions
database.dump = Database dump (pg_dump -Fc)

[restore_order]
1. roles.sql (psql)
2. extensions.sql (psql) - if needed
3. database.dump (pg_restore)
4. settings.sql (psql) - optional
`,
		time.Now().Format(time.RFC3339),
		opts.Compress,
		roleCount,
		extCount,
	)

	if err := addToTar(tarWriter, "MANIFEST.txt", []byte(manifest)); err != nil {
		return "", errors.Errorf("failed to write manifest to archive: %w", err)
	}

	return outputPath, nil
}

// execCommand runs a command in a container and returns stdout
func execCommand(ctx context.Context, containerID string, cmd []string, password string) (string, error) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"PGPASSWORD=" + password},
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", err
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var outBuf, errBuf bytes.Buffer
	_, err = stdcopy.StdCopy(&outBuf, &errBuf, resp.Reader)
	if err != nil {
		return "", err
	}

	inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", err
	}

	if inspectResp.ExitCode != 0 {
		return "", errors.Errorf("command failed with exit code %d: %s", inspectResp.ExitCode, errBuf.String())
	}

	return outBuf.String(), nil
}

// addToTar adds a file to a tar archive
func addToTar(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:    name,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err := tw.Write(data)
	return err
}

// backupDatabase creates a database backup using pg_dump via docker exec
func backupDatabase(ctx context.Context, env *EnvFile, opts BackupOptions) (string, error) {
	projectName := GetProjectName(env)

	// Find the db container
	containerID, err := GetContainerIDByService(ctx, projectName, "db")
	if err != nil {
		return "", err
	}

	// Build pg_dump command
	cmd := []string{"pg_dump", "-U", "postgres"}

	// Add format flag for custom format (better for restore)
	cmd = append(cmd, "-Fc")

	if opts.DataOnly {
		cmd = append(cmd, "--data-only")
	}
	if opts.SchemaOnly {
		cmd = append(cmd, "--schema-only")
	}

	// Determine output filename
	outputPath := opts.Output
	if outputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("backup-%s.dump", timestamp)
		if opts.Compress {
			outputPath += ".gz"
		}
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", errors.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	var writer io.Writer = outFile

	// Setup compression if needed
	if opts.Compress {
		gzWriter := gzip.NewWriter(outFile)
		defer gzWriter.Close()
		writer = gzWriter
	}

	// Execute pg_dump via docker exec
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"PGPASSWORD=" + env.GetEnvValue("POSTGRES_PASSWORD", "")},
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return "", errors.Errorf("failed to create exec: %w", err)
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return "", errors.Errorf("failed to attach exec: %w", err)
	}
	defer resp.Close()

	// Copy stdout to output, stderr to os.Stderr
	var errBuf strings.Builder
	_, err = stdcopy.StdCopy(writer, &errBuf, resp.Reader)
	if err != nil {
		return "", errors.Errorf("failed to copy output: %w", err)
	}

	// Check exit code
	inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", errors.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		return "", errors.Errorf("pg_dump failed with exit code %d: %s", inspectResp.ExitCode, errBuf.String())
	}

	return outputPath, nil
}

// backupStorage creates a tar archive of the storage volumes directory
func backupStorage(ctx context.Context, env *EnvFile, opts BackupOptions) (string, error) {
	storagePath := filepath.Join(opts.FolderPath, "volumes", "storage")

	// Check if storage directory exists
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		return "", errors.Errorf("storage directory not found: %s", storagePath)
	}

	// Determine output filename
	outputPath := opts.Output
	if outputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("storage-%s.tar", timestamp)
		if opts.Compress {
			outputPath += ".gz"
		}
	} else if opts.BackupDB {
		// If both DB and storage are being backed up, use different names
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("storage-%s.tar", timestamp)
		if opts.Compress {
			outputPath += ".gz"
		}
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", errors.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	var tarWriter *tar.Writer

	if opts.Compress {
		gzWriter := gzip.NewWriter(outFile)
		defer gzWriter.Close()
		tarWriter = tar.NewWriter(gzWriter)
	} else {
		tarWriter = tar.NewWriter(outFile)
	}
	defer tarWriter.Close()

	// Walk the storage directory and add files to the archive
	err = filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(storagePath, path)
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			header.Linkname = link
		}

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content if it's a regular file
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return "", errors.Errorf("failed to create tar archive: %w", err)
	}

	return outputPath, nil
}

// PrintBackupResult displays the results of a backup operation
func PrintBackupResult(result *BackupResult) {
	fmt.Println()
	fmt.Println(utils.Bold("=== Backup Results ==="))
	fmt.Println()

	if result.DBBackupPath != "" {
		backupType := "Database"
		if result.RolesIncluded {
			backupType = "Full database"
		}
		fmt.Printf("%s %s backup: %s\n", utils.Green("OK"), backupType, result.DBBackupPath)

		// Show file size
		if info, err := os.Stat(result.DBBackupPath); err == nil {
			fmt.Printf("   Size: %s\n", formatBytes(info.Size()))
		}

		// Show what's included in full backup
		if result.RolesIncluded {
			fmt.Println(utils.Aqua("   Includes:"))
			fmt.Println("   - Roles and permissions")
			fmt.Println("   - Database settings")
			fmt.Println("   - Extensions list")
			fmt.Println("   - Schema and data")
			if len(result.ObjectCounts) > 0 {
				fmt.Println(utils.Aqua("   Object counts:"))
				for obj, count := range result.ObjectCounts {
					fmt.Printf("   - %s: %d\n", obj, count)
				}
			}
		}
	}

	if result.StorageBackupPath != "" {
		fmt.Printf("%s Storage backup: %s\n", utils.Green("OK"), result.StorageBackupPath)

		// Show file size
		if info, err := os.Stat(result.StorageBackupPath); err == nil {
			fmt.Printf("   Size: %s\n", formatBytes(info.Size()))
		}
	}

	fmt.Println()
}

// formatBytes converts bytes to human-readable format
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// IsFullBackup checks if a backup file is a full backup (tar with manifest)
func IsFullBackup(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	// Check for gzip
	var reader io.Reader = file
	magic := make([]byte, 2)
	file.Read(magic)
	file.Seek(0, 0)

	if magic[0] == 0x1f && magic[1] == 0x8b {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return false
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Check for tar with MANIFEST.txt
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			break
		}
		if header.Name == "MANIFEST.txt" {
			return true
		}
	}

	return false
}
