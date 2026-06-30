# Proposal: `claudes pane` — a long-lived tabbed right-pane view

Status: **proposal** (no code yet). Author: claudes-dev, for Tim's review.

## TL;DR — recommended approach

Build `pane` as **option (a): pane is its own tmux session that acts as the tab
container, and its "session" tab holds a nested client attached to the active
agent, retargeted via `respawn-pane`.** Signal the active agent through a
**watched state-file** (`~/.cache/claudes/pane.json`), written by the LEFT TUI's
enter-handler, owned/applied by pane. Gate everything behind a new
**`[features] pane = true`** flag; default off = today's behavior unchanged.

Rationale in one line: claudes' whole design is "a thin wrapper around tmux; tmux
is the source of truth" (see CLAUDE.md). tmux already solves the genuinely hard
parts here — rendering an interactive session, resize/SIGWINCH, scrollback, and
a tab strip (its window list/status bar) — so we lean on it instead of
re-implementing a terminal emulator. The bespoke-Go-TUI option would betray that
philosophy and carries the most code and risk for no proportional gain.

---

## 1. The core display problem & the three options

Showing a **live, interactive** agent (already a tmux session on `-L claudes`)
inside pane's own tab chrome is nested-tmux territory. Evaluated:

### (a) Nested tmux — **RECOMMENDED**
- `pane` is a tmux session on a **separate socket** (`-L claudes-pane`) with two
  windows = two tabs: window 0 = task list (stub), window 1 = "session".
- Window 1's single pane runs a nested client: `tmux -L claudes attach -t claudes-<active>`.
- **Tab strip for free**: pane-tmux's status bar / window list *is* the tab strip.
- **Retarget for free**: on active-agent change, pane runs
  `tmux -L claudes-pane respawn-pane -k -t pane:session 'tmux -L claudes attach -t claudes-NEW'`.
  No bespoke teardown — tmux replaces the pane's process cleanly.
- **Resize/redraw for free**: tmux propagates SIGWINCH to the nested client.
- Cost: the known nested-tmux ergonomics — prefix collision and double status
  bar — both **fully mitigable** (see §3): give pane-tmux a distinct prefix or
  no-prefix tab keys, and turn the inner agent's status bar off.
- Net: ~small Go surface (a `pane` subcommand that builds the tmux session,
  watches the state-file, and issues `respawn-pane`); tmux does the heavy lifting.

### (b) `switch-client` retarget — rejected as primary
- The right-pane client just `switch-client -t claudes-X` to change agent.
- Cheapest, but pane stops being a cohesive program that *owns* persistent tab
  chrome — the task-list tab would have to be "just another session" you
  switch to, not a tab pane owns. Conflicts with the tabbed model Tim wants.
- Keep in pocket as a degenerate fallback; it's essentially (a) without tabs.

### (c) Bespoke Go TUI + embedded PTY — rejected (most code/risk)
- pane is a bubbletea app drawing its own tab strip and compositing the agent
  into a sub-rectangle via a PTY.
- To draw a tab strip *and* a live session on the same screen you need a real
  vt100 emulator (e.g. vt10x) to composite the agent into a region — that's a
  terminal emulator inside a TUI, plus key-forwarding, plus resize math. Most
  control, by far the most code, and duplicates what tmux already does well.
- Only worth it if we later need rich cross-session chrome tmux can't express.

---

## 2. Signalling: how the LEFT TUI says "active = X"

**Recommended: a watched state-file**, `~/.cache/claudes/pane.json`:

```json
{ "active": "leo", "updated_at": "2026-06-29T..." }
```

- Written atomically by the LEFT TUI's enter-handler using the **same flock +
  temp-file-rename pattern** already used by `internal/pinned` and the iterm2 tab
  registry. One writer at a time, no torn reads.
- pane watches it (poll every ~200ms for v1 — dependency-free; fsnotify later if
  needed) and applies changes via `respawn-pane`.

Why state-file over the alternatives:
- **vs direct tmux command** (TUI runs `respawn-pane` against pane-tmux itself):
  tightly couples the TUI to pane's internal layout/socket and breaks when pane
  isn't running. The TUI should declare *intent* ("active = X"), not reach into
  pane's guts.
- **vs IPC socket**: more machinery (listener, protocol, lifecycle) for no gain
  at single-machine scale.
- Bonus: this file is **the same shape a future event-bus would carry** (the
  in-flight event-bus exploration). pane becomes its first consumer; later the
  bus can supersede the file behind the same "declare active agent" intent.

Boundary kept clean: **TUI is the only writer; pane is the only one that touches
pane-tmux.**

---

## 3. Tab model & key routing

