package selfhost

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-errors/errors"
	"github.com/supabase/cli/internal/utils"
)

// LogsOptions configures log viewing behavior
type LogsOptions struct {
	FolderPath string
	Services   []string
	Follow     bool
	Since      string
	Tail       int
}

// RunLogs retrieves and displays logs from the specified services
func RunLogs(ctx context.Context, opts LogsOptions, w io.Writer) error {
	// Read .env file
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return err
	}

	projectName := GetProjectName(env)

	// If no services specified, get all services
	services := opts.Services
	if len(services) == 0 {
		containers, err := GetContainersByProject(ctx, projectName)
		if err != nil {
			return err
		}
		for _, c := range containers {
			if svc := c.Labels["com.docker.compose.service"]; svc != "" {
				services = append(services, svc)
			}
		}
	}

	// Validate services
	for _, svc := range services {
		if !IsValidService(svc) {
			return errors.Errorf("invalid service: %s. Valid services: %v", svc, ValidServices)
		}
	}

	// Parse the --since duration
	since := ""
	if opts.Since != "" {
		since, err = parseSince(opts.Since)
		if err != nil {
			return errors.Errorf("invalid --since value: %w", err)
		}
	}

	// Tail value
	tail := "100"
	if opts.Tail > 0 {
		tail = strconv.Itoa(opts.Tail)
	}

	// If following multiple services, we need to handle them concurrently
	if opts.Follow && len(services) > 1 {
		return streamMultipleLogs(ctx, env, services, since, tail, w)
	}

	// For single service or non-follow mode
	for _, svc := range services {
		if err := streamServiceLogs(ctx, env, svc, since, tail, opts.Follow, len(services) > 1, w); err != nil {
			// Log error but continue with other services
			fmt.Fprintf(w, "Error getting logs for %s: %v\n", svc, err)
		}
	}

	return nil
}

// parseSince parses a "since" string into a format Docker understands
func parseSince(since string) (string, error) {
	// Try parsing as duration (e.g., "10m", "1h", "2h30m")
	if d, err := time.ParseDuration(since); err == nil {
		// Convert to timestamp
		t := time.Now().Add(-d)
		return t.Format(time.RFC3339), nil
	}

	// Try parsing as timestamp
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, since); err == nil {
			return t.Format(time.RFC3339), nil
		}
	}

	// Maybe it's already in Docker's format (e.g., "10m" without Go duration parsing)
	// Try common shortcuts
	if strings.HasSuffix(since, "m") || strings.HasSuffix(since, "h") || strings.HasSuffix(since, "s") {
		return since, nil
	}

	return "", errors.Errorf("cannot parse '%s' as duration or timestamp", since)
}

// streamServiceLogs streams logs from a single service
func streamServiceLogs(ctx context.Context, env *EnvFile, serviceName, since, tail string, follow, prefixOutput bool, w io.Writer) error {
	projectName := GetProjectName(env)

	// Find container for this service
	c, err := GetContainerByService(ctx, projectName, serviceName)
	if err != nil {
		return err
	}

	logsOptions := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
		Timestamps: true,
	}

	if since != "" {
		logsOptions.Since = since
	}

	logs, err := utils.Docker.ContainerLogs(ctx, c.ID, logsOptions)
	if err != nil {
		return errors.Errorf("failed to get container logs: %w", err)
	}
	defer logs.Close()

	// If we need to prefix output with service name
	if prefixOutput {
		prefix := utils.Aqua(fmt.Sprintf("[%s] ", serviceName))
		return copyWithPrefix(logs, w, prefix)
	}

	// Standard output
	_, err = stdcopy.StdCopy(w, w, logs)
	if err != nil && !errors.Is(err, context.Canceled) {
		return errors.Errorf("failed to copy logs: %w", err)
	}

	return nil
}

