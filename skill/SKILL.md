---
name: claudes
description: Manage Claude Code sessions via the `claudes` CLI — a screen-like session manager backed by tmux. Use when the user asks to create, list, attach, send messages to, rename, read output from, or stop background Claude Code sessions, or to set up recurring scheduled prompts.
---

# claudes — Claude Code session manager

`claudes` is a screen-like CLI that manages background Claude Code sessions in detached tmux sessions on a dedicated socket (`-L claudes`). Sessions survive terminal restarts; you can fire off prompts and read output without ever attaching.

## When to use this skill

- The user wants to spawn, query, or kill a Claude Code session running in the background.
- The user wants to send a message to an already-running agent and read what it produced.
- The user wants to set up a recurring scheduled prompt — run a prompt every N minutes / daily / once at a time (`claudes tasks`).
- The user mentions `claudes new`, `claudes send`, `claudes ls`, or similar.

For visual layout (windows, workspaces, panes, browsers, notifications), use the `cmux` skill instead. For sending input to a *non-Claude* shell, use `cmux send`/`cmux send-key`. `claudes` is specifically for Claude Code sessions.

`send` and `logs` are still accepted as aliases for `write` and `read` respectively, for backwards compatibility.

## Commands

| Goal | Command |
|------|---------|
| List sessions (with descriptions) | `claudes ls` |
| Create a session | `claudes new <name> -d <dir>` (omit name for picker; add `-- <claude-flags>` to pass through). Defaults the agent into its own git worktree — see [Worktrees](#worktrees); `--no-worktree` (alias `--in-place`) to run in the checkout itself |
| Attach interactively | `claudes open <name>` |
| Write a message + Enter | `claudes write <name> "<message>"` |
| Write raw keys (no Enter) | `claudes write --keys <name> <key>...` (e.g. `Escape`, `Up`, `C-c`, `BSpace`) |
| Read recent output | `claudes read <name>` (default 50 lines; `-n N`, `-f` to follow) |
| Report your own activity/state | `claudes status "<activity>" [--state working\|waiting\|blocked\|done]` (run from inside the session; `--clear` to reset) |
| Attach a PR to your session | `claudes pr [url\|number]` (run from inside the session; no arg auto-detects the branch's PR via `gh`; `--clear` to detach). Shows as `#ID` left of the model in `ls`; in the TUI arrow right onto it and press enter to open in the browser. **Prefer the full URL form** (`claudes pr https://github.com/o/r/pull/123`) over the bare number — the number form shells out to `gh` to resolve the PR and that lookup flakes when `gh` auth runs in a subprocess/sandbox (`could not resolve PR "123" via gh`); the URL form skips the lookup. |
| Rename a session | `claudes rename <old> <new>` (or `claudes rename <new>` for picker) |
| Graceful stop | `claudes stop <name>` (sends `/exit`, waits, then kills) |
| Force kill | `claudes kill <name>` (alias for `stop --force`) |
| Pick model | `claudes new ... --model <name>` (or `--haiku`/`--sonnet`/`--opus` shortcuts) |
| Group agents in `ls` | `claudes new --group <g> ...` at create time, or `claudes group <g> [name...]` to move existing agents (`default` ungroups) |
| Pin/resurrect agents | `claudes pin <name>` (survives claude exit), `claudes start <name>` (resurrect in a tab), `claudes resume <name>` (resurrect in background, no tab), `claudes unpin <name>` |
| Manage projects | `claudes project {list,add,rm,show}` |
| Read/write top-level config | `claudes config {show,get,set}` |
| Manage the daemon | `claudes daemon {status,start,stop,logs}` |
| Recurring scheduled prompts | `claudes tasks {add,ls,edit,enable,disable,run,logs,rm}` (or the main TUI's schedules section) |

Most commands accept no name and open an interactive picker.

## Worktrees

`claudes new` (including with `--project`) defaults each agent into its **own git worktree**, so parallel agents never fight over one checkout. The worktree is named off the session: path `<repo>-worktrees/<name>`, branch `<name>` (e.g. session `CACT-3688` → `…/grow-worktrees/CACT-3688` on branch `CACT-3688`). The worktree path becomes the session's working dir, so `CLAUDES_DIR`, hooks, `ls`, `status`, and cost all reflect it.

- **Opt out** with `--no-worktree` (alias `--in-place`) — runs in the checkout itself. Use this for agents that legitimately need the main repo (e.g. "test in the main checkout").
- **Fallbacks (automatic):** a non-git dir runs in place silently; any `git worktree add` failure falls back in place with a one-line warning. The add is *attempted* even from a sandboxed shell — it succeeds wherever the path is write-allowlisted (e.g. `~/Projects/grow-worktrees`) and only degrades to in-place on a real EPERM. The spawn never aborts over worktree setup.
- **Reuse, not recreate:** the worktree path is persisted with the pin, so `claudes start`/`resume` reuse the same worktree. It is **never** auto-torn-down on stop (it holds unpushed branches / uncommitted work) — clean up stale ones with `/git-tidy`.
- **Copy personal context in:** `git worktree add` only brings *tracked* files, so untracked/gitignored per-repo context (`CLAUDE.local.md`, `.env`, …) is missing in a fresh worktree. Declare paths to carry over per project with `worktree_copy` (paths relative to the project dir):
  ```toml
  [projects.grow]
  dir = "/Users/timdavies/Projects/grow"
  worktree_copy = ["CLAUDE.local.md", ".env"]
  ```
  On worktree **creation** (not reuse) claudes copies each into the same relative path, making parent dirs as needed; missing sources are skipped silently and a copy failure never aborts the spawn. Settable on new projects via `claudes project add --worktree-copy <path>` (repeatable).

## Default model + scheduler daemon

`claudes` always passes `--model` to the spawned `claude` (default `opus`). Without this, a Haiku-running parent would silently produce Haiku sessions by inheritance. Override per-session via `--model X` or shortcuts.

A background daemon hosts the scheduler. It auto-spawns when you add/enable a scheduled prompt (`claudes daemon start` to spawn manually) and self-exits once there are no live sessions, no enabled schedules, and no run in flight. Each tick (default 60s; `CLAUDES_DAEMON_TICK=10s` for dev) it fires due schedules and tears down finished runs. State files live in `~/.cache/claudes/`; force-stop with `claudes daemon stop`, inspect with `claudes daemon logs`.

- **Liveness** is decided by the pidfile flock + heartbeat, not `kill(0)` — so `status`/`stop`/`start` stay correct even from a sandboxed shell. `stop` errors (rather than lying "stopped") if it can't signal the daemon.
- **Don't auto-spawn it from a sandboxed Claude Code Bash call** — a daemon inherits the sandbox and every run's `git worktree add` fails with EPERM. `claudes` refuses and tells you to start it from a real shell (`! claudes daemon start`). Repeated fire failures surface as a ⚠ in `daemon status` / `claudes ls` / the TUI.
- **Cost** (`$` per session in `claudes ls`) is stamped by the daemon and **on by default**; disable with `[daemon]\ncost = false` in `config.toml`. The heavier ambient jobs (Haiku pane summaries + tab reconcile) stay off unless `CLAUDES_DAEMON_AMBIENT=1`.

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

## Self-reporting status

By default the daemon *guesses* each session's description by summarizing its pane every minute, and the rail color in `claudes ls` is always blue ("idle") for any live agent — it can't tell working from blocked. An agent can override both by reporting for itself:

```
claudes status "scraping the firm homepage"             # set the activity line
claudes status --state blocked "waiting on CI to go green"  # activity + state
claudes status --state working                          # just the state
claudes status --clear                                  # hand control back to the daemon
```

- Run it **from inside the session** — it targets the current session (same resolution as `claudes whoami`), no name argument.
- A self-reported activity takes precedence over the daemon's ambient summary in `claudes ls`/the dashboard; the daemon keeps refreshing underneath and reappears once you `--clear`.
- The `--state` keyword tints the left rail: `working`→green, `waiting`/`blocked`→yellow, `done`/`idle`→blue. Any other word still shows as a `[chip]` but doesn't change the color.
- Good practice for long-running agents: call it when you start a phase ("running migration"), when you get stuck (`--state blocked`), and when you finish (`--state done`). It's how a human glancing at `claudes ls` knows what each agent is up to without attaching.

## Scheduled prompts (`claudes tasks`)

`claudes tasks` is recurring scheduled prompts: save a prompt + a cadence, and the daemon fires it on schedule. Each firing spawns an ephemeral `claude -p` session **in its own throwaway git worktree** (`<repo>/../<repo>-worktrees/<name>-<ts>`), captures its output, then tears the worktree down. State lives in `~/.cache/claudes/schedules.json`; per-run output in `~/.cache/claudes/schedules/<id>/<runID>.log`.

```
claudes tasks add --kind interval --every 5m  --name lint    --dir <repo> --prompt "..."   # every 5m, 9–18 window
claudes tasks add --kind daily    --at 09:00   --name standup --dir <repo> --prompt "..."   # daily at 09:00
claudes tasks add --kind daily    --at 09:00 --days mon       --name weekly --dir <repo> --prompt "..."  # Mondays only
claudes tasks add --kind daily    --at 09:00 --days mon,thu   --name twiceweek --dir <repo> --prompt "..." # Mon + Thu
claudes tasks add --kind once     --at "2026-06-20 14:00" --name oneoff --dir <repo> --prompt "..."
claudes tasks ls                       # id, name, cadence, enabled, next-fire, last-run status
claudes tasks enable 3 / disable 3     # toggle
claudes tasks run 3                    # fire now (ignores window + enabled)
claudes tasks logs 3                   # run history; `--run <id>` dumps one run's captured output
claudes tasks rm 3                     # delete schedule + its run history
```

- **Kinds**: `interval` (`--every 5m`), `daily` (`--at HH:MM`), `once` (`--at <datetime>`). `interval` only fires inside the active window (default 9–18, override `--window 9-18`); `once` ignores the window and auto-disables after firing.
- **Day-of-week**: add `--days mon` (or `--days mon,thu`) to restrict which weekdays a task fires. Works on both `daily` **and** `interval` kinds: a daily task fires only on those days (e.g. `mon 09:00`); an interval task fires only on those days *within* its hour window (e.g. `every 15m · 7–19 · weekdays`) — off days are skipped entirely, no wasted fires. Works on `tasks add` and `tasks edit` (empty `--days` on edit clears back to every day). Day names: `mon tue wed thu fri sat sun`; Mon–Fri renders as `weekdays`, Sat+Sun as `weekends`.
- **Permissions**: runs use `claude -p --permission-mode auto` so they don't hang on prompts (override with `--perm`). Needs Claude Code ≥2.1.83 and Opus 4.6+/Sonnet 4.6+.
- **Overlap**: if a schedule's previous run is still live, the next fire is skipped.
- **`--dir` must be a git repo** (the worktree hangs off its HEAD). Fully editable from the main TUI's bottom **schedules** section — `enter` opens a schedule's run logs; mutating actions need **shift** so a stray keystroke can't fire one: `N`/`E`/`X`/`T`/`R` add/edit/delete/toggle/run-now.

## Agent groups

Every agent belongs to a group; the default group is implicit and renders flush at the top of `claudes ls` with no header. Any other group (e.g. `review`, `background`) gets its own labelled section below, so related agents cluster together. Set a group at create time with `claudes new --group review`, or move agents later with `claudes group review one two three`. `claudes group default <name>` (or `claudes group "" <name>`) drops an agent back to the top group. The group lives in the session env for live agents and in the pin registry for paused ones, so it survives refreshes and resurrection.

**Convention: always put review agents in a `review` group.** When you spawn an agent whose job is to review a PR, diff, or another agent's work, create it with `claudes new --group review ...` (or `claudes group review <name>` after the fact). This keeps review agents visually separated from the agents doing the primary work, so the list stays legible at a glance.

## Gotchas

- **`tmux capture-pane` only sees what's currently in the pane buffer.** After a `/clear` or scroll-back overflow, earlier output is gone. Use `-n 500` if you suspect truncation.
- **Booting time.** A fresh session needs a couple seconds before Claude Code is ready to receive input. Sending too early types into a half-initialized TUI.
- **The socket is `-L claudes`.** Don't mix it up with the user's default tmux server — `tmux ls` (no `-L`) will not show claudes sessions.
- **`make install` won't overwrite an existing tmux.conf** at `~/.config/claudes/tmux.conf`. To pick up upstream config changes, copy the file manually and `tmux -L claudes source-file` it into running sessions (or restart them).
- **Sessions hold tmux config from the moment they were created.** Config changes don't apply retroactively — new sessions get the new config; existing ones need `source-file` or recreation.

## Config

`~/.config/claudes/config.toml` controls prefix, default model, projects, hooks. Editable via `claudes config set <key> <value>` (top-level scalars) and `claudes project add <name> --dir <path>` (project stanzas) — no need to hand-edit TOML. `claudes config show` prints the current effective config. `~/.config/claudes/tmux.conf` is the bundled tmux config applied to every claudes session via `tmux -f`. Hook scripts (`post_new`, `post_stop`) receive `CLAUDES_NAME`, `CLAUDES_PROJECT`, `CLAUDES_DIR`, `CLAUDES_MODEL` in env.
