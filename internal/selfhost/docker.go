package selfhost

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/go-errors/errors"
	"github.com/supabase/cli/internal/utils"
)

const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

// GetProjectName reads COMPOSE_PROJECT_NAME from .env or returns "supabase"
func GetProjectName(env *EnvFile) string {
	return env.GetEnvValue("COMPOSE_PROJECT_NAME", "supabase")
}

// GetContainersByProject lists containers for a compose project
func GetContainersByProject(ctx context.Context, projectName string) ([]types.Container, error) {
	args := filters.NewArgs(
		filters.Arg("label", composeProjectLabel+"="+projectName),
	)
	containers, err := utils.Docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, errors.Errorf("failed to list containers: %w", err)
	}
	return containers, nil
}

// GetContainerByService finds a container by service name within a compose project
func GetContainerByService(ctx context.Context, projectName, serviceName string) (*types.Container, error) {
	args := filters.NewArgs(
		filters.Arg("label", composeProjectLabel+"="+projectName),
		filters.Arg("label", composeServiceLabel+"="+serviceName),
	)
	containers, err := utils.Docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, errors.Errorf("failed to list containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, errors.Errorf("container for service %s not found in project %s", serviceName, projectName)
	}
	return &containers[0], nil
}

// GetContainerIDByService finds container ID for a service name
func GetContainerIDByService(ctx context.Context, projectName, serviceName string) (string, error) {
	c, err := GetContainerByService(ctx, projectName, serviceName)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// DBConnectionConfig holds database connection details
type DBConnectionConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// GetDBConnectionConfig returns DB connection details from .env
func GetDBConnectionConfig(env *EnvFile) DBConnectionConfig {
	return DBConnectionConfig{
		Host:     "localhost",
		Port:     env.GetEnvValue("DB_PORT", "5432"),
		User:     env.GetEnvValue("POSTGRES_USER", "postgres"),
		Password: env.GetEnvValue("POSTGRES_PASSWORD", ""),
		Database: env.GetEnvValue("POSTGRES_DB", "postgres"),
	}
}

// ValidServices lists valid service names for self-hosted Supabase
var ValidServices = []string{
	"db",
	"kong",
	"auth",
	"rest",
	"realtime",
	"storage",
	"imgproxy",
	"meta",
	"functions",
	"analytics",
	"studio",
	"vector",
	"pooler",
}

// IsValidService checks if a service name is valid
func IsValidService(service string) bool {
	for _, s := range ValidServices {
		if s == service {
			return true
		}
	}
	return false
}

// GetServiceContainerName returns the container name for a service
func GetServiceContainerName(env *EnvFile, serviceName string) string {
	projectName := GetProjectName(env)

	// Special case for realtime which may use REALTIME_CONTAINER_NAME
	if serviceName == "realtime" {
		if name := env.GetEnvValue("REALTIME_CONTAINER_NAME", ""); name != "" {
			return name
		}
	}

	return projectName + "-" + serviceName + "-1"
}
