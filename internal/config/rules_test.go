package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigDebounce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `notifications:
  - id: "custom"
    debounce: 45s
    trigger: {device: "a", state: "ON"}
  - id: "default"
    trigger: {device: "b", state: "ON"}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Notifications[0].Debounce; got != 45*time.Second {
		t.Errorf("custom debounce: got %v, want 45s", got)
	}
	if got := cfg.Notifications[1].Debounce; got != DefaultDebounce {
		t.Errorf("default debounce: got %v, want %v", got, DefaultDebounce)
	}
}
