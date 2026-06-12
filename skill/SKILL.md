---
name: claudes
description: Manage Claude Code sessions via the `claudes` CLI — a screen-like session manager backed by tmux. Use when the user asks to create, list, attach, send messages to, rename, read output from, or stop background Claude Code sessions, or to queue/claim/hand off tasks between agents.
---

# claudes — Claude Code session manager

`claudes` is a screen-like CLI that manages background Claude Code sessions in detached tmux sessions on a dedicated socket (`-L claudes`). Sessions survive terminal restarts; you can fire off prompts and read output without ever attaching.

## When to use this skill

- The user wants to spawn, query, or kill a Claude Code session running in the background.
- The user wants to send a message to an already-running agent and read what it produced.
- The user wants to queue work for other agents to pick up, or claim/complete shared tasks (`claudes tasks`).
- The user mentions `claudes new`, `claudes send`, `claudes ls`, or similar.

For visual layout (windows, workspaces, panes, browsers, notifications), use the `cmux` skill instead. For sending input to a *non-Claude* shell, use `cmux send`/`cmux send-key`. `claudes` is specifically for Claude Code sessions.

`send` and `logs` are still accepted as aliases for `write` and `read` respectively, for backwards compatibility.

## Commands

| Goal | Command |
|------|---------|
| List sessions (with descriptions) | `claudes ls` |
| Create a session | `claudes new <name> -d <dir>` (omit name for picker; add `-- <claude-flags>` to pass through) |
| Attach interactively | `claudes open <name>` |
| Write a message + Enter | `claudes write <name> "<message>"` |
| Write raw keys (no Enter) | `claudes write --keys <name> <key>...` (e.g. `Escape`, `Up`, `C-c`, `BSpace`) |
| Read recent output | `claudes read <name>` (default 50 lines; `-n N`, `-f` to follow) |
| Rename a session | `claudes rename <old> <new>` (or `claudes rename <new>` for picker) |
| Graceful stop | `claudes stop <name>` (sends `/exit`, waits, then kills) |
| Force kill | `claudes kill <name>` (alias for `stop --force`) |
| Pick model | `claudes new ... --model <name>` (or `--haiku`/`--sonnet`/`--opus` shortcuts) |
| Group agents in `ls` | `claudes new --group <g> ...` at create time, or `claudes group <g> [name...]` to move existing agents (`default` ungroups) |
| Pin/resurrect agents | `claudes pin <name>` (survives claude exit), `claudes start <name>` (resurrect in a tab), `claudes resume <name>` (resurrect in background, no tab), `claudes unpin <name>` |
| Manage projects | `claudes project {list,add,rm,show}` |
| Read/write top-level config | `claudes config {show,get,set}` |
| Manage the daemon | `claudes daemon {status,start,stop,logs}` |
| Cross-agent task queue | `claudes tasks` (dashboard) / `claudes tasks {add,claim,complete,rm,show,ls}` |

Most commands accept no name and open an interactive picker.

## Default model + auto-summarizing daemon

`claudes` always passes `--model` to the spawned `claude` (default `opus`). Without this, a Haiku-running parent would silently produce Haiku sessions by inheritance. Override per-session via `--model X` or shortcuts.

A background daemon auto-spawns on first `claudes new`/`open`/`ls` and self-exits when no sessions remain. Every minute (or `CLAUDES_DAEMON_TICK=15s` for dev) it captures each session's pane, hashes it, and asks `claude -p --model haiku` for an 8-12 word description. The result is written to the tmux session env (`@claudes-description`) and shown in `claudes ls`. State files live in `~/.cache/claudes/`. To force-stop the daemon: `claudes daemon stop`. It'll respawn next time you run `claudes ls` (assuming sessions exist).

## Cost tracking