// streamMultipleLogs streams logs from multiple services concurrently
func streamMultipleLogs(ctx context.Context, env *EnvFile, services []string, since, tail string, w io.Writer) error {
	projectName := GetProjectName(env)

	var wg sync.WaitGroup
	var mu sync.Mutex // Protect concurrent writes to w

	errCh := make(chan error, len(services))

	for _, svc := range services {
		wg.Add(1)
		go func(serviceName string) {
			defer wg.Done()

			c, err := GetContainerByService(ctx, projectName, serviceName)
			if err != nil {
				errCh <- errors.Errorf("[%s] %w", serviceName, err)
				return
			}

			logsOptions := container.LogsOptions{
				ShowStdout: true,
				ShowStderr: true,
				Follow:     true,
				Tail:       tail,
				Timestamps: true,
			}

			if since != "" {
				logsOptions.Since = since
			}

			logs, err := utils.Docker.ContainerLogs(ctx, c.ID, logsOptions)
			if err != nil {
				errCh <- errors.Errorf("[%s] failed to get logs: %w", serviceName, err)
				return
			}
			defer logs.Close()

			// Create a synchronized writer with prefix
			prefix := utils.Aqua(fmt.Sprintf("[%s] ", serviceName))
			syncWriter := &syncPrefixWriter{
				w:      w,
				prefix: prefix,
				mu:     &mu,
			}

			_, err = stdcopy.StdCopy(syncWriter, syncWriter, logs)
			if err != nil && !errors.Is(err, context.Canceled) {
				errCh <- errors.Errorf("[%s] error copying logs: %w", serviceName, err)
			}
		}(svc)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errCh)

	// Collect any errors
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// syncPrefixWriter is a thread-safe writer that prefixes each line
type syncPrefixWriter struct {
	w      io.Writer
	prefix string
	mu     *sync.Mutex
	buf    []byte
}

func (w *syncPrefixWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Add incoming data to buffer
	w.buf = append(w.buf, p...)

	// Process complete lines
	for {
		idx := indexByte(w.buf, '\n')
		if idx < 0 {
			break
		}

		line := w.buf[:idx+1]
		w.buf = w.buf[idx+1:]

		// Write prefixed line
		fmt.Fprint(w.w, w.prefix)
		_, err = w.w.Write(line)
		if err != nil {
			return len(p), err
		}
	}

	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// copyWithPrefix copies from reader to writer, prefixing each line
func copyWithPrefix(r io.Reader, w io.Writer, prefix string) error {
	buf := make([]byte, 8192)
	lineBuf := make([]byte, 0, 1024)
	isMultiplexed := true

	// Docker logs can be multiplexed (have 8-byte header) or raw
	// We'll try to detect and handle both

	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Process the data
			if isMultiplexed && len(data) >= 8 {
				// Try to demultiplex
				// Header format: [STREAM_TYPE, 0, 0, 0, SIZE1, SIZE2, SIZE3, SIZE4]
				for len(data) >= 8 {
					// Check if this looks like a valid header
					streamType := data[0]
					if streamType != 1 && streamType != 2 {
						// Doesn't look multiplexed, switch to raw mode
						isMultiplexed = false
						break
					}

					size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
					if size <= 0 || size > len(data)-8 {
						isMultiplexed = false
						break
					}

					payload := data[8 : 8+size]
					data = data[8+size:]

					// Process payload line by line
					lineBuf = processLines(w, prefix, lineBuf, payload)
				}
			}

			if !isMultiplexed || len(data) > 0 {
				// Process remaining data as raw
				lineBuf = processLines(w, prefix, lineBuf, data)
			}
		}

		if err != nil {
			if err == io.EOF {
				// Flush remaining buffer
				if len(lineBuf) > 0 {
					fmt.Fprint(w, prefix)
					w.Write(lineBuf)
					fmt.Fprintln(w)
				}
				return nil
			}
			return err
		}
	}
}

func processLines(w io.Writer, prefix string, lineBuf, data []byte) []byte {
	for _, b := range data {
		if b == '\n' {
			fmt.Fprint(w, prefix)
			w.Write(lineBuf)
			fmt.Fprintln(w)
			lineBuf = lineBuf[:0]
		} else {
			lineBuf = append(lineBuf, b)
		}
	}
	return lineBuf
}
