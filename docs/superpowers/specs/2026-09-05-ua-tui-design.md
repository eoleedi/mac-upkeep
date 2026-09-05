# UA TUI Design

## Summary

Replace the `ua` alias and `update_all` Zsh function with a standalone Go terminal application. The application starts configured update commands immediately, runs up to three commands concurrently, shows every command's state in an overview, and displays the selected command's live log in a persistent split pane.

The source will live in `~/GitHub/ua`. The installed binary will live at `~/.local/bin/ua`, which is already on `PATH`. Runtime configuration will live at `~/.config/ua/config.toml`.

## Goals

- Show all enabled update commands and their current states in one overview.
- Stream the selected command's combined standard output and standard error into a scrollable log pane.
- Run independent commands concurrently with a configurable limit of three by default.
- Continue running unaffected commands when one command fails.
- Allow a failed or canceled command to be retried from the overview.
- Allow safe cancellation of all running commands after confirmation.
- Keep complete logs for the current session and discard them at exit.
- Return an exit status that accurately represents the whole run.
- Make updater definitions editable without rebuilding the binary.

## Non-Goals

- Persist logs or run history after the application exits.
- Update packages directly through package-manager APIs.
- Detach updates to continue after the TUI exits.
- Provide remote execution, notifications, scheduling, or automatic self-update.
- Preserve the old `update_all_parallel` tmux implementation.

## Technology And Structure

The application will use Go, Bubble Tea for the event loop, Bubbles for the log viewport, Lip Gloss for terminal styling, and a TOML parser for configuration.

The code will be divided into focused packages:

- `cmd/ua`: startup, dependency wiring, signal handling, and final exit code.
- `internal/config`: TOML loading, defaults, validation, and executable requirement checks.
- `internal/runner`: task queue, concurrency control, child processes, output events, cancellation, and retries.
- `internal/ui`: Bubble Tea model, split-pane rendering, keyboard handling, scrolling, resize behavior, and quit confirmation.

The UI consumes runner events. Runner goroutines do not mutate UI state directly.

## Configuration

The global configuration contains `concurrency`, defaulting to `3`. Each `[[tasks]]` entry contains:

- `id`: stable, unique machine-readable identifier.
- `name`: human-readable overview label.
- `command`: command string executed by `/bin/zsh -lc`.
- `enabled`: whether the task participates in a run; defaults to `true`.
- `requires`: optional executable names that must be discoverable on `PATH`.

Example:

```toml
concurrency = 3

[[tasks]]
id = "homebrew"
name = "Homebrew"
command = "brew upgrade"
requires = ["brew"]

[[tasks]]
id = "conda"
name = "Conda"
command = "conda update conda python --all -y"
requires = ["conda"]
```

The installed default configuration will reproduce the current eight updater groups in this order:

1. Homebrew
2. Conda
3. Ruby
4. npm
5. Mac App Store
6. Poetry
7. Oh My Zsh
8. Skills

Composite behavior, including Ruby version detection and loading the shell definitions required by Oh My Zsh, remains in the corresponding command string rather than becoming package-manager-specific Go code.

Configuration validation occurs before entering the terminal's alternate screen. Invalid TOML, duplicate or empty IDs, empty names or commands, and concurrency below one produce a concise error with the relevant field. A missing config file also produces an actionable error rather than silently generating or changing user configuration.

## Task Model And Scheduling

Each enabled task has one of these states:

- `queued`: waiting for a concurrency slot.
- `running`: child process active.
- `succeeded`: exited with code zero.
- `failed`: exited nonzero or could not start.
- `canceled`: terminated by application cancellation.
- `unavailable`: one or more declared requirements are missing.

Unavailable tasks never consume a worker slot. The scheduler considers tasks in configuration order and starts at most `concurrency` child processes. A task completion immediately makes its slot available to the next queued task. A task failure does not block or cancel other tasks.

Retry is available for `failed` and `canceled` tasks. Retrying resets the selected task to `queued`, clears its previous output and timing data, rechecks its requirements, and schedules it subject to the same concurrency limit. A task that remains unavailable stays unavailable.

Each command inherits the environment and working directory from the `ua` process. Commands run in their own process groups so cancellation reaches descendants, not only the shell process.

## Output And Event Flow

Standard output and standard error share a synchronized task writer so their observed order is retained as closely as process scheduling permits. The writer emits chunks into the Bubble Tea message loop. The UI appends those chunks to that task's in-memory session log and refreshes the selected viewport.

