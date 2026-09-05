# ua

`ua` is a terminal interface for running a configured set of update commands concurrently. It starts enabled tasks immediately, keeps each task's live log available, and reports an aggregate result when you quit.

## Prerequisites

- macOS
- Go 1.27 for source builds
- Zsh
- Each package manager or executable required by an enabled task

The example configuration includes Homebrew, Conda, ruby-install, npm, `mas`, Poetry, Oh My Zsh, and `npx skills`. Disable tasks whose requirements are unavailable before running `ua`.

## Build And Install

Run the automated checks and install the binary:

```sh
make test
make install
```

Installation writes the binary to `~/.local/bin/ua`. It creates `~/.config/ua/config.toml` from `config.example.toml` only when that file does not already exist, so later installs preserve local configuration.

Ensure `~/.local/bin` is on `PATH`, then run:

```sh
ua
```

## Configuration

The default configuration path is `~/.config/ua/config.toml`. Use another file with `ua --config /path/to/config.toml`.

The global `concurrency` value must be at least `1` and defaults to `3` when omitted. Tasks run in configuration order, with no more than `concurrency` commands active at once. Each task uses this schema:

```toml
[[tasks]]
id = "homebrew"          # unique, non-empty identifier
name = "Homebrew"        # non-empty display name
command = "brew upgrade" # executed by /bin/zsh -lc
enabled = true            # optional; defaults to true when omitted
requires = ["brew"]       # optional executable names checked on PATH
```

Disabled tasks remain in the file but do not participate in a run. To disable an unavailable updater, add `enabled = false` to its task entry:

```toml
[[tasks]]
id = "conda"
name = "Conda"
command = "conda update conda python --all -y"
requires = ["conda"]
enabled = false
```

The TUI enters its alternate screen and starts all enabled tasks immediately. A completed task frees its concurrency slot for the next queued task; one failure does not stop unrelated tasks.

> **Warning:** The example Oh My Zsh task sources `~/.zshrc` before calling its update functions. Review that file and the command before enabling the task.

## Keyboard Controls

| Key | Action |
| --- | --- |
| Up/Down or `j`/`k` | Select a task while the overview is focused. |
| Enter | Focus the selected task's log. |
| Escape | Return log focus to the overview. |
| Page Up/Page Down | Scroll the selected log in overview or log focus. |
| Up/Down or `j`/`k` | Scroll one line while the log is focused. |
| `g`/`G` | Jump to the start/end of the selected log. |
| `c` | Cancel only the selected running task; the other tasks and manager keep running. |
| `r` | Retry the selected failed or canceled task. |
| `q` or Ctrl-C | Quit when idle; request confirmation while tasks are active. |
| `y` | Confirm cancellation. |
| `n` or Escape | Dismiss cancellation. |
| `q` or Ctrl-C at confirmation | Confirm cancellation immediately. |
| `?` | Toggle concise key help. |

Logs follow new output until you scroll upward. Moving to the bottom resumes following. Once every task is terminal, the TUI remains open for log review or retries.

`ua` attaches child stdout and stderr to an output-only pseudo-terminal, so commands emit the same native colors they use in a standard shell while stdin remains non-interactive. It also requests color with the conventional `CLICOLOR`, `CLICOLOR_FORCE`, `FORCE_COLOR`, and `HOMEBREW_COLOR` environment variables. Existing values are preserved, so setting `FORCE_COLOR=0 ua` disables force-color behavior for tools that honor it. Safe ANSI styles, including basic, 256-color, and truecolor SGR sequences, are rendered in the log pane; cursor-control sequences are removed to protect the TUI layout.

## Status And Exit Codes

Tasks move through these states:

| Status | Meaning |
| --- | --- |
| `queued` | Waiting for a concurrency slot. |
| `running` | The command is active. |
| `succeeded` | The command exited with status `0`. |
| `failed` | The command exited nonzero or could not start. |
| `canceled` | The task was stopped individually or by application cancellation. |
| `unavailable` | A declared requirement was not found on `PATH`. |

The final process exit code is `0` only when every enabled task's latest result is `succeeded`. It is `1` if any enabled task is failed, canceled, unavailable, or otherwise incomplete. Startup, configuration, flag, and UI errors return `2`.

## Retry And Cancellation

Press `c` on a running task to stop only that task's process group while the remaining queue continues. The task becomes canceled after process cleanup finishes. Press `r` on a failed or canceled task to clear its old log and timing data, recheck requirements, and queue it under the same concurrency limit. A task whose requirement is still missing becomes unavailable.

Quitting while work is active requires confirmation. Confirming marks queued tasks canceled and sends `SIGTERM` to every running command's process group. After a two-second grace period, `ua` sends `SIGKILL` to any surviving process groups and waits for cleanup before restoring the terminal. At the confirmation prompt, a second `q` or Ctrl-C skips the prompt but still performs managed cleanup.

External SIGINT, SIGTERM, and SIGHUP bypass confirmation and use the same managed cleanup. Bubble Tea's signal handler is disabled so `ua` is the sole owner of those signals. Normal cancellation keeps the event loop active until process cleanup completes and every terminal task status has been applied. If Bubble Tea itself returns an unexpected error or recovers a panic, fallback cleanup still completes before `ua` leaves its application-owned alternate screen; Bubble Tea may already have restored raw input mode and the cursor internally because its API does not expose a pre-restore error hook.

## Replacing Shell Functions

Do not remove an existing shell implementation until the new binary has passed automated and harmless smoke verification:

1. Run `make test` and `make install`.
2. Create a temporary configuration containing only harmless commands such as `printf` and `sleep`; do not invoke real updaters during the smoke test.
3. Run `ua --config /path/to/smoke.toml` and verify live output, selection, scrolling, failure and retry, selected-task cancellation and retry, resizing, and confirmed cancellation.
4. Run `ua --config ~/.config/ua/config.toml` only after reviewing and enabling the updater tasks you intend to use.
5. After verification succeeds, remove `alias -- ua=update_all`, `update_all`, and `update_all_parallel` from `~/.zshrc`.

No compatibility wrapper is needed when `~/.local/bin` is already on `PATH`.
