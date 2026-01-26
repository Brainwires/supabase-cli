package selfhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/go-errors/errors"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/supabase/cli/internal/utils"
)

// HealthOptions configures health check behavior
type HealthOptions struct {
	FolderPath string
	JSON       bool
	Watch      int // Refresh interval in seconds (0 = no watch)
}

// ServiceHealth represents the health status of a single service
type ServiceHealth struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Health    string `json:"health"`
	Uptime    string `json:"uptime"`
	Image     string `json:"image"`
	Container string `json:"container_id"`
}

// HealthResult contains the overall health check results
type HealthResult struct {
	Services    []ServiceHealth `json:"services"`
	Healthy     int             `json:"healthy"`
	Unhealthy   int             `json:"unhealthy"`
	Stopped     int             `json:"stopped"`
	ProjectName string          `json:"project_name"`
}

// RunHealth performs health checks on all services
func RunHealth(ctx context.Context, opts HealthOptions) (*HealthResult, error) {
	// Read .env file
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return nil, err
	}

	return checkHealth(ctx, env)
}

// checkHealth performs the actual health check
func checkHealth(ctx context.Context, env *EnvFile) (*HealthResult, error) {
	projectName := GetProjectName(env)

	// Get all containers for this project
	containers, err := GetContainersByProject(ctx, projectName)
	if err != nil {
		return nil, err
	}

	result := &HealthResult{
		Services:    make([]ServiceHealth, 0),
		ProjectName: projectName,
	}

	for _, c := range containers {
		// Get service name from labels
		serviceName := c.Labels["com.docker.compose.service"]
		if serviceName == "" {
			continue
		}

		// Get detailed container info
		inspect, err := utils.Docker.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}

		health := ServiceHealth{
			Name:      serviceName,
			Status:    c.State,
			Container: c.ID[:12],
			Image:     extractImageVersion(c.Image),
		}

		// Determine health status
		if c.State != "running" {
			health.Health = "stopped"
			health.Uptime = "-"
			result.Stopped++
		} else {
			// Calculate uptime
			if inspect.State.StartedAt != "" {
				startTime, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt)
				if err == nil {
					health.Uptime = formatDuration(time.Since(startTime))
				}
			}

			// Check health status
			if inspect.State.Health != nil {
				health.Health = inspect.State.Health.Status
				switch inspect.State.Health.Status {
				case types.Healthy:
					result.Healthy++
				case types.Unhealthy:
					result.Unhealthy++
				default:
					// starting or none
					result.Healthy++ // Count as healthy if running without health check
				}
			} else {
				health.Health = "none"
				result.Healthy++ // Count as healthy if running without health check
			}
		}

		result.Services = append(result.Services, health)
	}

	return result, nil
}

// extractImageVersion extracts the version/tag from an image name
func extractImageVersion(image string) string {
	// Remove sha256 digest if present
	if idx := strings.Index(image, "@"); idx != -1 {
		image = image[:idx]
	}

	// Get the tag part
	parts := strings.Split(image, ":")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return "latest"
}

// formatDuration formats a duration in a human-readable format
func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// PrintHealthResult displays the health check results
func PrintHealthResult(w io.Writer, result *HealthResult, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	return printHealthTable(w, result)
}

// printHealthTable prints a formatted table of health status
func printHealthTable(w io.Writer, result *HealthResult) error {
	table := tablewriter.NewTable(w,
		tablewriter.WithSymbols(tw.NewSymbols(tw.StyleRounded)),
		tablewriter.WithConfig(tablewriter.Config{
			Header: tw.CellConfig{
				Formatting: tw.CellFormatting{
					AutoFormat: tw.Off,
				},
				Alignment: tw.CellAlignment{
					Global: tw.AlignLeft,
				},
				Filter: tw.CellFilter{
					Global: func(s []string) []string {
						for i := range s {
							s[i] = utils.Bold(s[i])
						}
						return s
					},
				},
			},
			Row: tw.CellConfig{
				Alignment: tw.CellAlignment{
					Global: tw.AlignLeft,
				},
			},
		}),
		tablewriter.WithHeader([]string{"SERVICE", "STATUS", "HEALTH", "UPTIME", "IMAGE"}),
	)

	for _, svc := range result.Services {
		status := svc.Status
		health := svc.Health

		// Color code status
		switch svc.Status {
		case "running":
			status = utils.Green(svc.Status)
		case "exited", "dead":
			status = utils.Red(svc.Status)
		default:
			status = utils.Yellow(svc.Status)
		}

		// Color code health
		switch svc.Health {
		case "healthy":
			health = utils.Green(svc.Health)
		case "unhealthy":
			health = utils.Red(svc.Health)
		case "starting":
			health = utils.Yellow(svc.Health)
		case "stopped":
			health = utils.Red(svc.Health)
		default:
			health = utils.Aqua(svc.Health)
		}

		if err := table.Append(svc.Name, status, health, svc.Uptime, svc.Image); err != nil {
			return errors.Errorf("failed to append row: %w", err)
		}
	}

	if err := table.Render(); err != nil {
		return errors.Errorf("failed to render table: %w", err)
	}

	// Print summary
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %s healthy, %s unhealthy, %s stopped\n",
		utils.Green(fmt.Sprintf("%d", result.Healthy)),
		utils.Red(fmt.Sprintf("%d", result.Unhealthy)),
		utils.Yellow(fmt.Sprintf("%d", result.Stopped)),
	)

	return nil
}

// WatchHealth continuously monitors health status
func WatchHealth(ctx context.Context, opts HealthOptions, w io.Writer) error {
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(time.Duration(opts.Watch) * time.Second)
	defer ticker.Stop()

	// Run immediately first time
	if err := runAndPrintHealth(ctx, env, w, opts.JSON); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Clear screen (ANSI escape code)
			fmt.Fprint(w, "\033[H\033[2J")

			if err := runAndPrintHealth(ctx, env, w, opts.JSON); err != nil {
				fmt.Fprintf(w, "Error: %v\n", err)
			}

			fmt.Fprintf(w, "\n%s Refreshing every %d seconds (Ctrl+C to stop)\n",
				utils.Aqua("Watching:"), opts.Watch)
		}
	}
}

func runAndPrintHealth(ctx context.Context, env *EnvFile, w io.Writer, asJSON bool) error {
	result, err := checkHealth(ctx, env)
	if err != nil {
		return err
	}

	return PrintHealthResult(w, result, asJSON)
}
