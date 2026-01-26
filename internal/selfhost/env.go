package selfhost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-errors/errors"
)

// EnvFile represents a parsed .env file that preserves structure
type EnvFile struct {
	Path   string
	Lines  []string // Original lines (for preserving comments/order)
	Values map[string]string
}

// ReadEnvFile reads and parses a .env file, preserving structure
func ReadEnvFile(folderPath string) (*EnvFile, error) {
	envPath := filepath.Join(folderPath, ".env")

	file, err := os.Open(envPath)
	if err != nil {
		return nil, errors.Errorf("failed to open .env file: %w", err)
	}
	defer file.Close()

	env := &EnvFile{
		Path:   envPath,
		Lines:  []string{},
		Values: make(map[string]string),
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		env.Lines = append(env.Lines, line)

		// Parse key=value pairs (skip comments and empty lines)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Split on first = only
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := line[idx+1:]

		// Remove surrounding quotes if present
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		env.Values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, errors.Errorf("failed to read .env file: %w", err)
	}

	return env, nil
}

// UpdateEnvFile updates specific keys in the .env file, preserving structure
func UpdateEnvFile(env *EnvFile, updates map[string]string) error {
	// Create a map to track which keys we've updated
	updated := make(map[string]bool)

	// Update existing lines
	for i, line := range env.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		if newValue, ok := updates[key]; ok {
			env.Lines[i] = fmt.Sprintf("%s=%s", key, newValue)
			env.Values[key] = newValue
			updated[key] = true
		}
	}

	// Add any new keys that weren't in the file
	for key, value := range updates {
		if !updated[key] {
			env.Lines = append(env.Lines, fmt.Sprintf("%s=%s", key, value))
			env.Values[key] = value
		}
	}

	// Write back to file
	content := strings.Join(env.Lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(env.Path, []byte(content), 0644); err != nil {
		return errors.Errorf("failed to write .env file: %w", err)
	}

	return nil
}

// GetEnvValue returns a value from the env file, with a fallback default
func (e *EnvFile) GetEnvValue(key, defaultValue string) string {
	if value, ok := e.Values[key]; ok {
		return value
	}
	return defaultValue
}

// ValidateRequired checks that required keys exist in the env file
func (e *EnvFile) ValidateRequired(keys ...string) error {
	missing := []string{}
	for _, key := range keys {
		if _, ok := e.Values[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return errors.Errorf("missing required .env keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

// MaskSecret returns a masked version of a secret for display
func MaskSecret(secret string) string {
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

// RestartContainers runs docker compose down && docker compose up -d in the specified folder
func RestartContainers(folderPath string) error {
	fmt.Println("\nRestarting containers...")

	// Run docker compose down
	downCmd := exec.Command("docker", "compose", "down")
	downCmd.Dir = folderPath
	downCmd.Stdout = os.Stdout
	downCmd.Stderr = os.Stderr
	if err := downCmd.Run(); err != nil {
		return errors.Errorf("failed to stop containers: %w", err)
	}

	// Run docker compose up -d
	upCmd := exec.Command("docker", "compose", "up", "-d")
	upCmd.Dir = folderPath
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		return errors.Errorf("failed to start containers: %w", err)
	}

	fmt.Println("\nContainers restarted successfully.")
	return nil
}
