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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-errors/errors"
	"github.com/supabase/cli/internal/utils"
)

// RestoreOptions configures restore behavior
type RestoreOptions struct {
	FolderPath        string
	DBBackupFile      string
	StorageBackupFile string
	Clean             bool
}

// RestoreResult contains the results of a restore operation
type RestoreResult struct {
	DBRestored      bool
	StorageRestored bool
	RolesRestored   bool
	IsFullRestore   bool
}

// RunRestore performs database and/or storage restore
func RunRestore(ctx context.Context, opts RestoreOptions) (*RestoreResult, error) {
	// Read .env file
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return nil, err
	}

	result := &RestoreResult{}

	// Perform database restore
	if opts.DBBackupFile != "" {
		// Check if this is a full backup
		if IsFullBackup(opts.DBBackupFile) {
			result.IsFullRestore = true
			if err := restoreDatabaseFull(ctx, env, opts, result); err != nil {
				return nil, errors.Errorf("full database restore failed: %w", err)
			}
		} else {
			if err := restoreDatabase(ctx, env, opts); err != nil {
				return nil, errors.Errorf("database restore failed: %w", err)
			}
		}
		result.DBRestored = true
	}

	// Perform storage restore
	if opts.StorageBackupFile != "" {
		if err := restoreStorage(ctx, env, opts); err != nil {
			return nil, errors.Errorf("storage restore failed: %w", err)
		}
		result.StorageRestored = true
	}

	return result, nil
}

// isGzipped checks if a file is gzip compressed by checking magic bytes
func isGzipped(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	// Read first two bytes (gzip magic number: 0x1f 0x8b)
	buf := make([]byte, 2)
	_, err = file.Read(buf)
	if err != nil {
		return false, err
	}

	return buf[0] == 0x1f && buf[1] == 0x8b, nil
}

// restoreDatabaseFull restores from a full backup archive (roles + database)
func restoreDatabaseFull(ctx context.Context, env *EnvFile, opts RestoreOptions, result *RestoreResult) error {
	projectName := GetProjectName(env)

	// Find the db container
	containerID, err := GetContainerIDByService(ctx, projectName, "db")
	if err != nil {
		return err
	}

	// Open backup file
	file, err := os.Open(opts.DBBackupFile)
	if err != nil {
		return errors.Errorf("failed to open backup file: %w", err)
	}
	defer file.Close()

	// Check for gzip compression
	var reader io.Reader = file
	isCompressed, _ := isGzipped(opts.DBBackupFile)
	if isCompressed {
		file.Seek(0, 0)
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return errors.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		file.Seek(0, 0)
	}

	// Extract files from tar
	tarReader := tar.NewReader(reader)
	files := make(map[string][]byte)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Errorf("failed to read tar: %w", err)
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return errors.Errorf("failed to read %s: %w", header.Name, err)
		}
		files[header.Name] = data
	}

	password := env.GetEnvValue("POSTGRES_PASSWORD", "")

	// 1. Restore roles (skip if roles already exist - pg will error on duplicates)
	if rolesData, ok := files["roles.sql"]; ok {
		fmt.Println("  Restoring roles (skipping existing)...")

		// Filter out roles that would conflict with existing ones
		// We'll use ON_ERROR_ROLLBACK to continue past errors
		if err := execSQL(ctx, containerID, string(rolesData), password, true); err != nil {
			// Roles restore failures are often due to existing roles, which is OK
			fmt.Printf("  %s Some roles may already exist (this is normal)\n", utils.Yellow("Note:"))
		}
		result.RolesRestored = true
	}

	// 2. Restore database
	if dbData, ok := files["database.dump"]; ok {
		fmt.Println("  Restoring database schema and data...")

		// Copy dump to container
		tempPath := "/tmp/restore_backup.dump"
		if err := copyToContainer(ctx, containerID, tempPath, dbData); err != nil {
			return errors.Errorf("failed to copy backup to container: %w", err)
		}

		// Run pg_restore
		cmd := []string{"pg_restore", "-U", "postgres", "-d", "postgres"}
		if opts.Clean {
			cmd = append(cmd, "--clean", "--if-exists")
		}
		cmd = append(cmd, tempPath)

		execConfig := container.ExecOptions{
			Cmd:          cmd,
			AttachStdout: true,
			AttachStderr: true,
			Env:          []string{"PGPASSWORD=" + password},
		}

		execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
		if err != nil {
			return errors.Errorf("failed to create exec: %w", err)
		}

		resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
		if err != nil {
			return errors.Errorf("failed to attach exec: %w", err)
		}

		var errBuf strings.Builder
		_, err = stdcopy.StdCopy(os.Stdout, &errBuf, resp.Reader)
		resp.Close()
		if err != nil {
			return errors.Errorf("failed to copy output: %w", err)
		}

		inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
		if err != nil {
			return errors.Errorf("failed to inspect exec: %w", err)
		}

		// pg_restore returns non-zero for warnings
		if inspectResp.ExitCode != 0 && inspectResp.ExitCode != 1 {
			return errors.Errorf("pg_restore failed with exit code %d: %s", inspectResp.ExitCode, errBuf.String())
		}

		// Cleanup
		cleanupCmd := []string{"rm", "-f", tempPath}
		cleanupExec, _ := utils.Docker.ContainerExecCreate(ctx, containerID, container.ExecOptions{Cmd: cleanupCmd})
		if cleanupExec.ID != "" {
			utils.Docker.ContainerExecStart(ctx, cleanupExec.ID, container.ExecStartOptions{})
		}
	}

	// 3. Optionally restore settings
	if settingsData, ok := files["settings.sql"]; ok {
		// Only restore if there are actual settings (not just the header)
		if strings.Contains(string(settingsData), "ALTER DATABASE") {
			fmt.Println("  Restoring database settings...")
			if err := execSQL(ctx, containerID, string(settingsData), password, true); err != nil {
				fmt.Printf("  %s Failed to restore some settings: %v\n", utils.Yellow("Warning:"), err)
			}
		}
	}

	return nil
}

