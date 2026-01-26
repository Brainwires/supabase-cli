package selfhost

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v4"
	"github.com/supabase/cli/internal/utils"
)

// PasswdOptions configures password change behavior
type PasswdOptions struct {
	FolderPath   string
	ChangeDB     bool
	ChangeStudio bool
	DBUser       string
	Generate     bool
	Password     string
	SkipConfirm  bool
}

// PasswdResult contains the results of a password change operation
type PasswdResult struct {
	DBPasswordChanged     bool
	StudioPasswordChanged bool
	OldDBPassword         string
	NewDBPassword         string
	OldStudioPassword     string
	NewStudioPassword     string
}

// ValidDBUsers lists valid database users for password changes
var ValidDBUsers = []string{"postgres", "authenticator", "supabase_admin", "service_role"}

// RunPasswd changes database and/or studio passwords
func RunPasswd(ctx context.Context, opts PasswdOptions) (*PasswdResult, error) {
	// Validate options
	if !opts.ChangeDB && !opts.ChangeStudio {
		return nil, errors.New("must specify --db and/or --studio")
	}

	if opts.DBUser != "" && !isValidDBUser(opts.DBUser) {
		return nil, errors.Errorf("invalid database user: %s. Valid users: %v", opts.DBUser, ValidDBUsers)
	}

	// Default to postgres user
	if opts.DBUser == "" {
		opts.DBUser = "postgres"
	}

	// Read current .env
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return nil, err
	}

	result := &PasswdResult{}
	updates := make(map[string]string)

	// Handle database password
	if opts.ChangeDB {
		if err := env.ValidateRequired("POSTGRES_PASSWORD"); err != nil {
			return nil, err
		}
		result.OldDBPassword = env.Values["POSTGRES_PASSWORD"]

		// Get or generate new password
		newPassword := opts.Password
		if opts.Generate {
			generated, err := GeneratePassword(64)
			if err != nil {
				return nil, err
			}
			newPassword = generated
		}
		if newPassword == "" {
			return nil, errors.New("password is required (use --generate or --password)")
		}

		// Change password in database if user is postgres
		if opts.DBUser == "postgres" {
			if err := changeDBPassword(ctx, env, opts.DBUser, newPassword); err != nil {
				return nil, err
			}
		} else {
			// For other users, we need to connect first then change
			if err := changeDBPassword(ctx, env, opts.DBUser, newPassword); err != nil {
				return nil, err
			}
		}

		// Update .env only for postgres user (main password)
		if opts.DBUser == "postgres" {
			updates["POSTGRES_PASSWORD"] = newPassword
		}

		result.NewDBPassword = newPassword
		result.DBPasswordChanged = true
	}

	// Handle studio password
	if opts.ChangeStudio {
		if err := env.ValidateRequired("DASHBOARD_PASSWORD"); err != nil {
			return nil, err
		}
		result.OldStudioPassword = env.Values["DASHBOARD_PASSWORD"]

		// Get or generate new password
		newPassword := opts.Password
		if opts.Generate {
			generated, err := GeneratePassword(64)
			if err != nil {
				return nil, err
			}
			newPassword = generated
		}
		if newPassword == "" {
			return nil, errors.New("password is required (use --generate or --password)")
		}

		updates["DASHBOARD_PASSWORD"] = newPassword
		result.NewStudioPassword = newPassword
		result.StudioPasswordChanged = true
	}

	// Write updates to .env
	if len(updates) > 0 {
		if err := UpdateEnvFile(env, updates); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// changeDBPassword connects to the database and changes a user's password
func changeDBPassword(ctx context.Context, env *EnvFile, user, newPassword string) error {
	// Get connection info from .env
	// DB_PORT is the externally mapped port for Docker setups
	// POSTGRES_PORT is the internal container port (usually 5432)
	// We need DB_PORT to connect from outside the container
	port := env.GetEnvValue("DB_PORT", "5432")
	database := env.GetEnvValue("POSTGRES_DB", "postgres")
	password := env.GetEnvValue("POSTGRES_PASSWORD", "")

	if password == "" {
		return errors.New("POSTGRES_PASSWORD not found in .env")
	}

	// Build connection string - connect to localhost since we're running locally
	// (Docker maps the container port to localhost)
	// Use supabase_admin user which has superuser privileges in Supabase setups
	// It shares the same password as postgres (POSTGRES_PASSWORD)
	connStr := fmt.Sprintf("host=localhost port=%s user=supabase_admin password=%s dbname=%s sslmode=disable",
		port, password, database)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return errors.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close(ctx)

	// Change the password using ALTER USER
	// Note: We use format string here because the password needs to be quoted properly
	query := fmt.Sprintf("ALTER USER %s PASSWORD %s", quoteIdentifier(user), quoteLiteral(newPassword))
	_, err = conn.Exec(ctx, query)
	if err != nil {
		return errors.Errorf("failed to change password for user %s: %w", user, err)
	}

	// In Supabase setups, postgres and supabase_admin share the same password (POSTGRES_PASSWORD)
	// When changing postgres password, we must also change supabase_admin to keep them in sync
	if user == "postgres" {
		query = fmt.Sprintf("ALTER USER %s PASSWORD %s", quoteIdentifier("supabase_admin"), quoteLiteral(newPassword))
		_, err = conn.Exec(ctx, query)
		if err != nil {
			return errors.Errorf("failed to sync supabase_admin password: %w", err)
		}
	}

	return nil
}

// quoteIdentifier quotes a PostgreSQL identifier (table name, user name, etc.)
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteLiteral quotes a PostgreSQL string literal
func quoteLiteral(value string) string {
	// Escape single quotes by doubling them
	escaped := strings.ReplaceAll(value, `'`, `''`)
	return `'` + escaped + `'`
}

func isValidDBUser(user string) bool {
	for _, valid := range ValidDBUsers {
		if user == valid {
			return true
		}
	}
	return false
}

// PrintPasswdResult displays the results of a password change
func PrintPasswdResult(result *PasswdResult) {
	fmt.Println()
	fmt.Println(utils.Bold("=== Password Change Results ==="))
	fmt.Println()

	if result.DBPasswordChanged {
		fmt.Println(utils.Green("Database password changed successfully"))
		fmt.Printf("  Old: %s\n", MaskSecret(result.OldDBPassword))
		fmt.Printf("  New: %s\n", MaskSecret(result.NewDBPassword))
		fmt.Println()
	}

	if result.StudioPasswordChanged {
		fmt.Println(utils.Green("Studio dashboard password changed successfully"))
		fmt.Printf("  Old: %s\n", MaskSecret(result.OldStudioPassword))
		fmt.Printf("  New: %s\n", MaskSecret(result.NewStudioPassword))
		fmt.Println()
	}

	if result.DBPasswordChanged {
		fmt.Println(utils.Yellow("Important:"))
		fmt.Println("  Update any applications using the old database password")
	}
}
