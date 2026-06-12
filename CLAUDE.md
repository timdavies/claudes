# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`claudes` is a screen-like CLI for managing parallel Claude Code sessions. It's a thin Go wrapper around tmux: every "session" is a detached tmux session running `claude`, scoped to a dedicated socket (`-L claudes` by default) so it doesn't mix with the user's personal tmux server. The wrapper itself stores no state — tmux is the source of truth; `claudes` only adds naming, project resolution, hooks, and ergonomics.

## Build & develop

```sh
go build ./...        # build
go vet ./...          # vet
go test ./...         # tests (currently sparse)
make install          # build → ~/.local/bin, install skill, install bundled tmux.conf if missing
make install-skill    # just sync skill/SKILL.md → ~/.claude/skills/claudes/SKILL.md
```

`make install` will **not** overwrite an existing `~/.config/claudes/tmux.conf`. To pick up tmux config changes after editing `tmux.conf` in this repo, copy it manually and `tmux -L claudes source-file ~/.config/claudes/tmux.conf` into running sessions (or restart them — existing sessions don't pick up config changes retroactively).

## Architecture

Three layers, top-down:

- **`cmd/`** — Cobra subcommands (`new`, `open`, `ls`, `write` (aliased `send`), `read` (aliased `logs`), `stop`/`kill`, `rename`, `pin`/`unpin`/`start`/`resume`, `group`, `project`, `config`, `daemon`, `tasks`, `status`). Each is mostly argument parsing + picker logic + a call into `internal/`. Most commands accept a session name or fall through to an interactive picker (`internal/picker`); a missing TTY or `CLAUDES_NO_INTERACTIVE=1` skips the picker.
- **`internal/config`** — TOML config at `~/.config/claudes/config.toml`. The important method is `Config.Resolve(explicitDir, projectFlag, cwd) Resolved` — it merges global defaults with the project that matches `cwd` (or the explicit `--project` flag) and returns the per-invocation settings. Project `hooks` *replace* global hooks (not merge); project `default_args` likewise replace.
- **`internal/tmux`** — Wraps `tmux` CLI calls against the configured socket. `Client.cmd(args...)` is the helper everything goes through; it prepends `-L <socket>` and `-f <tmux_config>`. The non-obvious method is **`SendKeys`**: it routes the message body through `load-buffer -` (stdin) + `paste-buffer -p -d` rather than `send-keys -l`, then sends Enter as a separate keypress. This is deliberate — `send-keys -l` (a) hits the OS argv limit at ~17KB and (b) makes tmux 3.4+ split long literal text into multiple bracketed-paste chunks, which causes Claude Code's TUI to consume the trailing Enter as paste-edit-mode exit instead of submitting. `SendRawKeys` (used by `send --keys`) is the raw-key path with no Enter, no buffer routing.

- **`internal/tasks`** — A cross-agent task queue persisted at `~/.cache/claudes/tasks.json`. This is durable, mutable shared state (unlike most of claudes, where tmux is the source of truth — it follows the same flock + atomic-write pattern as `internal/pinned`). Every mutation is a read-modify-write inside an exclusive `flock`, which is what makes `claim` a safe compare-and-set: two agents racing to claim the same task can't both win. `cmd/tasks.go` is the subcommand surface (`add`/`claim`/`complete`/`rm`/`show`/`ls`); `cmd/tasks_tui.go` is the actionable dashboard. Identity (creator/claimant) comes from `currentSessionName`; `complete` reports back to the creator via the same `SendKeys` path `claudes write` uses.

- **`internal/cost`** — Reports per-session cost by shelling out to [ccusage](https://github.com/ryoppippi/ccusage) (`ccusage session --json`, falling back to `npx ccusage`). claudes assigns the Claude Code session UUID at spawn (`claude --session-id`, stamped as `CLAUDES_SESSION_ID`); ccusage keys each session by `period` (that same UUID), so `SessionCosts` returns a `uuid → totalCost` map the daemon looks up directly. Sessions created before `--session-id` existed are backfilled by `ResolveSessionID` (newest unclaimed transcript in the cwd's project dir) and the recovered UUID is stamped so the next tick is a direct lookup. ccusage reads the transcripts Claude Code writes and applies maintained pricing, so the figure matches Claude Code's own billing — claudes carries no pricing table of its own. The daemon refreshes it each tick and stamps `@claudes-cost` (only when changed), which `claudes ls`/TUI render beside the model. ccusage/node absent → empty map, no cost shown.

`internal/session` is thin glue between tmux's session listing and the config's project/model inference.

## Session lifecycle

1. `claudes new` runs `cfg.Resolve(...)`, builds a `cmdline` of `["claude" + default_args + "--model" model + passthrough...]`, calls `tmux new-session -d -s <prefix><name> -c <dir> ... cmdline`. tmux daemonizes; the pane runs `claude` directly (no shell in between).
2. `claudes open` is a `syscall.Exec` to `tmux attach-session` with `os.Environ()` — the parent process is replaced.
3. `claudes stop` sends `/exit` via `SendKeys` and waits up to `stop_timeout` seconds before falling back to `tmux kill-session`. `claudes kill` is `stop --force`.

The `pickSession` helper in `cmd/stop.go` is shared by stop/rename/send/logs — it accepts a name argument, tolerates names not in the listing (for stale-state cleanup), and otherwise launches the picker.

## Bundled tmux config

`tmux.conf` is shipped with the repo and installed (only if absent) to `~/.config/claudes/tmux.conf`. Notable bindings:

- `Ctrl-Q` (no prefix) kills the current session.
- Mouse wheel enters copy-mode for scrollback (passes through to alternate-screen apps like vim).
- `extended-keys always` + `terminal-features ',xterm*:extkeys'` so apps can distinguish Shift+Enter etc.
- Mouse drag-end copies to `pbcopy` and exits copy-mode (highlight vanishes — that's a tmux limitation; hold Option for terminal-native selection).

## Important behaviors to preserve

- **Sessions exit when `claude` exits.** There's no shell wrapping the pane — once `claude` quits, the pane and session go away. Don't add a shell layer without a strong reason; it would break `stop`'s `/exit`-then-wait path and complicate env handling.
- **`SendKeys` always appends Enter.** Its empty-text branch is used by `stop` to send a bare Enter to confirm something. The `--keys` flag goes through `SendRawKeys` instead, which doesn't.
- **`make install` won't clobber the user's `tmux.conf`.** This is intentional (sharing setups, customization). Mention this when telling the user to pick up config changes.

## Conventions

- Branch names: simple, dash-separated, no username prefix. e.g. `fix-send-paste-buffer`, not `tim/fix-paste`.
- Cobra commands: keep them small; push logic into `internal/`.
- `dangerouslyDisableSandbox: true` is needed for `go build`, `go vet`, `go test`, and `make` calls because Go's build cache and `~/.local/bin` are outside the default sandbox. `claudes` itself, `gh`, `bk`, `cmux` all run in-sandbox.
