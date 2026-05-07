---
name: claudes
description: Manage Claude Code sessions via the `claudes` CLI — a screen-like session manager backed by tmux. Use when the user asks to create, list, attach, send messages to, rename, read output from, or stop background Claude Code sessions.
---

# claudes — Claude Code session manager

`claudes` is a screen-like CLI that manages background Claude Code sessions in detached tmux sessions on a dedicated socket (`-L claudes`). Sessions survive terminal restarts; you can fire off prompts and read output without ever attaching.

## When to use this skill

- The user wants to spawn, query, or kill a Claude Code session running in the background.
- The user wants to send a message to an already-running agent and read what it produced.
- The user mentions `claudes new`, `claudes send`, `claudes ls`, or similar.

For visual layout (windows, workspaces, panes, browsers, notifications), use the `cmux` skill instead. For sending input to a *non-Claude* shell, use `cmux send`/`cmux send-key`. `claudes` is specifically for Claude Code sessions.

## Commands

| Goal | Command |
|------|---------|
| List sessions | `claudes ls` |
| Create a session | `claudes new <name> -d <dir>` (omit name for picker; add `-- <claude-flags>` to pass through) |
| Attach interactively | `claudes open <name>` |
| Send a message + Enter | `claudes send <name> "<message>"` |
| Send raw keys (no Enter) | `claudes send --keys <name> <key>...` (e.g. `Escape`, `Up`, `C-c`, `BSpace`) |
| Read recent output | `claudes logs <name>` (default 50 lines; `-n N`, `-f` to follow) |
| Rename a session | `claudes rename <old> <new>` (or `claudes rename <new>` for picker) |
| Graceful stop | `claudes stop <name>` (sends `/exit`, waits, then kills) |
| Force kill | `claudes kill <name>` (alias for `stop --force`) |
| Model overrides | `claudes new ... --sonnet` / `--opus` |

Most commands accept no name and open an interactive picker.

## Sending input — important

`claudes send` is **literal by default**: `claudes send foo "Up"` types the two characters `U`,`p` and presses Enter. To send actual key presses (Escape to dismiss a dialog, arrows for history, Ctrl-C to interrupt), use `--keys`:

```
claudes send --keys foo Escape         # dismiss a dialog
claudes send --keys foo Up Up          # recall previous prompt (no submit)
claudes send --keys foo C-c            # interrupt
```

Multi-line message: pass the text as one quoted arg with embedded newlines. The body is delivered to the pane as a single bracketed-paste block (via a tmux paste buffer), so the whole thing arrives as one prompt with one trailing Enter — no per-line submits, and no size ceiling beyond what tmux itself can hold.

## Reading output

`claudes logs <name>` runs `tmux capture-pane` against the session's pane and prints what's on screen + recent scrollback. Increase scrollback with `-n 200`, or stream with `-f`. After sending a prompt, give Claude time to respond before capturing — typically `sleep 3-8s` for short replies, longer for tool-use turns.

## Typical workflows

**Fire-and-forget a prompt to a running session:**
```
claudes send my-session "summarize the last commit and write a one-line PR title"
sleep 6
claudes logs my-session -n 40
```

**Spawn a new session in a project, send first prompt:**
```
claudes new bug-fix-1 -d ~/Projects/whatever
sleep 3   # let Claude Code finish booting
claudes send bug-fix-1 "read the failing spec and propose a fix"
```

**Dismiss a stuck dialog and resend:**
```
claudes send --keys my-session Escape
claudes send my-session "retry the request"
```

## Gotchas

- **`tmux capture-pane` only sees what's currently in the pane buffer.** After a `/clear` or scroll-back overflow, earlier output is gone. Use `-n 500` if you suspect truncation.
- **Booting time.** A fresh session needs a couple seconds before Claude Code is ready to receive input. Sending too early types into a half-initialized TUI.
- **The socket is `-L claudes`.** Don't mix it up with the user's default tmux server — `tmux ls` (no `-L`) will not show claudes sessions.
- **`make install` won't overwrite an existing tmux.conf** at `~/.config/claudes/tmux.conf`. To pick up upstream config changes, copy the file manually and `tmux -L claudes source-file` it into running sessions (or restart them).
- **Sessions hold tmux config from the moment they were created.** Config changes don't apply retroactively — new sessions get the new config; existing ones need `source-file` or recreation.

## Config

`~/.config/claudes/config.toml` controls prefix, default model, projects, hooks. `~/.config/claudes/tmux.conf` is the bundled tmux config applied to every claudes session via `tmux -f`. Hook scripts (`post_new`, `post_stop`) receive `CLAUDES_NAME`, `CLAUDES_PROJECT`, `CLAUDES_DIR`, `CLAUDES_MODEL` in env.
