package main

import (
	"encoding/json"
	"os"

	"github.com/samber/lo"
)

// SecureStorageRead attempts to read persisted UI state from secure storage.
func SecureStorageRead(key string) (*SecureStorageState, error) {
	data, err := os.ReadFile(getStateFilePath())
	if err != nil || len(data) == 0 {
		return nil, err
	}

	var state *SecureStorageState
	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, err
	}
	return state, nil
}

// SecureStorageWrite attempts to persist UI state to secure storage.
func SecureStorageWrite(key string, state *SecureStorageState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(getStateFilePath(), data, 0644)
}

// getStateFilePath returns the path for our persisted app state file.
func getStateFilePath() string {
	dir, _ := os.UserConfigDir()
	return lo.Ternary(dir != "", dir+"/supersoniciq/state.json", "./state.json")
}
