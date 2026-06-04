package app

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// LoopConfig holds operator-controlled settings for the afk loop runner.
// It is loaded from ~/.config/afk/loop.yaml; defaults are written on first run.
type LoopConfig struct {
	// Command is the agent command template executed per task, e.g.
	// "claude -p {{.Prompt}}". No built-in default — caller fails closed when empty.
	Command string `yaml:"command"`

	// PromptTemplate is a text/template string rendered against a task to
	// produce the prompt injected into Command.
	PromptTemplate string `yaml:"prompt_template"`

	// TaskTimeout caps a single task execution. Default: 10m.
	TaskTimeout time.Duration `yaml:"task_timeout"`

	// Cooldown is the pause between loop ticks when no task was found. Default: 5s.
	Cooldown time.Duration `yaml:"cooldown"`

	// MaxConsecutiveFailures halts the loop after this many back-to-back
	// task failures. Default: 3.
	MaxConsecutiveFailures int `yaml:"max_consecutive_failures"`

	// Lease is the exclusive claim duration taken on each task. Default: 30m.
	Lease time.Duration `yaml:"lease"`

	// HeartbeatInterval controls how often the loop extends the lease on a
	// running task. Default: 2m.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`

	// Worker is the worker identity string. When empty the caller derives one
	// (e.g. "loop-<pid>").
	Worker string `yaml:"worker"`
}

// defaultLoopConfig returns the built-in defaults written on first run and used
// as the in-memory fallback when the config file is absent or unreadable.
func defaultLoopConfig() LoopConfig {
	return LoopConfig{
		// Command intentionally empty — fail-closed: caller must detect and error.
		PromptTemplate: `Task ID: {{.ID}}
Status:  {{.Status}}

{{.Body}}
{{- if .Tags}}

Tags: {{range $i, $t := .Tags}}{{if $i}}, {{end}}{{$t}}{{end}}
{{- end}}
{{- if .Stage}}
Stage: {{.Stage}}
{{- end}}`,
		TaskTimeout:            10 * time.Minute,
		Cooldown:               5 * time.Second,
		MaxConsecutiveFailures: 3,
		Lease:                  30 * time.Minute,
		HeartbeatInterval:      2 * time.Minute,
		Worker:                 "",
	}
}

var (
	loopConfigOnce   sync.Once
	loadedLoopConfig LoopConfig
)

// LoadLoopConfig returns the loop configuration, loading it once on first use.
// It writes defaults to ~/.config/afk/loop.yaml when the file is absent and
// falls back to built-in defaults on any error, so the loop never fails to
// start due to config problems.
func LoadLoopConfig() LoopConfig {
	loopConfigOnce.Do(func() {
		loadedLoopConfig = loadLoopConfig()
	})
	return loadedLoopConfig
}

// loopConfigPath returns the on-disk path for loop.yaml under the user config
// dir, or an error when the config dir is unavailable.
func loopConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "afk", "loop.yaml"), nil
}

// loadLoopConfig reads loop.yaml, writing defaults on first run.
// Any failure (no config dir, unreadable, malformed YAML) falls back to the
// built-in defaults.
func loadLoopConfig() LoopConfig {
	defaults := defaultLoopConfig()
	path, err := loopConfigPath()
	if err != nil {
		return defaults
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeDefaultLoopConfig(path, defaults)
		}
		return defaults
	}

	var cfg LoopConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaults
	}
	return cfg
}

// writeDefaultLoopConfig serializes defaults to path, creating parent
// directories as needed. Errors are ignored: a failed write must not block
// the loop, and the in-memory defaults are used regardless.
func writeDefaultLoopConfig(path string, defaults LoopConfig) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return
	}
	data, err := yaml.Marshal(defaults)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
