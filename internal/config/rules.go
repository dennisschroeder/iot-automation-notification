package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Condition struct {
	Type     string      `yaml:"type"`     // "binary_sensor", "time", "kv_store"
	Operator string      `yaml:"operator"` // "<", ">", "==", "between"
	Value    interface{} `yaml:"value"`    // e.g. 50, "07:00-22:00", or false
	Device   string      `yaml:"device"`   // the key in KV store or entity id
	Default  interface{} `yaml:"default"`  // Fallback if key not in KV store
}

type Trigger struct {
	Device  string   `yaml:"device,omitempty"`  // primary single ID
	Devices []string `yaml:"devices,omitempty"` // array of device names
	Type    string   `yaml:"type"`              // "binary_sensor", "light"
	State   string   `yaml:"state"`             // "ON" or "OFF"
}

type Action struct {
	Channel string                 `yaml:"channel"` // "mobile_app", "google_home", "tv"
	Target  string                 `yaml:"target"`  // e.g. "dennis_iphone", "living_room_speaker"
	Title   string                 `yaml:"title"`
	Message string                 `yaml:"message"`
	Data    map[string]interface{} `yaml:"data"`
}

type NotificationRule struct {
	ID         string      `yaml:"id"`
	Trigger    Trigger     `yaml:"trigger"`
	Conditions []Condition `yaml:"conditions"`
	Actions    []Action    `yaml:"actions"`
}

type Config struct {
	Notifications []NotificationRule `yaml:"notifications"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	// Normalize Trigger Device IDs
	for i := range cfg.Notifications {
		rule := &cfg.Notifications[i]
		if len(rule.Trigger.Devices) == 0 && rule.Trigger.Device != "" {
			rule.Trigger.Devices = []string{rule.Trigger.Device}
		}
		if rule.Trigger.Type == "" {
			rule.Trigger.Type = "binary_sensor"
		}
	}

	return &cfg, nil
}
