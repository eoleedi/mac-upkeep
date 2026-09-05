package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultConcurrency = 3

type Config struct {
	Concurrency int
	Tasks       []Task
}

type Task struct {
	ID       string
	Name     string
	Command  string
	Enabled  bool
	Requires []string
}

type rawConfig struct {
	Concurrency *int      `toml:"concurrency"`
	Tasks       []rawTask `toml:"tasks"`
}

type rawTask struct {
	ID       string   `toml:"id"`
	Name     string   `toml:"name"`
	Command  string   `toml:"command"`
	Enabled  *bool    `toml:"enabled"`
	Requires []string `toml:"requires"`
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ua", "config.toml"), nil
}

func Load(path string) (Config, error) {
	var raw rawConfig
	metadata, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("config %s: unknown key %s", path, undecoded[0])
	}

	concurrency := DefaultConcurrency
	if raw.Concurrency != nil {
		concurrency = *raw.Concurrency
	}
	if concurrency < 1 {
		return Config{}, fmt.Errorf("config %s: concurrency: must be at least 1", path)
	}
	if len(raw.Tasks) == 0 {
		return Config{}, fmt.Errorf("config %s: tasks: must contain at least one task", path)
	}

	config := Config{Concurrency: concurrency, Tasks: make([]Task, 0, len(raw.Tasks))}
	ids := make(map[string]struct{}, len(raw.Tasks))
	for i, rawTask := range raw.Tasks {
		id := strings.TrimSpace(rawTask.ID)
		if id == "" {
			return Config{}, taskError(path, i, "id", "must not be empty")
		}
		if _, exists := ids[id]; exists {
			return Config{}, taskError(path, i, "id", "must be unique")
		}
		ids[id] = struct{}{}

		if strings.TrimSpace(rawTask.Name) == "" {
			return Config{}, taskError(path, i, "name", "must not be empty")
		}
		if strings.TrimSpace(rawTask.Command) == "" {
			return Config{}, taskError(path, i, "command", "must not be empty")
		}

		requirements := make(map[string]struct{}, len(rawTask.Requires))
		for _, requirement := range rawTask.Requires {
			requirement = strings.TrimSpace(requirement)
			if _, exists := requirements[requirement]; exists {
				return Config{}, taskError(path, i, "requires", fmt.Sprintf("duplicate requirement %q", requirement))
			}
			requirements[requirement] = struct{}{}
		}

		enabled := true
		if rawTask.Enabled != nil {
			enabled = *rawTask.Enabled
		}
		config.Tasks = append(config.Tasks, Task{
			ID:       rawTask.ID,
			Name:     rawTask.Name,
			Command:  rawTask.Command,
			Enabled:  enabled,
			Requires: rawTask.Requires,
		})
	}

	return config, nil
}

func taskError(path string, index int, field, reason string) error {
	return fmt.Errorf("config %s: tasks[%d].%s: %s", path, index, field, reason)
}
