package mode

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/enowdev/succubus/internal/store"
)

// Config is the daemon's optional settings file, at ~/.succubus/config.json.
//
// It exists for the handful of things that cannot be inferred: where to send
// notifications, and which enforcement level to run at. Everything else is
// derived from the project or passed as a flag.
type Config struct {
	// Webhooks reach a person — Slack, Discord, a phone. They cannot reach an
	// agent: agents have no endpoint and no process between turns.
	Webhooks []store.WebhookConfig `json:"webhooks,omitempty"`
	// Enforcement is off, nag, or block. The SUCCUBUS_ENFORCEMENT environment
	// variable wins over this.
	Enforcement string `json:"enforcement,omitempty"`

	// AutoWake spawns a short headless turn when an agent is addressed by name,
	// instead of leaving the message until that session is next prompted.
	//
	// Off by default: waking an agent starts a real, billable turn, and that
	// should be a decision rather than a surprise.
	AutoWake bool `json:"auto_wake,omitempty"`
	// AutoWakeDelaySec waits before waking, so a burst of messages produces one
	// turn rather than several, and so a human still typing is not answered
	// mid-sentence.
	AutoWakeDelaySec int `json:"auto_wake_delay_sec,omitempty"`
}

// ConfigPath is ~/.succubus/config.json.
func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".succubus", "config.json")
}

// LoadConfig reads the settings file. A missing file is not an error — it just
// means nothing has been configured.
func LoadConfig() (*Config, error) {
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return &Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return &Config{}, err
	}
	return &c, nil
}

// SaveConfig writes the settings file atomically.
func SaveConfig(c *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// Webhook URLs carry secrets in the path, so keep the file private.
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