`claudes ls` shows the cost per agent (e.g. `$12.47`) next to its model. At spawn, claudes assigns the Claude Code session UUID (`claude --session-id`), and the daemon shells out to [ccusage](https://github.com/ryoppippi/ccusage) once per tick (`ccusage session --json`, or `npx ccusage` if it isn't installed globally), matching each session's `period` (the UUID) to its `totalCost` and stamping it into the session env (`@claudes-cost`). ccusage reads the same transcripts Claude Code writes and uses maintained pricing, so the figure matches Claude Code's own billing. No ccusage / no node → no cost shown (degrades quietly). Sessions created before this feature (no stamped UUID) show no cost; `$0.00` is suppressed.

## Writing input — important

`claudes write` is **literal by default**: `claudes write foo "Up"` types the two characters `U`,`p` and presses Enter. To send actual key presses (Escape to dismiss a dialog, arrows for history, Ctrl-C to interrupt), use `--keys`:

```
claudes write --keys foo Escape         # dismiss a dialog
claudes write --keys foo Up Up          # recall previous prompt (no submit)
claudes write --keys foo C-c            # interrupt
```

Multi-line message: pass the text as one quoted arg with embedded newlines. The body is delivered to the pane as a single bracketed-paste block (via a tmux paste buffer), so the whole thing arrives as one prompt with one trailing Enter — no per-line submits, and no size ceiling beyond what tmux itself can hold.

## Reading output

`claudes read <name>` runs `tmux capture-pane` against the session's pane and prints what's on screen + recent scrollback. Increase scrollback with `-n 200`, or stream with `-f`. After writing a prompt, give Claude time to respond before reading — typically `sleep 3-8s` for short replies, longer for tool-use turns.

## Typical workflows

**Fire-and-forget a prompt to a running session:**
```
claudes write my-session "summarize the last commit and write a one-line PR title"
sleep 6
claudes read my-session -n 40
```

**Spawn a new session in a project, write the first prompt:**
```
claudes new bug-fix-1 -d ~/Projects/whatever
sleep 3   # let Claude Code finish booting
claudes write bug-fix-1 "read the failing spec and propose a fix"
```

**Dismiss a stuck dialog and resend:**
```
claudes write --keys my-session Escape
claudes write my-session "retry the request"
```

## Cross-agent task queue

`claudes tasks` is a shared queue so agents (or you) can hand work to each other. State lives in `~/.cache/claudes/tasks.json`, guarded by a file lock — `claim` is a safe compare-and-set, so two agents can't grab the same task.

```
claudes tasks add "wire up the export endpoint"        # open queue — anyone can claim
claudes tasks add --to bob "review the migration"      # directed at bob (also pokes bob to claim)
claudes tasks claim                                     # claim the oldest task open to you
claudes tasks claim 3                                   # claim a specific task by id
claudes tasks complete --result "done, see PR #42"      # complete your claimed task; reports back to its creator
claudes tasks complete 3                                # complete a specific task
claudes tasks ls                                        # plain list; `claudes tasks` alone = live dashboard
```

- **Identity** (who created/claimed) is the current session name (same as `claudes whoami`). From a plain shell, pass `--as <name>`; humans can create/complete but not claim.
- **Report-back**: `complete` messages the task's creating agent via `claudes write` (skipped when the creator is a human or no longer running).
- **Completing** with no id picks your single claimed task; if you have several, pass the id. The dashboard's `x` key completes without a note — use the CLI `--result` to attach one.

## Agent groups

Every agent belongs to a group; the default group is implicit and renders flush at the top of `claudes ls` with no header. Any other group (e.g. `review`, `background`) gets its own labelled section below, so related agents cluster together. Set a group at create time with `claudes new --group review`, or move agents later with `claudes group review one two three`. `claudes group default <name>` (or `claudes group "" <name>`) drops an agent back to the top group. The group lives in the session env for live agents and in the pin registry for paused ones, so it survives refreshes and resurrection.

## Gotchas

- **`tmux capture-pane` only sees what's currently in the pane buffer.** After a `/clear` or scroll-back overflow, earlier output is gone. Use `-n 500` if you suspect truncation.
- **Booting time.** A fresh session needs a couple seconds before Claude Code is ready to receive input. Sending too early types into a half-initialized TUI.
- **The socket is `-L claudes`.** Don't mix it up with the user's default tmux server — `tmux ls` (no `-L`) will not show claudes sessions.
- **`make install` won't overwrite an existing tmux.conf** at `~/.config/claudes/tmux.conf`. To pick up upstream config changes, copy the file manually and `tmux -L claudes source-file` it into running sessions (or restart them).
- **Sessions hold tmux config from the moment they were created.** Config changes don't apply retroactively — new sessions get the new config; existing ones need `source-file` or recreation.

## Config

`~/.config/claudes/config.toml` controls prefix, default model, projects, hooks. Editable via `claudes config set <key> <value>` (top-level scalars) and `claudes project add <name> --dir <path>` (project stanzas) — no need to hand-edit TOML. `claudes config show` prints the current effective config. `~/.config/claudes/tmux.conf` is the bundled tmux config applied to every claudes session via `tmux -f`. Hook scripts (`post_new`, `post_stop`) receive `CLAUDES_NAME`, `CLAUDES_PROJECT`, `CLAUDES_DIR`, `CLAUDES_MODEL` in env.
