# claudes

a lightweight manager for your claude(s). tmux underneath.

* like `screen` for `claude` — sessions keep running after you detach.
* use `claudes logs` or `claudes send` to read/write without opening the session.
* or `claudes open` to hop in 🕳️🐇

## Quickstart

```sh
git clone https://github.com/timdavies/claudes && cd claudes
make install         # binary → ~/.local/bin, default tmux.conf → ~/.config/claudes/

# Spin up two sessions in different repos
claudes new -d ~/projects/api      # name it "api-1"
claudes new -d ~/projects/web      # name it "web-1"

claudes ls
# NAME   PROJECT  MODEL  DIR                  STATUS
# api-1  —        —      ~/projects/api       idle
# web-1  —        —      ~/projects/web       idle

# Hop into one — work as normal — Ctrl+B D to detach, Ctrl+Q to kill
claudes open api-1

# Or drive it without attaching
claudes send web-1 "run the tests and fix any failures"
claudes logs web-1 -f

# Done with it
claudes stop api-1
```

Run `claudes open` (or `stop`, `send`, `logs`) with no name and you'll get a picker.

## Config

Optional, at `~/.config/claudes/config.toml`:

```toml
[projects.myapp]
dir          = "~/projects/myapp"
default_args = ["--dangerously-skip-permissions", "--worktree"]

[projects.myapp.hooks]
post_stop = "cd $CLAUDES_DIR && git worktree prune"
```

When you're inside a project's `dir`, claudes detects it automatically and applies its settings. Sessions auto-name as `<project>-1`, `<project>-2`, …

## Bundled tmux config

`make install` drops a default `tmux.conf` at `~/.config/claudes/tmux.conf` (used only by claudes-managed sessions, not your personal tmux):

- `Ctrl+Q` — kill the session
- mouse on, 50k scrollback, no status bar

Edit freely — `make install` won't overwrite an existing file.

## Requirements

`tmux`, the `claude` CLI, Go ≥ 1.22 to build.

## More

`make completions` installs zsh/bash/fish completion scripts. `claudes --help` for the full command list.
