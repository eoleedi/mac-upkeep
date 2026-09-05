package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestShellExecutorStreamsStdoutAndStderr(t *testing.T) {
	script := writeScript(t, `printf 'out-one\n'; printf 'err-one\n' >&2; printf 'tail'`)
	var output safeBuffer
	result := NewShellExecutor(2*time.Second).Run(context.Background(), Task{Command: script}, &output)
	if result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	for _, want := range []string{"out-one", "err-one", "tail"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output %q missing %q", output.String(), want)
		}
	}
}

func TestShellExecutorReportsExitCode(t *testing.T) {
	result := NewShellExecutor(2*time.Second).Run(context.Background(), Task{Command: "exit 7"}, io.Discard)
	if result.ExitCode != 7 || result.Err == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestShellExecutorSerializesConcurrentOutput(t *testing.T) {
	script := writeScript(t, `
for stream in stdout stderr; do
  (
    for i in {1..200}; do
      if [[ $stream == stdout ]]; then
        printf 'out-%s\n' "$i"
      else
        printf 'err-%s\n' "$i" >&2
      fi
    done
  ) &
done
wait
`)
	var output unsafeBuffer
	result := NewShellExecutor(2*time.Second).Run(context.Background(), Task{Command: script}, &output)
	if result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	if got := output.String(); !strings.Contains(got, "out-200") || !strings.Contains(got, "err-200") {
		t.Fatalf("output missing final lines")
	}
}

func TestSynchronizedWriterSerializesWrites(t *testing.T) {
	underlying := &overlapWriter{}
	writer := &synchronizedWriter{writer: underlying}
	start := make(chan struct{})
	var writes sync.WaitGroup
	for range 20 {
		writes.Add(1)
		go func() {
			defer writes.Done()
			<-start
			_, _ = writer.Write([]byte("output"))
		}()
	}
	close(start)
	writes.Wait()
	if underlying.overlapped.Load() {
		t.Fatal("underlying writer received concurrent writes")
	}
}

func TestShellExecutorCancelsProcessGroupAfterGrace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin process-group integration test")
	}

	pidFile := t.TempDir() + "/child.pid"
	script := writeScript(t, fmt.Sprintf(`
(trap '' TERM; while true; do sleep 1; done) &
child=$!
printf '%%s' "$child" > %s
wait "$child"
`, shellQuote(pidFile)))
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan Result, 1)
	grace := 100 * time.Millisecond
	go func() {
		resultCh <- NewShellExecutor(grace).Run(ctx, Task{Command: script}, io.Discard)
	}()

	childPID := waitForPID(t, pidFile)
	startedCancel := time.Now()
	cancel()
	select {
	case result := <-resultCh:
		if result.ExitCode != -1 || !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("result = %#v, want canceled", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within three seconds")
	}
	if elapsed := time.Since(startedCancel); elapsed < grace {
		t.Fatalf("Run returned after %v, before grace %v", elapsed, grace)
	}

	deadline := time.Now().Add(time.Second)
	for {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child process %d still exists: %v", childPID, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShellExecutorTreatsCanceledOutputAsCancellation(t *testing.T) {
	result := NewShellExecutor(time.Second).Run(
		context.Background(),
		Task{Command: "printf output"},
		writerFunc(func([]byte) (int, error) { return 0, context.Canceled }),
	)
	if result.ExitCode != -1 || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("result = %#v, want canceled", result)
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type unsafeBuffer struct {
	b strings.Builder
}

type overlapWriter struct {
	active     atomic.Int32
	overlapped atomic.Bool
}

func (w *overlapWriter) Write(p []byte) (int, error) {
	if w.active.Add(1) != 1 {
		w.overlapped.Store(true)
	}
	time.Sleep(time.Millisecond)
	w.active.Add(-1)
	return len(p), nil
}

func (b *unsafeBuffer) Write(p []byte) (int, error) {
	return b.b.Write(p)
}

func (b *unsafeBuffer) String() string {
	return b.b.String()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/script.zsh"
	contents := "#!/bin/zsh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		contents, err := os.ReadFile(path)
		if err == nil && len(contents) > 0 {
			pid, err := strconv.Atoi(string(contents))
			if err != nil {
				t.Fatalf("parse child PID %q: %v", contents, err)
			}
			return pid
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child PID: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for child PID")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
