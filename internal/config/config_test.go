package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaultsAndPreservesOrder(t *testing.T) {
	path := writeConfig(t, `
[[tasks]]
id = "brew"
name = "Homebrew"
command = "brew upgrade"

[[tasks]]
id = "npm"
name = "npm"
command = "npm update -g"
enabled = false
`)

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Concurrency != 3 {
		t.Fatalf("Concurrency = %d, want 3", got.Concurrency)
	}
	if len(got.Tasks) != 2 || got.Tasks[0].ID != "brew" || !got.Tasks[0].Enabled || got.Tasks[1].Enabled {
		t.Fatalf("unexpected tasks: %#v", got.Tasks)
	}
}

func TestLoadPreservesCommandBody(t *testing.T) {
	const command = "  brew upgrade &&\n  brew cleanup  "
	path := writeConfig(t, `
concurrency = 1
[[tasks]]
id = "brew"
name = "Homebrew"
command = """  brew upgrade &&
  brew cleanup  """
requires = ["brew", "git"]
`)

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tasks[0].Command != command {
		t.Fatalf("Command = %q, want %q", got.Tasks[0].Command, command)
	}
	if len(got.Tasks[0].Requires) != 2 || got.Tasks[0].Requires[0] != "brew" || got.Tasks[0].Requires[1] != "git" {
		t.Fatalf("Requires = %#v", got.Tasks[0].Requires)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := map[string]struct {
		body      string
		wantError string
	}{
		"zero concurrency": {
			body: `concurrency = 0
[[tasks]]
id = "brew"
name = "Homebrew"
command = "brew upgrade"`,
			wantError: "concurrency",
		},
		"duplicate id": {
			body: `[[tasks]]
id = "brew"
name = "Homebrew"
command = "brew upgrade"
[[tasks]]
id = "brew"
name = "Again"
command = "brew outdated"`,
			wantError: "tasks[1].id",
		},
		"empty command": {
			body: `[[tasks]]
id = "brew"
name = "Homebrew"
command = ""`,
			wantError: "tasks[0].command",
		},
		"whitespace command": {
			body: `[[tasks]]
id = "brew"
name = "Homebrew"
command = "   "`,
			wantError: "tasks[0].command",
		},
		"empty id": {
			body: `[[tasks]]
id = ""
name = "Homebrew"
command = "brew upgrade"`,
			wantError: "tasks[0].id",
		},
		"empty name": {
			body: `[[tasks]]
id = "brew"
name = ""
command = "brew upgrade"`,
			wantError: "tasks[0].name",
		},
		"no tasks": {
			body:      `concurrency = 1`,
			wantError: "tasks",
		},
		"duplicate requirements": {
			body: `[[tasks]]
id = "brew"
name = "Homebrew"
command = "brew upgrade"
requires = ["brew", "brew"]`,
			wantError: "tasks[0].requires",
		},
		"unknown key": {
			body: `unexpected = true
[[tasks]]
id = "brew"
name = "Homebrew"
command = "brew upgrade"`,
			wantError: "unknown key",
		},
		"malformed TOML": {
			body:      `concurrency = [`,
			wantError: "config ",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, test.body)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil")
			}
			if !strings.Contains(err.Error(), "config "+path+":") {
				t.Fatalf("Load() error = %q, want path %q", err, path)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load() error = %q, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestLoadMissingFileErrorIncludesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("Load() error = %q, want it to contain %q", err, path)
	}
}

func TestDefaultPathUsesHomeDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("HOME"), ".config", "ua", "config.toml"); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.toml")
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Concurrency != 3 {
		t.Fatalf("Concurrency = %d, want 3", got.Concurrency)
	}
	if len(got.Tasks) != 8 {
		t.Fatalf("len(Tasks) = %d, want 8", len(got.Tasks))
	}
	wantIDs := []string{"homebrew", "conda", "ruby", "npm", "mas", "poetry", "oh-my-zsh", "skills"}
	for i, task := range got.Tasks {
		if !task.Enabled {
			t.Errorf("Tasks[%d].Enabled = false, want true", i)
		}
		if task.ID != wantIDs[i] {
			t.Errorf("Tasks[%d].ID = %q, want %q", i, task.ID, wantIDs[i])
		}
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
