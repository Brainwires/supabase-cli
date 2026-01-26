package selfhost

import (
	"fmt"

	"github.com/go-errors/errors"
	"github.com/supabase/cli/internal/utils"
)

// RollOptions configures key regeneration behavior
type RollOptions struct {
	FolderPath    string
	IncludeSecret bool
	SkipConfirm   bool
}

// RollResult contains the results of a key roll operation
type RollResult struct {
	OldJWTSecret      string
	NewJWTSecret      string
	JWTSecretChanged  bool
	OldAnonKey        string
	NewAnonKey        string
	OldServiceRoleKey string
	NewServiceRoleKey string
}

// RunRoll regenerates JWT-based API keys
func RunRoll(opts RollOptions) (*RollResult, error) {
	// Read current .env
	env, err := ReadEnvFile(opts.FolderPath)
	if err != nil {
		return nil, err
	}

	// Validate required keys exist
	if err := env.ValidateRequired("JWT_SECRET", "ANON_KEY", "SERVICE_ROLE_KEY"); err != nil {
		return nil, err
	}

	result := &RollResult{
		OldJWTSecret:      env.Values["JWT_SECRET"],
		OldAnonKey:        env.Values["ANON_KEY"],
		OldServiceRoleKey: env.Values["SERVICE_ROLE_KEY"],
	}

	updates := make(map[string]string)
	jwtSecret := result.OldJWTSecret

	// Optionally regenerate JWT_SECRET
	if opts.IncludeSecret {
		newSecret, err := GenerateJWTSecret()
		if err != nil {
			return nil, errors.Errorf("failed to generate JWT secret: %w", err)
		}
		jwtSecret = newSecret
		updates["JWT_SECRET"] = newSecret
		result.NewJWTSecret = newSecret
		result.JWTSecretChanged = true
	} else {
		result.NewJWTSecret = jwtSecret
	}

	// Generate new ANON_KEY
	anonKey, err := GenerateAnonKey(jwtSecret)
	if err != nil {
		return nil, errors.Errorf("failed to generate anon key: %w", err)
	}
	updates["ANON_KEY"] = anonKey
	result.NewAnonKey = anonKey

	// Generate new SERVICE_ROLE_KEY
	serviceKey, err := GenerateServiceRoleKey(jwtSecret)
	if err != nil {
		return nil, errors.Errorf("failed to generate service role key: %w", err)
	}
	updates["SERVICE_ROLE_KEY"] = serviceKey
	result.NewServiceRoleKey = serviceKey

	// Write updates to .env
	if err := UpdateEnvFile(env, updates); err != nil {
		return nil, err
	}

	return result, nil
}

// PrintRollResult displays the results of a key roll operation
func PrintRollResult(result *RollResult) {
	fmt.Println()
	fmt.Println(utils.Bold("=== API Key Roll Results ==="))
	fmt.Println()

	if result.JWTSecretChanged {
		fmt.Println(utils.Yellow("JWT_SECRET regenerated"))
		fmt.Printf("  Old: %s\n", MaskSecret(result.OldJWTSecret))
		fmt.Printf("  New: %s\n", MaskSecret(result.NewJWTSecret))
		fmt.Println()
	}

	fmt.Println(utils.Green("ANON_KEY regenerated"))
	fmt.Printf("  Old: %s\n", MaskSecret(result.OldAnonKey))
	fmt.Printf("  New: %s\n", MaskSecret(result.NewAnonKey))
	fmt.Println()

	fmt.Println(utils.Green("SERVICE_ROLE_KEY regenerated"))
	fmt.Printf("  Old: %s\n", MaskSecret(result.OldServiceRoleKey))
	fmt.Printf("  New: %s\n", MaskSecret(result.NewServiceRoleKey))
	fmt.Println()

	fmt.Println(utils.Yellow("Important:"))
	fmt.Println("  Update your application with the new keys:")
	if result.JWTSecretChanged {
		fmt.Println(utils.Red("  WARNING: JWT_SECRET changed - ALL existing tokens are now invalid!"))
	}
	fmt.Println("  - Update SUPABASE_ANON_KEY in your client applications")
	fmt.Println("  - Update SUPABASE_SERVICE_ROLE_KEY in your server applications")
}
