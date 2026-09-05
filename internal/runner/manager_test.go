package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerBoundsConcurrencyAndPreservesQueueOrder(t *testing.T) {
	executor := newBlockingExecutor()
	tasks := makeTasks(5)
	manager := NewManager(tasks, 2, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	for i := range tasks {
		assertEvent(t, events, tasks[i].ID, Queued)
	}
	assertEvent(t, events, "task-1", Running)
	assertEvent(t, events, "task-2", Running)
	assertNoEvent(t, events)

	executor.release("task-1", Result{})
	assertEvent(t, events, "task-1", Succeeded)
	assertEvent(t, events, "task-3", Running)
}

func TestManagerContinuesAfterTaskFailure(t *testing.T) {
	executor := newBlockingExecutor()
	tasks := makeTasks(3)
	manager := NewManager(tasks, 1, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	for i := range tasks {
		assertEvent(t, events, tasks[i].ID, Queued)
	}
	assertEvent(t, events, "task-1", Running)
	executor.release("task-1", Result{ExitCode: 7, Err: fmt.Errorf("exit status 7")})
	failed := assertEvent(t, events, "task-1", Failed)
	if failed.ExitCode != 7 || failed.Err == nil {
		t.Fatalf("failed event = %#v, want exit code 7 and error", failed)
	}
	assertEvent(t, events, "task-2", Running)
	executor.release("task-2", Result{})
	assertEvent(t, events, "task-2", Succeeded)
	assertEvent(t, events, "task-3", Running)
	executor.release("task-3", Result{})
	assertEvent(t, events, "task-3", Succeeded)
}

func TestManagerRetryClearsTerminalResultAndRunsAgain(t *testing.T) {
	executor := newBlockingExecutor()
	manager := NewManager(makeTasks(1), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	executor.release("task-1", Result{ExitCode: 7, Err: fmt.Errorf("exit status 7")})
	assertEvent(t, events, "task-1", Failed)

	if err := manager.Retry("task-1"); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	queued := assertEvent(t, events, "task-1", Queued)
	if queued.ExitCode != 0 || queued.Err != nil {
		t.Fatalf("retry queued event = %#v, want cleared result", queued)
	}
	assertEvent(t, events, "task-1", Running)
	executor.release("task-1", Result{})
	assertEvent(t, events, "task-1", Succeeded)
}

func TestManagerRetryRunsCanceledTaskAgain(t *testing.T) {
	executor := newBlockingExecutor()
	manager := NewManager(makeTasks(1), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	executor.release("task-1", Result{Err: context.Canceled})
	assertEvent(t, events, "task-1", Canceled)

	if err := manager.Retry("task-1"); err != nil {
		t.Fatalf("Retry() error = %v for canceled task", err)
	}
	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	executor.release("task-1", Result{})
	assertEvent(t, events, "task-1", Succeeded)
}

func TestManagerCancelTaskWithShellExecutorThenRetry(t *testing.T) {
	dir := t.TempDir()
	firstRun := filepath.Join(dir, "first-run")
	command := fmt.Sprintf(`if [[ ! -e %q ]]; then touch %q; trap 'exit 0' TERM; while true; do sleep 0.01; done; else printf retried; fi`, firstRun, firstRun)
	manager := NewManager([]Task{{ID: "task-1", Command: command}}, 1, NewShellExecutor(100*time.Millisecond), lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	waitForPath(t, firstRun)
	if err := manager.CancelTask("task-1"); err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	assertEvent(t, events, "task-1", Canceled)
	if err := manager.Retry("task-1"); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	output := assertEvent(t, events, "task-1", "")
	if output.Output != "retried" {
		t.Fatalf("retry output = %q, want retried", output.Output)
	}
	assertEvent(t, events, "task-1", Succeeded)
}

func TestManagerCancelTaskRejectsNonRunningTasks(t *testing.T) {
	executor := newBlockingExecutor()
	manager := NewManager(makeTasks(2), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})
	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-2", Queued)
	assertEvent(t, events, "task-1", Running)
	for _, id := range []string{"task-2", "missing"} {
		if err := manager.CancelTask(id); err == nil {
			t.Fatalf("CancelTask(%q) error = nil", id)
		}
	}
}

func TestManagerRetryRechecksRequirements(t *testing.T) {
	var calls atomic.Int64
	executor := executorFunc(func(context.Context, Task, io.Writer) Result {
		calls.Add(1)
		return Result{ExitCode: 1}
	})
	var lookups atomic.Int64
	lookup := func(string) (string, error) {
		if lookups.Add(1) == 1 {
			return "/bin/tool", nil
		}
		return "", exec.ErrNotFound
	}
	manager := NewManager([]Task{{ID: "task-1", Requires: []string{"tool"}}}, 1, executor, lookup)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	assertEvent(t, events, "task-1", Failed)
	if err := manager.Retry("task-1"); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	assertEvent(t, events, "task-1", Queued)
	event := assertEvent(t, events, "task-1", Unavailable)
	if event.Err == nil || !strings.Contains(event.Err.Error(), "tool") {
		t.Fatalf("unavailable error = %v, want requirement name", event.Err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("executor calls = %d, want 1", got)
	}
}

func TestManagerRetryRejectsIneligibleTasks(t *testing.T) {
	t.Run("running queued and unknown", func(t *testing.T) {
		executor := newBlockingExecutor()
		manager := NewManager(makeTasks(2), 1, executor, lookupFound)
		events := manager.Start(context.Background())
		t.Cleanup(func() {
			manager.Cancel()
			manager.Wait()
		})

		assertEvent(t, events, "task-1", Queued)
		assertEvent(t, events, "task-2", Queued)
		assertEvent(t, events, "task-1", Running)
		for _, id := range []string{"task-1", "task-2", "missing"} {
			if err := manager.Retry(id); err == nil {
				t.Fatalf("Retry(%q) error = nil", id)
			}
		}
	})

	t.Run("succeeded", func(t *testing.T) {
		executor := newBlockingExecutor()
		manager := NewManager(makeTasks(1), 1, executor, lookupFound)
		events := manager.Start(context.Background())
		t.Cleanup(func() {
			manager.Cancel()
			manager.Wait()
		})

		assertEvent(t, events, "task-1", Queued)
		assertEvent(t, events, "task-1", Running)
		executor.release("task-1", Result{})
		assertEvent(t, events, "task-1", Succeeded)
		if err := manager.Retry("task-1"); err == nil {
			t.Fatal("Retry() error = nil for succeeded task")
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		manager := NewManager(
			[]Task{{ID: "task-1", Requires: []string{"missing"}}},
			1,
			&countingExecutor{},
			func(string) (string, error) { return "", exec.ErrNotFound },
		)
		events := manager.Start(context.Background())
		t.Cleanup(func() {
			manager.Cancel()
			manager.Wait()
		})

		assertEvent(t, events, "task-1", Unavailable)
		if err := manager.Retry("task-1"); err == nil {
			t.Fatal("Retry() error = nil for unavailable task")
		}
	})
}

func TestManagerMarksMissingRequirementUnavailable(t *testing.T) {
	executor := &countingExecutor{}
	lookup := func(name string) (string, error) { return "", exec.ErrNotFound }
	manager := NewManager([]Task{{ID: "task-1", Requires: []string{"brew"}}}, 1, executor, lookup)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	event := assertEvent(t, events, "task-1", Unavailable)
	if event.Err == nil || !strings.Contains(event.Err.Error(), "brew") {
		t.Fatalf("unavailable error = %v, want requirement name", event.Err)
	}
	if calls := executor.calls.Load(); calls != 0 {
		t.Fatalf("executor calls = %d, want 0", calls)
	}
}

func TestManagerCancelMarksQueuedTasksCanceledAndCloses(t *testing.T) {
	executor := newBlockingExecutor()
	manager := NewManager(makeTasks(3), 1, executor, lookupFound)
	events := manager.Start(context.Background())

	for i := 1; i <= 3; i++ {
		assertEvent(t, events, fmt.Sprintf("task-%d", i), Queued)
	}
	assertEvent(t, events, "task-1", Running)
	manager.Cancel()
	manager.Cancel()

	terminal := make(map[string]Status)
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				if terminal["task-1"] != Canceled || terminal["task-2"] != Canceled || terminal["task-3"] != Canceled {
					t.Fatalf("terminal states = %#v, want all canceled", terminal)
				}
				manager.Wait()
				return
			}
			if event.Status == Running && event.TaskID != "task-1" {
				t.Fatalf("queued task started after cancellation: %#v", event)
			}
			if event.Status == Canceled {
				terminal[event.TaskID] = event.Status
			}
		case <-deadline:
			t.Fatal("timed out waiting for manager cancellation")
		}
	}
}

func TestManagerCancelUnblocksEventBackpressure(t *testing.T) {
	bufferFilled := make(chan struct{})
	executor := executorFunc(func(_ context.Context, _ Task, output io.Writer) Result {
		for range eventBufferSize {
			if _, err := io.WriteString(output, "output"); err != nil {
				return Result{Err: err}
			}
		}
		close(bufferFilled)
		_, err := io.WriteString(output, "blocked output")
		return Result{Err: err}
	})
	manager := NewManager(makeTasks(1), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	<-bufferFilled

	manager.Cancel()
	waited := make(chan struct{})
	go func() {
		manager.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return with a full event buffer")
	}
}

func TestManagerCancellationPreservesTerminalEventWithSaturatedOutput(t *testing.T) {
	filled := make(chan struct{})
	executor := executorFunc(func(ctx context.Context, _ Task, output io.Writer) Result {
		for range eventBufferSize + 1 {
			if _, err := io.WriteString(output, "output"); err != nil {
				close(filled)
				return Result{Err: err}
			}
		}
		close(filled)
		<-ctx.Done()
		return Result{Err: ctx.Err()}
	})
	manager := NewManager(makeTasks(1), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	<-filled

	manager.Cancel()
	manager.Wait()
	foundCanceled := false
	for event := range events {
		foundCanceled = foundCanceled || event.Status == Canceled
	}
	if !foundCanceled {
		t.Fatal("terminal canceled event was dropped under output backpressure")
	}
}

func TestManagerForwardsExecutorOutput(t *testing.T) {
	executor := executorFunc(func(_ context.Context, _ Task, output io.Writer) Result {
		_, _ = io.WriteString(output, "updating\n")
		return Result{}
	})
	manager := NewManager(makeTasks(1), 1, executor, lookupFound)
	events := manager.Start(context.Background())
	t.Cleanup(func() {
		manager.Cancel()
		manager.Wait()
	})

	assertEvent(t, events, "task-1", Queued)
	assertEvent(t, events, "task-1", Running)
	output := assertEvent(t, events, "task-1", "")
	if output.Output != "updating\n" {
		t.Fatalf("Output = %q, want %q", output.Output, "updating\n")
	}
	assertEvent(t, events, "task-1", Succeeded)
}

func TestAggregateRequiresAllSuccess(t *testing.T) {
	if got := Aggregate(map[string]Status{"a": Succeeded, "b": Failed}); got == 0 {
		t.Fatal("Aggregate() = 0 with failed task")
	}
	if got := Aggregate(map[string]Status{"a": Succeeded, "b": Succeeded}); got != 0 {
		t.Fatalf("Aggregate() = %d, want 0", got)
	}
	if got := Aggregate(map[string]Status{}); got == 0 {
		t.Fatal("Aggregate() = 0 for empty states")
	}
}

type blockingExecutor struct {
	mu       sync.Mutex
	releases map[string]chan Result
}

type countingExecutor struct {
	calls atomic.Int64
}

func (e *countingExecutor) Run(context.Context, Task, io.Writer) Result {
	e.calls.Add(1)
	return Result{}
}

type executorFunc func(context.Context, Task, io.Writer) Result

func (f executorFunc) Run(ctx context.Context, task Task, output io.Writer) Result {
	return f(ctx, task, output)
}

func newBlockingExecutor() *blockingExecutor {
	return &blockingExecutor{releases: make(map[string]chan Result)}
}

func (e *blockingExecutor) Run(ctx context.Context, task Task, _ io.Writer) Result {
	select {
	case <-ctx.Done():
		return Result{Err: ctx.Err()}
	case result := <-e.releaseChannel(task.ID):
		return result
	}
}

func (e *blockingExecutor) release(taskID string, result Result) {
	e.releaseChannel(taskID) <- result
}

func (e *blockingExecutor) releaseChannel(taskID string) chan Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.releases[taskID] == nil {
		e.releases[taskID] = make(chan Result)
	}
	return e.releases[taskID]
}

func makeTasks(count int) []Task {
	tasks := make([]Task, count)
	for i := range tasks {
		tasks[i] = Task{ID: fmt.Sprintf("task-%d", i+1)}
	}
	return tasks
}

func lookupFound(name string) (string, error) {
	return "/bin/" + name, nil
}

func assertEvent(t *testing.T, events <-chan Event, taskID string, status Status) Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatalf("events closed, want %s %s", taskID, status)
		}
		if event.TaskID != taskID || event.Status != status {
			t.Fatalf("event = %#v, want task %s status %s", event, taskID, status)
		}
		if event.At.IsZero() {
			t.Fatalf("event = %#v, want timestamp", event)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s %s", taskID, status)
		return Event{}
	}
}

func assertNoEvent(t *testing.T, events <-chan Event) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected event before executor release: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
}

func waitForPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
