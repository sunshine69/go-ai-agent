package config

import (
	"os"
	"path/filepath"
	"testing"
)

// cleanupEnv unsets every CORS var after each test so tests don't leak state.
func cleanupEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CORS_ENABLED", "CORS_ORIGIN", "CORS_METHODS",
		"CORS_ALLOW_HEADERS", "CORS_MAX_AGE", "CORS_ALLOW_CREDENTIALS",
	} {
		os.Unsetenv(k)
	}
}

// --- .env-file parsing ----------------------------------------------------

// TestCORSFromEnvFile verifies CORS settings are read from a .env file.
func TestCORSFromEnvFile(t *testing.T) {
	cleanupEnv(t)
	t.Cleanup(func() { cleanupEnv(t) })

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	c := Load(envPath)

	if !c.Cors().Enabled {
		t.Errorf("expected CORS_ENABLED to default to true; got %+v", c.Cors())
	}
	if got := c.Cors().Origin; got != "*" {
		t.Errorf("expected CORS_ORIGIN default \"*\"; got %q", got)
	}
	if c.Cors().AllowCreds {
		t.Errorf("expected CORS_ALLOW_CREDENTIALS to default to false; got %+v", c.Cors())
	}
}

// TestCORSFromFile verifies CORS settings are read from a .env file.
func TestCORSFromFile(t *testing.T) {
	cleanupEnv(t)
	t.Cleanup(func() { cleanupEnv(t) })

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeFile(t, envPath,
		"CORS_ENABLED=true\n"+
			"CORS_ORIGIN=https://wails.localhost\n"+
			"CORS_METHODS=GET, POST, OPTIONS\n"+
			"CORS_ALLOW_HEADERS=Content-Type\n"+
			"CORS_MAX_AGE=600\n"+
			"CORS_ALLOW_CREDENTIALS=true\n")

	c := Load(envPath)
	cors := c.Cors()

	if !cors.Enabled {
		t.Error("CORS.Enabled = false, want true")
	}
	if cors.Origin != "https://wails.localhost" {
		t.Errorf("CORS.Origin = %q, want https://wails.localhost", cors.Origin)
	}
	if cors.Methods != "GET, POST, OPTIONS" {
		t.Errorf("CORS.Methods = %q", cors.Methods)
	}
	if cors.AllowHeaders != "Content-Type" {
		t.Errorf("CORS.AllowHeaders = %q", cors.AllowHeaders)
	}
	if cors.MaxAge != 600 {
		t.Errorf("CORS.MaxAge = %d, want 600", cors.MaxAge)
	}
	if !cors.AllowCreds {
		t.Error("CORS.AllowCreds = false, want true")
	}
}

// TestCORSDisabled verifies CORS_ENABLED=false produces Enabled=false and
// lets the middleware pass through untouched.
func TestCORSDisabled(t *testing.T) {
	cleanupEnv(t)
	t.Cleanup(func() { cleanupEnv(t) })

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeFile(t, envPath, "CORS_ENABLED=false\n")

	c := Load(envPath)
	if c.Cors().Enabled {
		t.Error("CORS.Enabled = true, want false")
	}
}

// --- environment-variable parsing ----------------------------------------

// TestCORSFromEnv verifies CORS settings are read from process env vars.
func TestCORSFromEnv(t *testing.T) {
	cleanupEnv(t)
	t.Cleanup(func() { cleanupEnv(t) })

	os.Setenv("CORS_ENABLED", "true")
	os.Setenv("CORS_ORIGIN", ":")
	os.Setenv("CORS_METHODS", "GET, POST, PUT, DELETE")
	os.Setenv("CORS_ALLOW_HEADERS", "Content-Type, Authorization")
	os.Setenv("CORS_MAX_AGE", "7200")
	os.Setenv("CORS_ALLOW_CREDENTIALS", "true")

	c := Load("") // no .env path => reads process env
	cors := c.Cors()

	if !cors.Enabled {
		t.Error("CORS.Enabled = false, want true")
	}
	if cors.Origin != ":" {
		t.Errorf("CORS.Origin = %q, want :", cors.Origin)
	}
	if cors.Methods != "GET, POST, PUT, DELETE" {
		t.Errorf("CORS.Methods = %q", cors.Methods)
	}
	if cors.AllowHeaders != "Content-Type, Authorization" {
		t.Errorf("CORS.AllowHeaders = %q", cors.AllowHeaders)
	}
	if cors.MaxAge != 7200 {
		t.Errorf("CORS.MaxAge = %d, want 7200", cors.MaxAge)
	}
	if !cors.AllowCreds {
		t.Error("CORS.AllowCreds = false, want true")
	}
}

// --- defaults -------------------------------------------------------------

// TestCORSDefaults verifies every CORS field has a sensible default when no
// env vars are set.
func TestCORSDefaults(t *testing.T) {
	cleanupEnv(t)
	t.Cleanup(func() { cleanupEnv(t) })

	c := Load("")
	cors := c.Cors()

	if !cors.Enabled {
		t.Error("default CORS.Enabled = false, want true")
	}
	if cors.Origin != "*" {
		t.Errorf("default CORS.Origin = %q, want *", cors.Origin)
	}
	if cors.Methods == "" {
		t.Error("default CORS.Methods is empty")
	}
	if cors.AllowHeaders == "" {
		t.Error("default CORS.AllowHeaders is empty")
	}
	if cors.MaxAge != 3600 {
		t.Errorf("default CORS.MaxAge = %d, want 3600", cors.MaxAge)
	}
	if cors.AllowCreds {
		t.Error("default CORS.AllowCreds = true, want false")
	}
}

// --- helpers --------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
