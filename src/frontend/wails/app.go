package main

import (
	"context"
	"os"
)

// App struct
type App struct {
	ctx context.Context
	store *SecureStorageState
}

// SecureStorageState holds last UI state so it survives restarts.
// Persisted by Wails in the user's secure storage when available.
type SecureStorageState struct {
	Secrets        map[string]string `json:"secrets"`
	ConversationID string            `json:"conversationId,omitempty"`
	Domain         string            `json:"domain,omitempty"`
	SubCategory    string            `json:"subCategory,omitempty"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		store: &SecureStorageState{
			Secrets: map[string]string{},
		},
	}
}

// Startup is called when the app starts. The context is saved
// so we can call the runtime methods.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx

	// Load persisted UI state from secure storage (best-effort).
	store, err := SecureStorageRead("supersoniciq-ui")
	if err == nil && store != nil {
		a.store.ConversationID = store.ConversationID
		a.store.Domain = store.Domain
		a.store.SubCategory = store.SubCategory
	}

	// Load backend URL from env vars.
	envVars, _ := Environment()
	for k, v := range envVars {
		switch k {
		case "SUPERSONICIQ_BACKEND_URL":
			os.Setenv("VITE_BACKEND_URL", v)
		case "OPENAI_API_KEY":
			os.Setenv("OPENAI_API_KEY", v)
		}
	}
}

// Shutdown is called when the app is shutting down.
func (a *App) Shutdown() {
	// Save current UI state to secure storage if needed
	if a.store != nil && (a.store.Domain != "" || a.store.ConversationID != "") {
		state := &SecureStorageState{
			Secrets:        map[string]string{},
			ConversationID: a.store.ConversationID,
			Domain:         a.store.Domain,
			SubCategory:    a.store.SubCategory,
		}
		if err := SecureStorageWrite("supersoniciq-ui", state); err != nil {
			println("Failed to write secure storage:", err.Error())
		}
	}
}

// SetLastQuestion sets the last question in the app state.
func (a *App) SetLastQuestion(question string) {
	a.store.Secrets["last_question"] = question
}

// GetBackendURL returns the configured backend URL from environment or default.
func (a *App) GetBackendURL() string {
	if url := os.Getenv("VITE_BACKEND_URL"); url != "" {
		return url
	}
	return "http://localhost:8000"
}
