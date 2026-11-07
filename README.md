# Mac Upkeep

Run macOS maintenance commands concurrently in a terminal interface with live logs, cancellation, and retries.

## Prerequisites

- macOS
- [Homebrew](https://brew.sh)
- Zsh
- Each package manager or executable required by an enabled task

## Install With Homebrew

Install from the tap and check the installed version:

```sh
brew install eoleedi/tap/mac-upkeep
mac-upkeep --version
```

Homebrew does not create or modify your configuration. Copy the example without overwriting an existing file:

```sh
mkdir -p ~/.config/mac-upkeep
cp -n "$(brew --prefix mac-upkeep)/share/mac-upkeep/config.example.toml" \
  ~/.config/mac-upkeep/config.toml
```

Review `~/.config/mac-upkeep/config.toml` and disable unwanted or unavailable tasks before running—enabled tasks start immediately. Install each task's required package manager separately.

```sh
mac-upkeep
```

To upgrade:

```sh
brew update
brew upgrade eoleedi/tap/mac-upkeep
```

## Configuration

The default configuration path is `~/.config/mac-upkeep/config.toml`. Use another file with `mac-upkeep --config /path/to/config.toml`.

Set `concurrency` to the maximum number of simultaneous tasks (default: `3`, minimum: `1`). Add tasks as follows:

```toml
[[tasks]]
id = "homebrew"          # unique, non-empty identifier
name = "Homebrew"        # non-empty display name
command = "brew upgrade" # executed by /bin/zsh -lc
enabled = true            # optional; defaults to true when omitted
requires = ["brew"]       # optional requirements
```

Set `enabled = false` to skip a task. One task's failure does not stop the others.

> **Warning:** The example Oh My Zsh task sources `~/.zshrc` before calling its update functions. Review that file and the command before enabling the task.

## Keyboard Controls

| Key | Action |
| --- | --- |
| Up/Down or `j`/`k` | Select a task or scroll its focused log. |
| Enter | Focus the selected task's log. |
| Escape | Return log focus to the overview. |
| Page Up/Page Down | Scroll the selected log in overview or log focus. |
| `g`/`G` | Jump to the start/end of the selected log. |
| `c` | Cancel the selected running task. |
| `r` | Retry the selected failed or canceled task. |
| `q` or Ctrl-C | Quit when idle; request confirmation while tasks are active. |
| `y` | Confirm cancellation. |
| `n` or Escape | Dismiss cancellation. |
| `?` | Toggle concise key help. |

Logs follow new output until you scroll upward. Moving to the bottom resumes following. Once every task is terminal, the TUI remains open for log review or retries.

## Exit Codes

- `0`: All enabled tasks succeeded.
- `1`: A task failed, was canceled, was unavailable, or did not finish.
- `2`: A startup, configuration, flag, or UI error occurred.
