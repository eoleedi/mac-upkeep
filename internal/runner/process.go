package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type ShellExecutor struct {
	grace       time.Duration
	signalGroup func(int, syscall.Signal) error
}

func NewShellExecutor(grace time.Duration) *ShellExecutor {
	return &ShellExecutor{grace: grace, signalGroup: syscall.Kill}
}

func (e *ShellExecutor) Run(ctx context.Context, task Task, output io.Writer) Result {
	output = &synchronizedWriter{writer: output}
	terminal, childTerminal, err := pty.Open()
	if err != nil {
		return Result{ExitCode: -1, Err: err}
	}
	defer terminal.Close()
	defer childTerminal.Close()
	if err := pty.Setsize(terminal, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		return Result{ExitCode: -1, Err: err}
	}

	cmd := exec.Command("/bin/zsh", "-lc", task.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = colorEnvironment(os.Environ())
	cmd.Stdout = childTerminal
	cmd.Stderr = childTerminal

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1, Err: err}
	}
	_ = childTerminal.Close()

	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(output, ptyEOFReader{Reader: terminal})
		if err != nil {
			_ = terminal.Close()
		}
		copied <- err
	}()

	waited := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		if copyErr := <-copied; waitErr == nil && copyErr != nil {
			waitErr = copyErr
		}
		waited <- waitErr
	}()

	select {
	case err := <-waited:
		return resultFromWait(err)
	case <-ctx.Done():
		return e.cancel(cmd.Process.Pid, waited)
	}
}

type ptyEOFReader struct {
	io.Reader
}

func (r ptyEOFReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}

func colorEnvironment(environment []string) []string {
	result := append([]string(nil), environment...)
	present := make(map[string]bool, len(environment))
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			present[name] = true
		}
	}
	defaults := []string{
		"CLICOLOR=1",
		"CLICOLOR_FORCE=1",
		"FORCE_COLOR=1",
		"HOMEBREW_COLOR=1",
		"TERM=xterm-256color",
	}
	for _, entry := range defaults {
		name, _, _ := strings.Cut(entry, "=")
		if !present[name] {
			result = append(result, entry)
		}
	}
	return result
}

func (e *ShellExecutor) cancel(pid int, waited <-chan error) Result {
	if err := e.signalGroup(-pid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		<-waited
		return Result{ExitCode: -1, Err: context.Canceled}
	}

	timer := time.NewTimer(e.grace)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	waitComplete := false
	waitCh := waited
	for {
		select {
		case <-waitCh:
			waitComplete = true
			waitCh = nil
			if !e.processGroupExists(pid) {
				return Result{ExitCode: -1, Err: context.Canceled}
			}
		case <-ticker.C:
			if e.processGroupExists(pid) {
				continue
			}
			if !waitComplete {
				<-waited
			}
			return Result{ExitCode: -1, Err: context.Canceled}
		case <-timer.C:
			if e.processGroupExists(pid) {
				_ = e.signalGroup(-pid, syscall.SIGKILL)
			}
			if !waitComplete {
				<-waited
			}
			return Result{ExitCode: -1, Err: context.Canceled}
		}
	}
}

func (e *ShellExecutor) processGroupExists(pid int) bool {
	err := e.signalGroup(-pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
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
