package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eoleedi/mac-upkeep/internal/runner"
	"github.com/eoleedi/mac-upkeep/internal/ui"
)

func TestMissingConfigReturnsStartupErrorWithoutLaunching(t *testing.T) {
	tests := map[string]struct {
		args     func(string) []string
		wantPath func(string) string
	}{
		"default": {
			args:     func(string) []string { return nil },
			wantPath: func(home string) string { return filepath.Join(home, ".config", "mac-upkeep", "config.toml") },
		},
		"explicit": {
			args:     func(home string) []string { return []string{"--config", filepath.Join(home, "missing.toml")} },
			wantPath: func(home string) string { return filepath.Join(home, "missing.toml") },
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			launched := false
			var stderr bytes.Buffer
			code := runWithLauncher(test.args(home), io.Discard, &stderr, func(context.Context, ui.Model, io.Writer, func()) (ui.Model, error) {
				launched = true
				return ui.Model{}, nil
			})

			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if launched {
				t.Fatal("launcher was invoked for missing config")
			}
			if path := test.wantPath(home); !strings.Contains(stderr.String(), path) {
				t.Fatalf("stderr = %q, want config path %q", stderr.String(), path)
			}
		})
	}
}

func TestMalformedConfigDoesNotLaunch(t *testing.T) {
	path := writeCommandConfig(t, `concurrency = [`)
	launched := false
	var stderr bytes.Buffer

	code := runWithLauncher([]string{"--config", path}, io.Discard, &stderr, func(context.Context, ui.Model, io.Writer, func()) (ui.Model, error) {
		launched = true
		return ui.Model{}, nil
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if launched {
		t.Fatal("launcher was invoked for malformed config")
	}
	if !strings.Contains(stderr.String(), path) {
		t.Fatalf("stderr = %q, want config path %q", stderr.String(), path)
	}
}

func TestExplicitConfigLoadsEnabledTasks(t *testing.T) {
	path := writeCommandConfig(t, `
concurrency = 1

[[tasks]]
id = "chosen"
name = "Chosen task"
command = "printf ok"

[[tasks]]
id = "disabled"
name = "Disabled task"
command = "printf no"
enabled = false
`)
	launched := false

	code := runWithLauncher([]string{"--config", path}, io.Discard, io.Discard, func(_ context.Context, model ui.Model, _ io.Writer, _ func()) (ui.Model, error) {
		launched = true
		view := model.View()
		if !strings.Contains(view, "Chosen task") || strings.Contains(view, "Disabled task") {
			t.Fatalf("launcher model contains wrong tasks:\n%s", view)
		}
		return updateTaskStatus(t, model, "chosen", runner.Succeeded), nil
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !launched {
		t.Fatal("launcher was not invoked")
	}
}

func TestLauncherErrorCancelsAndWaitsForManager(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	stopped := filepath.Join(dir, "stopped")
	command := fmt.Sprintf("printf started > %q; trap \"sleep 0.05; printf stopped > %q; exit 0\" TERM; while true; do sleep 0.01; done", started, stopped)
	path := writeCommandConfig(t, fmt.Sprintf(`
concurrency = 1
[[tasks]]
id = "blocking"
name = "Blocking task"
command = %q
`, command))
	var stderr bytes.Buffer

	code := runWithLauncher([]string{"--config", path}, io.Discard, &stderr, func(context.Context, ui.Model, io.Writer, func()) (ui.Model, error) {
		waitForFile(t, started)
		return ui.Model{}, errors.New("terminal unavailable")
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatalf("manager process was not canceled and waited for: %v", err)
	}
	if !strings.Contains(stderr.String(), "terminal unavailable") {
		t.Fatalf("stderr = %q, want launcher error", stderr.String())
	}
}

func TestUITerminationWaitsForManagerBeforeLauncherReturns(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	stopped := filepath.Join(dir, "stopped")
	command := fmt.Sprintf("printf started > %q; trap \"sleep 0.05; printf stopped > %q; exit 0\" TERM; while true; do sleep 0.01; done", started, stopped)
	path := writeCommandConfig(t, fmt.Sprintf(`
concurrency = 1
[[tasks]]
id = "blocking"
name = "Blocking task"
command = %q
`, command))
	cleanupFinishedBeforeReturn := false

	code := runWithLauncher([]string{"--config", path}, io.Discard, io.Discard, func(_ context.Context, model ui.Model, _ io.Writer, _ func()) (ui.Model, error) {
		waitForFile(t, started)
		updated, cmd := model.Update(ui.TerminateMsg{})
		if _, quitting := cmd().(tea.QuitMsg); quitting {
			t.Fatal("termination quit before asynchronous cleanup")
		}
		_, err := os.Stat(stopped)
		cleanupFinishedBeforeReturn = err == nil
		return updated.(ui.Model), nil
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !cleanupFinishedBeforeReturn {
		t.Fatal("UI cancellation returned to launcher before manager cleanup completed")
	}
}

func TestTerminalTaskStatusesDetermineExitCode(t *testing.T) {
	tests := map[string]struct {
		status runner.Status
		want   int
	}{
		"succeeded":   {status: runner.Succeeded, want: 0},
		"failed":      {status: runner.Failed, want: 1},
		"canceled":    {status: runner.Canceled, want: 1},
		"unavailable": {status: runner.Unavailable, want: 1},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeCommandConfig(t, `
concurrency = 1
[[tasks]]
id = "task"
name = "Task"
command = "printf ok"
`)
			code := runWithLauncher([]string{"--config", path}, io.Discard, io.Discard, func(_ context.Context, model ui.Model, _ io.Writer, _ func()) (ui.Model, error) {
				return updateTaskStatus(t, model, "task", test.status), nil
			})
			if code != test.want {
				t.Fatalf("exit code = %d, want %d", code, test.want)
			}
		})
	}
}

func TestRunRecoversLauncherPanicAfterManagerCleanup(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	stopped := filepath.Join(dir, "stopped")
	command := fmt.Sprintf("printf started > %q; trap 'printf stopped > %q; exit 0' TERM; while true; do sleep 0.01; done", started, stopped)
	path := writeCommandConfig(t, fmt.Sprintf(`
concurrency = 1
[[tasks]]
id = "blocking"
name = "Blocking task"
command = %q
`, command))
	var stderr bytes.Buffer

	code := runWithLauncher([]string{"--config", path}, io.Discard, &stderr, func(context.Context, ui.Model, io.Writer, func()) (ui.Model, error) {
		waitForFile(t, started)
		panic("render failed")
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatalf("manager process was not cleaned up after panic: %v", err)
	}
	if !strings.Contains(stderr.String(), "UI panic: render failed") {
		t.Fatalf("stderr = %q, want recovered panic", stderr.String())
	}
}

func TestManagedScreenCleansUpBeforeRestoringAlternateScreen(t *testing.T) {
	var output bytes.Buffer
	_, err := runManagedScreen(&output, func() { output.WriteString("cleanup") }, func() (tea.Model, error) {
		return nil, errors.New("run failed")
	})
	if err == nil {
		t.Fatal("runManagedScreen() error = nil")
	}
	if got, want := output.String(), enterAltScreen+"cleanup"+exitAltScreen; got != want {
		t.Fatalf("screen lifecycle = %q, want %q", got, want)
	}
}

func TestManagedScreenCleansUpBeforeRestoringAfterPanic(t *testing.T) {
	var output bytes.Buffer
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("runManagedScreen did not propagate panic")
			}
		}()
		_, _ = runManagedScreen(&output, func() { output.WriteString("cleanup") }, func() (tea.Model, error) {
			panic("render failed")
		})
	}()
	if got, want := output.String(), enterAltScreen+"cleanup"+exitAltScreen; got != want {
		t.Fatalf("panic screen lifecycle = %q, want %q", got, want)
	}
}

func TestTerminationSignalsIncludeInterruptTermAndHangup(t *testing.T) {
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		found := false
		for _, signal := range terminationSignals {
			found = found || signal == want
		}
		if !found {
			t.Fatalf("termination signals %v missing %v", terminationSignals, want)
		}
	}
}

func TestTerminationBridgeSendsMessageAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	messages := make(chan tea.Msg, 1)
	done := bridgeTermination(ctx, finished, func(msg tea.Msg) { messages <- msg })
	cancel()

	select {
	case msg := <-messages:
		if _, ok := msg.(ui.TerminateMsg); !ok {
			t.Fatalf("termination message = %T, want ui.TerminateMsg", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation was not delivered to the UI")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("termination bridge did not stop after sending")
	}
}

func TestTerminationBridgeStopsWithoutSendingAfterProgramFinishes(t *testing.T) {
	finished := make(chan struct{})
	messages := make(chan tea.Msg, 1)
	done := bridgeTermination(context.Background(), finished, func(msg tea.Msg) { messages <- msg })
	close(finished)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("termination bridge leaked after program finished")
	}
	select {
	case msg := <-messages:
		t.Fatalf("unexpected message after program finished: %T", msg)
	default:
	}
}

func writeCommandConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func updateTaskStatus(t *testing.T, model ui.Model, taskID string, status runner.Status) ui.Model {
	t.Helper()
	updated, _ := model.Update(runner.Event{TaskID: taskID, Status: status})
	result, ok := updated.(ui.Model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.Model", updated)
	}
	return result
}

func assertTeaQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("command = nil, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", cmd())
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