The complete output for every task remains scrollable for the duration of the session. Update command output is expected to be moderate, so persistent files and log rotation are intentionally omitted. Carriage-return progress output is normalized into readable lines. ANSI styling is allowed when Bubble Tea can safely render it; terminal-control sequences that could corrupt the layout are stripped.

Task completion events contain the exit code, elapsed time, and any process-start error. Late output is drained before the final state event so the log does not lose trailing diagnostics.

## Interface

The application enters the alternate screen and begins the run immediately. The upper pane displays a title, aggregate progress, and one row per enabled task. Each row includes:

- status marker and task name;
- elapsed time while running or total duration after completion;
- exit code or short unavailable reason when relevant.

The lower pane displays the selected task name, state, and live log. The overview remains visible while logs are inspected. The layout adapts to terminal resizing; when height is constrained, the overview remains usable and the log pane receives the remaining space.

Keyboard controls:

- Up/Down or `j`/`k`: select a task.
- Enter: focus the selected task's log viewport.
- Escape: return focus to the overview.
- Page Up/Page Down: scroll the selected log while the overview is focused.
- Up/Down or `j`/`k`: scroll line-by-line while the log is focused.
- `g`/`G`: jump to the start/end of the selected log.
- `r`: retry the selected failed or canceled task.
- `q`: quit when idle; request confirmation while any task is queued or running.
- `y`/`n` or Escape: confirm or dismiss cancellation.
- `?`: toggle concise key help.

Live logs follow the newest output unless the user scrolls upward. Returning to the bottom resumes follow mode.

After all tasks reach terminal states, the TUI remains open for review and retry. The header presents the final succeeded, failed, canceled, and unavailable counts.

## Cancellation And Signals

Confirming quit cancels the shared run context, marks queued tasks canceled, sends `SIGTERM` to each running process group, waits for a two-second grace period, and then sends `SIGKILL` to groups that remain alive. Cleanup finishes before the alternate screen is restored.

`SIGINT` requests the same cancellation flow when the TUI can process it. A second interrupt skips confirmation but still attempts process-group cleanup. Unexpected UI shutdown or a recovered panic also cancels the shared context before returning control to the terminal.

## Exit Status

The process exits with status `0` only when every enabled task succeeds. It exits nonzero when any enabled task fails, is canceled, or is unavailable. A startup or configuration error also exits nonzero.

This rule applies after retries: a task's latest terminal result determines the final aggregate status.

## Installation And Shell Migration

The repository will provide a small install target that builds the binary to `~/.local/bin/ua` and installs an example configuration only when `~/.config/ua/config.toml` does not already exist.

After a successful smoke test, remove `alias -- ua=update_all`, `update_all`, and `update_all_parallel` from `~/.zshrc`. No compatibility wrapper is needed because `~/.local/bin` is already on `PATH`.

## Testing

Unit tests will cover:

- valid configuration, defaults, and each validation failure;
- requirement detection and unavailable tasks;
- queue ordering and enforcement of the concurrency limit;
- all valid state transitions;
- retry reset and rescheduling behavior;
- output accumulation and progress-line normalization;
- aggregate result and process exit status;
- overview/log focus, scrolling, resizing, confirmation, and retry keys.

Runner integration tests will create temporary shell scripts instead of invoking real package managers. They will verify concurrent execution, interleaved standard output and standard error, nonzero exits, trailing output capture, and process-group cancellation.

UI tests will update the Bubble Tea model at fixed dimensions and assert important text and state rather than brittle full-screen snapshots.

Before shell migration, verification will run:

```sh
go test ./...
go vet ./...
```

A manual smoke configuration with short, harmless commands will then verify live output, selection, scrolling, one failure, retry, resize handling, and confirmed cancellation. Real updater commands will only be used after this verification succeeds.

## Delivery Criteria

The redesign is complete when:

- `ua` starts all available configured tasks without an extra prompt;
- no more than three commands run concurrently by default;
- every task's state remains visible in the overview;
- selecting a task exposes its complete live session log;
- one task's failure does not stop unrelated tasks;
- failed and canceled tasks can be retried;
- quitting during execution confirms and cleans up descendants;
- final process status reflects all latest task outcomes;
- automated checks and the harmless manual smoke test pass;
- the old Zsh alias and functions are removed only after the binary and config work.
