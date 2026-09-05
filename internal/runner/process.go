package runner

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type ShellExecutor struct {
	grace time.Duration
}

func NewShellExecutor(grace time.Duration) *ShellExecutor {
	return &ShellExecutor{grace: grace}
}

func (e *ShellExecutor) Run(ctx context.Context, task Task, output io.Writer) Result {
	output = &synchronizedWriter{writer: output}
	cmd := exec.Command("/bin/zsh", "-lc", task.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Err: err}
	}

	waited := make(chan error, 1)
	go func() {
		waited <- cmd.Wait()
	}()

	select {
	case err := <-waited:
		return resultFromWait(err)
	case <-ctx.Done():
		return e.cancel(cmd.Process.Pid, waited)
	}
}

func (e *ShellExecutor) cancel(pid int, waited <-chan error) Result {
	if err := syscall.Kill(-pid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		<-waited
		return Result{ExitCode: -1, Err: context.Canceled}
	}

	timer := time.NewTimer(e.grace)
	defer timer.Stop()

	waitComplete := false
	select {
	case <-waited:
		waitComplete = true
		if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return Result{ExitCode: -1, Err: context.Canceled}
		}
		<-timer.C
	case <-timer.C:
	}

	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if !waitComplete {
		<-waited
	}
	return Result{ExitCode: -1, Err: context.Canceled}
}

func resultFromWait(err error) Result {
	if err == nil {
		return Result{}
	}
	if errors.Is(err, context.Canceled) {
		return Result{ExitCode: -1, Err: context.Canceled}
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return Result{ExitCode: exitError.ExitCode(), Err: err}
	}
	return Result{ExitCode: -1, Err: err}
}

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
