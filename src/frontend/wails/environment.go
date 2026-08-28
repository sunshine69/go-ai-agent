package main

import (
	"os"
)

// Environment returns a map of environment variables relevant to the app.
func Environment() (map[string]string, error) {
	envVars := make(map[string]string)

	requiredKeys := []string{
		"SUPERSONICIQ_BACKEND_URL",
		"OPENAI_API_KEY",
	}

	for _, key := range requiredKeys {
		if val := os.Getenv(key); val != "" {
			envVars[key] = val
		}
	}

	return envVars, nil
}
