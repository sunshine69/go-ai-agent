package db

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTSecret is the shared secret used to sign/verify JWT access tokens.
// It is loaded from the JWT_SECRET env var, or generated (and persisted once)
// on start-up. When JWT_SECRET_FILE is set the generated secret is written to
// that file so sessions survive restarts.
var JWTSecret []byte

// loadJWTSecret initialises JWTSecret from the environment. Safe to call once
// at startup; init() below runs it automatically.
func loadJWTSecret() {
	if s := LookupEnvOr("JWT_SECRET", ""); s != "" {
		JWTSecret = []byte(s)
		return
	}
	if f := LookupEnvOr("JWT_SECRET_FILE", ""); f != "" {
		if data, err := readSecretFile(f); err == nil && len(data) > 0 {
			JWTSecret = data
			return
		}
		// Generate and persist so tokens survive a restart.
		s := randomHex(32)
		_ = writeSecretFile(f, []byte(s))
		JWTSecret = []byte(s)
		return
	}
	// Fallback: random secret (sessions do not survive restart).
	JWTSecret = []byte(randomHex(32))
}

// secretFile is the default file used to persist the generated secret.
const secretFile = "jwt_secret.txt"

func init() {
	loadJWTSecret()
	// Ensure the default file exists with a generated secret so subsequent
	// reads of the same path reuse it.
	if LookupEnvOr("JWT_SECRET", "") == "" && LookupEnvOr("JWT_SECRET_FILE", "") == "" {
		if _, err := os.Stat(secretFile); err != nil {
			_ = os.WriteFile(secretFile, []byte(randomHex(32)), 0o600)
			JWTSecret = []byte(randomHex(32))
		} else {
			if data, err := os.ReadFile(secretFile); err == nil && len(data) > 0 {
				JWTSecret = data
			}
		}
	}
}

// IssueToken creates a signed JWT for userID valid for ttl.
func IssueToken(userID int64, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user": userID,
		"exp":  now.Add(ttl).Unix(),
		"iat":  now.Unix(),
		"nbf":  now.Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(JWTSecret)
	if err != nil {
		return "", err
	}
	return signed, nil
}

// ParseToken verifies a signed JWT and returns the user id embedded in it.
func ParseToken(tokenStr string) (int64, error) {
	userID, _, err := ParseTokenAndClaims(tokenStr)
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// ParseTokenAndClaims verifies a signed JWT and returns the user id plus the
// raw claims. Exposed for testing/debugging.
func ParseTokenAndClaims(tokenStr string) (int64, jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	t, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("invalid signing method")
		}
		return JWTSecret, nil
	})
	if err != nil || !t.Valid {
		return 0, nil, err
	}
	uid, ok := claims["user"].(float64)
	if !ok {
		return 0, nil, errors.New("invalid user claim")
	}
	return int64(uid), claims, nil
}

// TokenExpiry is the default lifetime of an issued JWT.
const TokenExpiry = 720 * time.Hour

// LookupEnvOr returns the value of the environment variable named by key, or
// the fallback if it is unset or empty. (config.go defines envKey; this local
// helper is provided so the db package does not depend on config.)
func LookupEnvOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// randomHex returns a random value encoded as a hex string of 2*n bytes.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// readSecretFile reads the bytes of a secret file.
func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// writeSecretFile writes a secret file with 0600 permissions.
func writeSecretFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}