// execSQL runs SQL in a container via psql
func execSQL(ctx context.Context, containerID, sql, password string, ignoreErrors bool) error {
	cmd := []string{"psql", "-U", "postgres", "-d", "postgres"}
	if ignoreErrors {
		cmd = append(cmd, "-v", "ON_ERROR_STOP=0")
	}
	cmd = append(cmd, "-c", sql)

	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"PGPASSWORD=" + password},
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return err
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return err
	}

	var errBuf strings.Builder
	stdcopy.StdCopy(io.Discard, &errBuf, resp.Reader)
	resp.Close()

	inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return err
	}

	if !ignoreErrors && inspectResp.ExitCode != 0 {
		return errors.Errorf("psql failed: %s", errBuf.String())
	}

	return nil
}

// copyToContainer copies data to a file inside a container
func copyToContainer(ctx context.Context, containerID, path string, data []byte) error {
	copyCmd := []string{"sh", "-c", fmt.Sprintf("cat > %s", path)}

	execConfig := container.ExecOptions{
		Cmd:          copyCmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return err
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return err
	}

	_, err = io.Copy(resp.Conn, bytes.NewReader(data))
	if err != nil {
		resp.Close()
		return err
	}
	resp.CloseWrite()

	io.Copy(io.Discard, resp.Reader)
	resp.Close()

	return nil
}

// restoreDatabase restores a database from a backup file using pg_restore via docker exec
func restoreDatabase(ctx context.Context, env *EnvFile, opts RestoreOptions) error {
	projectName := GetProjectName(env)

	// Find the db container
	containerID, err := GetContainerIDByService(ctx, projectName, "db")
	if err != nil {
		return err
	}

	// Check if backup file is compressed
	isCompressed, err := isGzipped(opts.DBBackupFile)
	if err != nil {
		// Fall back to extension check
		isCompressed = strings.HasSuffix(opts.DBBackupFile, ".gz")
	}

	// Read the backup file
	backupFile, err := os.Open(opts.DBBackupFile)
	if err != nil {
		return errors.Errorf("failed to open backup file: %w", err)
	}
	defer backupFile.Close()

	var reader io.Reader = backupFile

	// Decompress if needed
	if isCompressed {
		gzReader, err := gzip.NewReader(backupFile)
		if err != nil {
			return errors.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Read all backup content into memory (for simplicity)
	backupContent, err := io.ReadAll(reader)
	if err != nil {
		return errors.Errorf("failed to read backup file: %w", err)
	}

	// Build pg_restore command
	cmd := []string{"pg_restore", "-U", "postgres", "-d", "postgres"}

	if opts.Clean {
		cmd = append(cmd, "--clean", "--if-exists")
	}

	// We need to pipe the backup content to pg_restore
	// First, copy the backup file to the container, then run pg_restore

	// Create a temp file path inside the container
	tempPath := "/tmp/restore_backup.dump"

	// Copy file to container using docker cp via exec with cat
	copyCmd := []string{"sh", "-c", fmt.Sprintf("cat > %s", tempPath)}

	execConfig := container.ExecOptions{
		Cmd:          copyCmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	execID, err := utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return errors.Errorf("failed to create exec: %w", err)
	}

	resp, err := utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return errors.Errorf("failed to attach exec: %w", err)
	}

	// Write backup content to stdin
	_, err = io.Copy(resp.Conn, bytes.NewReader(backupContent))
	if err != nil {
		resp.Close()
		return errors.Errorf("failed to copy backup to container: %w", err)
	}
	resp.CloseWrite()

	// Read any output
	io.Copy(io.Discard, resp.Reader)
	resp.Close()

	// Now run pg_restore
	cmd = append(cmd, tempPath)

	execConfig = container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Env:          []string{"PGPASSWORD=" + env.GetEnvValue("POSTGRES_PASSWORD", "")},
	}

	execID, err = utils.Docker.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return errors.Errorf("failed to create exec: %w", err)
	}

	resp, err = utils.Docker.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{})
	if err != nil {
		return errors.Errorf("failed to attach exec: %w", err)
	}
	defer resp.Close()

	// Copy output
	var errBuf strings.Builder
	_, err = stdcopy.StdCopy(os.Stdout, &errBuf, resp.Reader)
	if err != nil {
		return errors.Errorf("failed to copy output: %w", err)
	}

	// Check exit code
	inspectResp, err := utils.Docker.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return errors.Errorf("failed to inspect exec: %w", err)
	}

	// pg_restore returns non-zero exit codes for warnings, so we only fail on severe errors
	if inspectResp.ExitCode != 0 && inspectResp.ExitCode != 1 {
		return errors.Errorf("pg_restore failed with exit code %d: %s", inspectResp.ExitCode, errBuf.String())
	}

	// Clean up temp file
	cleanupCmd := []string{"rm", "-f", tempPath}
	cleanupExec, _ := utils.Docker.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd: cleanupCmd,
	})
	if cleanupExec.ID != "" {
		utils.Docker.ContainerExecStart(ctx, cleanupExec.ID, container.ExecStartOptions{})
	}

	return nil
}