- pane-tmux uses **no-prefix tab switching** — bind `M-1` / `M-2` (Alt+number,
  or F-keys) at the pane-tmux level to `select-window`. Because pane-tmux is the
  *outer* server, it intercepts these before they reach the inner agent client;
  every other keystroke flows through to the agent. Zero collision with the
  agent's `C-b` / `C-q`.
- **Double status bar**: set the inner agent sessions' `status off` (or render
  pane-tmux's status as the only visible strip). Needs a one-line check of the
  bundled `tmux.conf` to confirm current status settings (open question §7).
- Result: Tim hits `M-2` to land on the live agent and just types; `M-1` to flip
  to the task list. No prefix dance.

---

## 4. The `features.pane` flag

- New config section:
  ```toml
  [features]
  pane = true
  ```
  Backed by `Config.Features map[string]bool` + a helper `cfg.FeatureEnabled("pane")`.
  This is a **lightweight convention, not a plugin system**: future optional
  features just add a key. No loader/registry.
- **Where enter branches** — `tuiModel.activate()` (cmd/tui.go):
  - `pane` **off** (default): exactly today — focus/open the iTerm tab, or
    quit-and-attach when no tab backend.
  - `pane` **on**: write `pane.json` `active=<agent>`, **stay in the TUI** (no
    quit, no new iTerm tab). pane picks it up and swaps its session tab.
  - The PR-cell enter (open PR in browser) is unchanged either way.
- **`claudes open <name>` when pane is on**: route through pane — write
  `pane.json` instead of exec-attaching — **with a fallback**: if pane isn't
  running, fall back to today's direct `tmux attach`. So `open` still "shows you
  the agent," just via pane when pane owns the right pane. When pane is off,
  `open` is byte-for-byte unchanged.

---

## 5. Defaults & lifecycle

- **Startup default**: `active = leo`. pane launches its session tab attached to
  `claudes-leo`. If leo isn't running, the session tab shows a placeholder
  ("leo not running — start it on the left").
- **Active agent dies**: the inner `tmux attach` exits; pane's session tab shows
  an "agent <X> exited" placeholder (a thin wrapper process in the respawned pane
  prints status and waits, rather than collapsing the window). pane itself stays
  alive — the task-list tab anchors it.
- **Nothing selected / no agents**: session tab shows "no agent selected — pick
  one on the left." Task-list tab always present.
- pane is **long-lived**; it is never torn down by agent churn.

---

## 6. Interactions with existing features

- **Scroll, shift-gated keybindings, pin/reorder** (recent LEFT-TUI work): all
  unaffected — they're left-pane concerns. The only change is enter's *effect*
  (set-active-in-pane instead of open-iTerm-tab). `X`/`P`/`N`/reorder still work.
- **Retired macuake/cmux side-tab + the parked "TODO dashboard auto-open pending
  an iTerm2 tab story"**: pane is plausibly **that tab story**. Tab 1 (the
  task/review list) is the natural home for the long-parked TODO dashboard, and
  pane gives it a persistent, always-visible right-pane container that the
  one-off iTerm-tab approach never cleanly had. Recommend explicitly treating
  pane as the resolution to that parked item (scoped later).
- **iTerm2 tabs backend**: when pane is on, enter no longer spawns iTerm tabs, so
  the tab registry goes mostly dormant for navigation. Leave the tabs backend
  intact and orthogonal — pane on/off and tabs on/off are independent knobs; pane
  on simply stops *using* the tab-open path from enter.

---

## 7. Open questions (could not resolve from code alone)

1. **Inner status bar**: does the bundled `tmux.conf` leave a status bar on for
   agent sessions? Must confirm so the nested client's status is suppressed and
   only pane's tab strip shows. (One-line check + maybe a `set -g status off`
   on the inner attach.)
2. **How pane is launched in the right iTerm pane**: manual `claudes pane` for
   v1, or config-driven autostart? Lean manual for v1.
3. **Focus-follows-enter**: after enter on the left, should focus jump to the
   right pane, or does Tim keep driving from the left and only type right when he
   wants to? Since pane is always visible in the split, likely *no* auto-focus
   needed; optional iTerm AppleScript focus if desired.
4. **Watch mechanism**: poll (recommended, dependency-free) vs fsnotify. Poll at
   ~200ms is imperceptible and simplest; revisit only if it feels laggy.
5. **Nested SIGWINCH/redraw edge cases** through `respawn-pane` — worth a short
   spike before committing, though tmux handles this well in practice.

---

## 8. Explicitly OUT OF SCOPE for v1

- Multi-machine / SSH-attached agents.
- Multiple simultaneous sessions / split / multi-view (only ever **one** agent
  shown at a time).
- The task-list tab's actual contents (stub/placeholder only).
