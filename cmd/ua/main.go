package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eoleedi/ua/internal/config"
	"github.com/eoleedi/ua/internal/runner"
	"github.com/eoleedi/ua/internal/ui"
)

type launchFunc func(ctx context.Context, model ui.Model, output io.Writer) (ui.Model, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithLauncher(args, stdout, stderr, launchProgram)
}

func runWithLauncher(args []string, stdout, stderr io.Writer, launch launchFunc) int {
	flags := flag.NewFlagSet("ua", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "ua: unexpected argument %q\n", flags.Arg(0))
		flags.Usage()
		return 2
	}

	if *configPath == "" {
		path, err := config.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "ua: %v\n", err)
			return 2
		}
		*configPath = path
	}
	loaded, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "ua: %v\n", err)
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
	defer func() {
		manager.Cancel()
		manager.Wait()
	}()
	model := ui.New(tasks, manager, events)
	finalModel, launchErr := launch(ctx, model, stdout)
	if launchErr != nil {
		fmt.Fprintf(stderr, "ua: %v\n", launchErr)
		return 2
	}
	return finalModel.ExitCode()
}

func launchProgram(ctx context.Context, model ui.Model, output io.Writer) (ui.Model, error) {
	signalCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithOutput(output))
	finished := make(chan struct{})
	bridgeDone := bridgeTermination(signalCtx, finished, program.Send)

	result, err := program.Run()
	close(finished)
	<-bridgeDone
	if err != nil {
		return model, err
	}
	finalModel, ok := result.(ui.Model)
	if !ok {
		return model, fmt.Errorf("ui returned %T, want ui.Model", result)
	}
	return finalModel, nil
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
