package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eoleedi/mac-upkeep/internal/config"
	"github.com/eoleedi/mac-upkeep/internal/runner"
	"github.com/eoleedi/mac-upkeep/internal/ui"
)

type launchFunc func(ctx context.Context, model ui.Model, output io.Writer, cleanup func()) (ui.Model, error)

var terminationSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	enterAltScreen = "\x1b[?1049h"
	exitAltScreen  = "\x1b[?1049l"
)

type managerController struct {
	manager *runner.Manager
}

func (c managerController) Retry(id string) error {
	return c.manager.Retry(id)
}

func (c managerController) CancelTask(id string) error {
	return c.manager.CancelTask(id)
}

func (c managerController) Cancel() {
	c.manager.Cancel()
	c.manager.Wait()
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithLauncher(args, stdout, stderr, launchProgram)
}

func runWithLauncher(args []string, stdout, stderr io.Writer, launch launchFunc) (exitCode int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(stderr, "mac-upkeep: UI panic: %v\n", recovered)
			exitCode = 2
		}
	}()
	flags := flag.NewFlagSet("mac-upkeep", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to configuration file")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "mac-upkeep: unexpected argument %q\n", flags.Arg(0))
		flags.Usage()
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "mac-upkeep %s\n", version)
		return 0
	}

	if *configPath == "" {
		path, err := config.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "mac-upkeep: %v\n", err)
			return 2
		}
		*configPath = path
	}
	loaded, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "mac-upkeep: %v\n", err)
		return 2
	}

	tasks := make([]runner.Task, 0, len(loaded.Tasks))
	for _, task := range loaded.Tasks {
		if !task.Enabled {
			continue
		}
		tasks = append(tasks, runner.Task{
			ID:       task.ID,
			Name:     task.Name,
			Command:  task.Command,
			Requires: task.Requires,
		})
	}

	manager := runner.NewManager(tasks, loaded.Concurrency, runner.NewShellExecutor(2*time.Second), exec.LookPath)
	ctx := context.Background()
	events := manager.Start(ctx)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			manager.Cancel()
			manager.Wait()
		})
	}
	defer cleanup()
	model := ui.New(tasks, managerController{manager: manager}, events)
	finalModel, launchErr := launch(ctx, model, stdout, cleanup)
	if launchErr != nil {
		fmt.Fprintf(stderr, "mac-upkeep: %v\n", launchErr)
		return 2
	}
	return finalModel.ExitCode()
}

func launchProgram(ctx context.Context, model ui.Model, output io.Writer, cleanup func()) (ui.Model, error) {
	signalCtx, stop := signal.NotifyContext(ctx, terminationSignals...)
	defer stop()

	program := tea.NewProgram(model, tea.WithOutput(output), tea.WithoutSignalHandler())
	finished := make(chan struct{})
	bridgeDone := bridgeTermination(signalCtx, finished, program.Send)
	defer func() {
		close(finished)
		<-bridgeDone
	}()

	result, err := runManagedScreen(output, cleanup, program.Run)
	if err != nil {
		return model, err
	}
	finalModel, ok := result.(ui.Model)
	if !ok {
		return model, fmt.Errorf("ui returned %T, want ui.Model", result)
	}
	return finalModel, nil
}

func runManagedScreen(output io.Writer, cleanup func(), run func() (tea.Model, error)) (tea.Model, error) {
	defer func() { _, _ = io.WriteString(output, exitAltScreen) }()
	defer cleanup()
	_, _ = io.WriteString(output, enterAltScreen)
	return run()
}

func bridgeTermination(ctx context.Context, finished <-chan struct{}, send func(tea.Msg)) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			send(ui.TerminateMsg{})
		case <-finished:
		}
	}()
	return done
}
