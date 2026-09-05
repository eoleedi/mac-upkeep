# UA TUI Final Fix Report

## Result

DONE

## Verification Evidence

Run from `/Users/eoleedi/GitHub/ua/.worktrees/ua-tui` after the final code edit and `gofmt`:

### `go test -race ./...`

Exit status: 0

```text
ok  	github.com/eoleedi/ua/cmd/ua	1.712s
ok  	github.com/eoleedi/ua/internal/config	(cached)
ok  	github.com/eoleedi/ua/internal/runner	(cached)
ok  	github.com/eoleedi/ua/internal/ui	(cached)
```

### `go vet ./...`

Exit status: 0. No output.

### `git diff --check`

Exit status: 0. No output.

## Regression Evidence

- Tests were first run against the pre-fix production code. Runner tests failed to compile because `Manager.CancelTask` and `Normalizer.Flush` did not exist. Config tests showed untrimmed requirements and accepted an empty requirement. UI tests showed `c` did nothing, global cleanup blocked `Update` and returned `tea.Quit` immediately, and final bare-CR output was lost.
- `TestManagerCancelTaskWithShellExecutorThenRetry` uses a real `ShellExecutor`: the first process is canceled through its per-run context, emits `Canceled`, retries while the manager remains active, emits output, and reaches `Succeeded`.
- `TestManagerCancellationPreservesTerminalEventWithSaturatedOutput` deterministically fills output delivery beyond the 64-event bound, cancels without consuming, verifies `Manager.Wait` returns, then drains and observes the terminal `Canceled` event.
- `TestCleanupCommandLeavesEventLoopFreeToDrainTerminalEvents` blocks manager cleanup while applying the runner's terminal event, then verifies quit waits for both cleanup completion and event-channel closure.
- Normalizer tests cover a final bare CR and confirm split CRLF is neither lost nor duplicated.
- Configuration tests cover trimming before storage, whitespace-only requirements, and duplicates that appear only after trimming.
- Startup tests cover SIGINT/SIGTERM/SIGHUP registration, fallback cleanup after launcher error and recovered panic, and cleanup-before-alternate-screen restoration for both error and panic paths.

## Finding Self-Review

### Signal And Shutdown Ownership

- Bubble Tea is created with `tea.WithoutSignalHandler()`.
- `signal.NotifyContext` is the sole SIGINT, SIGTERM, and SIGHUP owner and bridges each first signal to `ui.TerminateMsg`.
- Keyboard `q`/Ctrl-C still requests confirmation while work is active; `y`, or a second `q`/Ctrl-C at confirmation, starts forced managed cleanup.
- Cleanup runs as a Bubble Tea command. The model continues requesting runner events and emits `tea.Quit` only after controller cleanup is complete and the runner event stream has closed.
- Global cleanup is idempotent through `sync.Once`, so normal UI completion and fallback launcher cleanup cannot race duplicate manager shutdown.

Exact abnormal-exit guarantee: `ua` owns alternate-screen entry/exit around `Program.Run`, and its defer performs manager cancellation and wait before leaving that screen on a returned error or propagated panic. Bubble Tea can restore raw input mode and cursor state internally before returning an error because v1.3.10 exposes no pre-restore error hook; this limitation is documented in README. Bubble Tea's normal panic recovery returns an error, while `runWithLauncher` additionally recovers a panic escaping the launcher and still runs its previously registered cleanup defer.

### Selected-Task Cancellation

- `Manager.CancelTask(id)` only accepts a running task and cancels its per-run child context, leaving the manager context, queue, and unrelated tasks active.
- The overview-global `c` key asynchronously cancels the selected running task. Failed/canceled retry behavior remains on `r`.
- Real ShellExecutor integration and UI key-dispatch tests cover the full production path in complementary layers.

### Output Finalization

- `Normalizer.Flush(previous)` restores a final line tentatively removed while distinguishing bare CR from split CRLF, then clears buffered parser state.
- UI event application flushes before assigning any terminal status, after all earlier output events have been applied.

### Terminal Event Delivery

- Output remains bounded and backpressured through a dedicated output channel and a 64-entry relay queue.
- Status events use a separate relay input, so cancellation statuses are accepted even when output is saturated.
- `Manager.Wait` is authoritative for process cleanup and does not depend on an event consumer. The public event stream remains authoritative for modeled state and closes after a consumer drains all accepted events. With no consumer, cleanup still completes; a later consumer can drain terminal statuses and close the stream.

### Requirement Normalization

- Requirement names are trimmed once, empty trimmed values are rejected, duplicate detection uses trimmed values, and only those normalized values are stored and passed to lookup.

### Documentation And Safety

- README documents `c`, selected-task cancel/retry behavior, external signal behavior, cleanup ordering, and the Bubble Tea abnormal-exit limitation.
- No updater command was run. `~/.zshrc` was not read or modified.

## Concerns

- Bubble Tea v1.3.10 does not expose a hook before its internal raw-mode/cursor restoration on an unexpected `Run` error. The application-owned alternate screen prevents restoration of the user's prior screen until process cleanup finishes, but raw mode and cursor may already be restored internally as documented.
