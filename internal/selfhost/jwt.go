package selfhost

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/go-errors/errors"
	"github.com/golang-jwt/jwt/v5"
)

// Standard Supabase JWT claims
const (
	JWTIssuer     = "supabase"
	JWTExpiry     = 10 * 365 * 24 * time.Hour // 10 years
	RoleAnon      = "anon"
	RoleService   = "service_role"
)

// JWTClaims represents Supabase JWT claims
type JWTClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateJWT creates a signed JWT token with HS256
func GenerateJWT(secret string, role string) (string, error) {
	now := time.Now()

	claims := JWTClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    JWTIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(JWTExpiry)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", errors.Errorf("failed to sign JWT: %w", err)
	}

	return signedToken, nil
}

// GenerateAnonKey creates a new anonymous API key
func GenerateAnonKey(secret string) (string, error) {
	return GenerateJWT(secret, RoleAnon)
}

// GenerateServiceRoleKey creates a new service role API key
func GenerateServiceRoleKey(secret string) (string, error) {
	return GenerateJWT(secret, RoleService)
}

// GenerateSecret generates a cryptographically secure random hex string
func GenerateSecret(length int) (string, error) {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		return "", errors.Errorf("failed to generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateJWTSecret generates a new 64-character JWT secret
func GenerateJWTSecret() (string, error) {
	return GenerateSecret(64)
}

// GeneratePassword generates a secure random password
func GeneratePassword(length int) (string, error) {
	return GenerateSecret(length)
}

// ValidateJWT verifies a JWT token and returns its claims
func ValidateJWT(tokenString, secret string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, errors.Errorf("failed to parse JWT: %w", err)
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid JWT token")
}
