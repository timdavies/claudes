# claudes

a lightweight manager for your claude(s). tmux underneath.

* like `screen` for `claude` — sessions keep running after you detach.
* use `claudes read` or `claudes write` to read/write without opening the session.
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
claudes write web-1 "run the tests and fix any failures"
claudes read web-1 -f

# Done with it
claudes stop api-1
```

Run `claudes open` (or `stop`, `write`, `read`) with no name and you'll get a picker. `send` and `logs` are kept as aliases for `write` and `read`.

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

## Pinned agents

```sh
claudes new --pin foo      # create + pin
claudes pin foo            # pin a live agent
claudes unpin foo          # remove the pin
claudes start foo          # resurrect a paused pinned agent
```

A **pinned** agent survives its claude process exiting (Ctrl+D, `/exit`, crash, or `claudes stop`). The tmux session goes away but the entry stays in `claudes ls` as `paused` with a 📌. `claudes start <name>` recreates the tmux session with the same model, project, dir, and cmdline as before. Unpinned (default) agents disappear when their session ends, same as always.

State lives at `~/.cache/claudes/pinned.json` — safe to inspect or hand-edit.

## macuake integration (macOS)

Opt-in. When enabled, every `claudes new` opens a [macuake](https://macuake.com) tab attached to the tmux session; `claudes stop` (or the agent self-exiting) closes it.

```toml
[macuake]
enabled = true
# socket = "/tmp/macuake.sock"   # default; usually omit
```

Requires macuake's socket API to be turned on in its Settings. When the socket is unreachable, claudes logs one warning and continues without a tab — session creation never fails on macuake errors. The daemon's reconciler closes orphan tabs every 5s; override with `CLAUDES_MACUAKE_TICK=2s` etc.

## Bundled tmux config

`make install` drops a default `tmux.conf` at `~/.config/claudes/tmux.conf` (used only by claudes-managed sessions, not your personal tmux):

- `Ctrl+Q` — kill the session
- mouse on, 50k scrollback, no status bar

Edit freely — `make install` won't overwrite an existing file.

## Requirements

`tmux`, the `claude` CLI, Go ≥ 1.22 to build.

## More

`make completions` installs zsh/bash/fish completion scripts. `claudes --help` for the full command list.
