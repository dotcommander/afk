package task

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// validationConfig holds the phrase lists that ValidateBody and the
// generated-candidate churn check consult. The lists are behavioral
// configuration data (per the workspace "Configuration Data" rule), so they
// live in ~/.config/afk/validation.yaml rather than as Go source literals.
type validationConfig struct {
	ExactBodies      []string `yaml:"exact_bodies"`
	BodyPhrases      []string `yaml:"body_phrases"`
	AutonomyPhrases  []string `yaml:"autonomy_phrases"`
	VagueSoftware    []string `yaml:"vague_software_phrases"`
	GeneratedPhrases []string `yaml:"generated_phrases"`
}

// defaultValidationConfig is the built-in fallback written to disk on first run
// and used when the config file is absent or unreadable. It reproduces the
// historical hardcoded phrase lists exactly.
func defaultValidationConfig() validationConfig {
	return validationConfig{
		ExactBodies: []string{
			"continue this",
			"fix the thing",
			"make it better",
			"do something",
			"whatever",
			"pick my nose",
		},
		BodyPhrases: []string{
			"pick my nose",
			"brush my teeth",
			"wash my hair",
		},
		AutonomyPhrases: []string{
			"askuserquestion",
			"hitl gate",
			"hitl-dependent",
			"human in the loop",
			"post this question to the user",
			"wait for answer",
			"wait for user answer",
		},
		VagueSoftware: []string{
			"polish the game play",
			"polish the gameplay",
			"work on polishing",
		},
		GeneratedPhrases: []string{
			"and/or",
			"clean up",
			"cleanup",
			"etc.",
			"general polish",
			"improve overall",
			"investigate broadly",
			"make better",
			"nice to have",
			"polish",
			"refactor broadly",
			"style-only",
			"x or y",
		},
	}
}

var (
	validationConfigOnce   sync.Once
	loadedValidationConfig validationConfig
)

// validationCfg returns the loaded validation configuration, loading it once on
// first use. Loading writes defaults to ~/.config/afk/validation.yaml when the
// file is absent and falls back to the built-in defaults on any error so that
// validation never fails open or panics due to config problems.
func validationCfg() validationConfig {
	validationConfigOnce.Do(func() {
		loadedValidationConfig = loadValidationConfig()
	})
	return loadedValidationConfig
}

// validationConfigPath returns the on-disk path for validation.yaml under the
// user config dir, or "" (with no error) when the config dir is unavailable.
func validationConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "afk", "validation.yaml")
}

// loadValidationConfig reads validation.yaml, writing defaults on first run.
// Any failure (no config dir, unreadable, malformed YAML) falls back to the
// built-in defaults.
func loadValidationConfig() validationConfig {
	defaults := defaultValidationConfig()
	path := validationConfigPath()
	if path == "" {
		return defaults
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeDefaultValidationConfig(path, defaults)
		}
		return defaults
	}

	var cfg validationConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaults
	}
	return cfg
}

// writeDefaultValidationConfig serializes defaults to path, creating parent
// directories as needed. Errors are ignored: a failed write must not block
// validation, and the in-memory defaults are used regardless.
func writeDefaultValidationConfig(path string, defaults validationConfig) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return
	}
	data, err := yaml.Marshal(defaults)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