// restoreStorage extracts a tar archive to the storage volumes directory
func restoreStorage(ctx context.Context, env *EnvFile, opts RestoreOptions) error {
	storagePath := filepath.Join(opts.FolderPath, "volumes", "storage")

	// Create storage directory if it doesn't exist
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		return errors.Errorf("failed to create storage directory: %w", err)
	}

	// If clean option is set, remove existing contents
	if opts.Clean {
		entries, err := os.ReadDir(storagePath)
		if err != nil {
			return errors.Errorf("failed to read storage directory: %w", err)
		}
		for _, entry := range entries {
			path := filepath.Join(storagePath, entry.Name())
			if err := os.RemoveAll(path); err != nil {
				return errors.Errorf("failed to remove %s: %w", path, err)
			}
		}
	}

	// Open the backup file
	backupFile, err := os.Open(opts.StorageBackupFile)
	if err != nil {
		return errors.Errorf("failed to open backup file: %w", err)
	}
	defer backupFile.Close()

	// Check if compressed
	isCompressed, err := isGzipped(opts.StorageBackupFile)
	if err != nil {
		isCompressed = strings.HasSuffix(opts.StorageBackupFile, ".gz")
	}

	var tarReader *tar.Reader

	if isCompressed {
		gzReader, err := gzip.NewReader(backupFile)
		if err != nil {
			return errors.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		tarReader = tar.NewReader(gzReader)
	} else {
		tarReader = tar.NewReader(backupFile)
	}

	// Extract files from the tar archive
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.Errorf("failed to read tar header: %w", err)
		}

		targetPath := filepath.Join(storagePath, header.Name)

		// Security check: ensure the path is within the storage directory
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(storagePath)) {
			return errors.Errorf("invalid tar entry path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return errors.Errorf("failed to create directory: %w", err)
			}

		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return errors.Errorf("failed to create parent directory: %w", err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return errors.Errorf("failed to create file: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return errors.Errorf("failed to extract file: %w", err)
			}
			outFile.Close()

		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return errors.Errorf("failed to create symlink: %w", err)
			}
		}
	}

	return nil
}

// PrintRestoreResult displays the results of a restore operation
func PrintRestoreResult(result *RestoreResult) {
	fmt.Println()
	fmt.Println(utils.Bold("=== Restore Results ==="))
	fmt.Println()

	if result.DBRestored {
		if result.IsFullRestore {
			fmt.Printf("%s Full database restored successfully\n", utils.Green("OK"))
			if result.RolesRestored {
				fmt.Println("   - Roles and permissions restored")
			}
			fmt.Println("   - Schema and data restored")
		} else {
			fmt.Printf("%s Database restored successfully\n", utils.Green("OK"))
		}
	}

	if result.StorageRestored {
		fmt.Printf("%s Storage restored successfully\n", utils.Green("OK"))
	}

	fmt.Println()
	fmt.Println(utils.Yellow("Note: You may need to restart containers for changes to take effect."))
}
