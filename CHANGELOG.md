# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Settings (`c`, also in the command center), in sections: what opens at start (the most recent agent,
  the command center or the New workspace form), the header and what it shows (branch, pull request, diff), the model label
  on the tab bar, workspace numbers, thinking and verbose tool output by default, scroll speed (wheel
  and `J` / `K`), the keyboard hints at the bottom, asking before quitting, the terminal title, the
  update check, and the editor (`e`) and file manager (`F`) commands. They are saved to
  `<config dir>/paseo-tui/config.toml` (TOML, every setting described in a comment) on every change.
  Editing that file applies it while paseo-tui runs; a broken file is reported and not applied.

## [0.8.0] - 2026-11-26

### Added

- `/clear` (or `/new`) and `/exit` (or `/quit`, `/q`) work as in the desktop app instead of being sent to
  the agent: `/exit` archives the agent, `/clear` archives it and opens a fresh agent in this workspace
  with the same provider, model, thinking level, mode and options. Both are listed under `/`.
- When an omp agent is stuck in its `ask` tool, the line above the message box says so: omp 19 asks with an
  HTML form, which Paseo's omp adapter drops ([getpaseo/paseo#10471](https://github.com/getpaseo/paseo/issues/10471)),
  so no question ever arrives. Sending a message ends the wait and reaches the agent as the answer.
- A pending question shows its options in the box above the message field (all of them when there are
  10 or fewer), not only after pressing `enter`.

### Changed

- The question dialog shows every question of a request in one scrollable form: options as radio buttons
  or checkboxes, and an "Other" field on the same list where you type your own answer directly (typing on
  any option starts it). `↑` / `↓` move, `tab` jumps to the next question, `enter` picks, `ctrl+s` or
  "Send answers" sends; sending with a question left open points at it.
- Oh My Pi's multiple choice is one checkbox list instead of one pick per round: paseo-tui sends omp's
  rounds itself (each pick, "Other" and its text, then "Done selecting"), and opens the form's next
  question on its own when omp asks it.

## [0.7.0] - 2026-11-25

### Added

- The focused agent's model and thinking level show at the right end of the tab bar
  (e.g. `Claude Opus 5.5 · high`). They take no extra space and are left out when the tabs need the room.

### Changed

- Scrolling is calmer. A wheel notch moves 2 lines, and a fast spin (or trackpad swipe) speeds up to at
  most 8 lines per step instead of 20; the scroll keys move 3 lines instead of 6.
- The keyboard help (`?`) is grouped into sections (Navigate, Tabs, Write, Agents, Files, Transcript,
  Layout, App), laid out in two columns when the window is wide enough, and scrolls when it still
  doesn't fit (`j` / `k`, `↑` / `↓`, `space` / `pgdn`, the mouse wheel). Other keys close it.
- Shortcuts follow the usual terminal-app conventions (lazygit, k9s, tig):
  - `j` / `k` move through the sidebar, like `↓` / `↑`; `J` / `K` scroll the transcript.
  - `r` renames the agent (was `R`); `ctrl+r` reloads / reconnects (was `r`).
  - `s` stops the running agent only after asking (`s s` or `s y`).
  - A pending permission is reviewed in a dialog: `enter` opens it with the full command, `y` allows,
    `n` denies, `esc` decides later. `x` still denies straight away; `y` alone no longer allows.
  - Deleting an agent is in the `a` menu, or the `delete` key; `D` is gone, so a slip of shift on `d`
    (details panel) can't start a delete.
  - `1`…`9` jump to tab N, as `alt+1…9` do (Option often doesn't send `alt` in macOS terminals).
  - `:` opens the command center too; `c` no longer opens a new agent (`n` does).

### Fixed

- Markdown tables wider than the transcript are laid out to fit its full width: long cells wrap inside their column.
  Before, every row was broken across lines, which scrambled the table.

## [0.6.0] - 2026-11-25

### Added

- `/` lists the agent's slash commands and skills above the message box, like the desktop's composer.
  A `/` at the start of a message lists both, a `/` later in it lists only skills. Type to filter,
  `↑` / `↓` to choose, `tab` or `enter` to insert, `esc` to close. Pressing `/` outside the message box
  starts one.
- Questions from agents (Claude's `AskUserQuestion` and other question requests) can be answered.
  The pending question shows above the message box; `enter` or `y` opens a form like the desktop's
  question card (pick an option, toggle several for multiple choice, or type your own answer), and `x`
  dismisses it. Before, `y` approved the question without an answer.
- `H` opens History, like the desktop's: every agent on the host, archived ones included, most recently
  active first, with search (title, workspace, branch, project). `enter` opens an agent; an archived one
  is restored first, together with its workspace (an archived worktree is recreated). Also in the
  command center.

### Changed

- `/` no longer opens the command center (it starts a slash command); `p` and `ctrl+p` still do. With
  no agent open, `/` still opens the command center.
- The subagents list (`u`) shows the newest subagent on top. Nested subagents stay under the one that
  started them, newest first too.
- Workspaces are numbered under their sidebar heading (project, status group or Pinned), in the sidebar
  and the header (`paseo-tui › 1. main`), so moving with `J` / `K` or `{` / `}` shows which way you're going.

## [0.5.0] - 2026-11-24

### Added

- `f` forks the focused agent, like the desktop's fork menu: a new tab (or, via *Where*, a new
  workspace) with the same provider, model, thinking, mode and options, whose first prompt carries
  the source agent's chat history. Also in the command center.
- `R` renames the focused agent, which is also its tab label. Also in the command center.
- `e` opens the focused workspace's directory in your editor (`$VISUAL`, `$EDITOR`, else nvim or vim)
  in a pane floating over the UI, so agents keep updating behind it. `ctrl+\` hides the pane and `e`
  brings it back; quitting the editor closes it. Quitting paseo-tui with the editor still open asks first.
- `F` browses the focused workspace's files in a floating pane, like `e`, with the first terminal file
  manager installed: ranger, yazi, lf or nnn. Without one it opens the directory in Finder on macOS,
  Explorer on Windows, or the file manager on Linux (`xdg-open`). Also in the command center.
  Each workspace has its own `e` and `F` panes, so switching workspaces opens that workspace's directory.

### Changed

- `j` / `k` scroll the transcript and `J` / `K` move through the sidebar (they were the other way round).
  `↑` / `↓` still move through the sidebar.
- `q` and `ctrl+c` ask "Quit paseo-tui?" before exiting. `y`, `enter` or the same key again confirms.

### Fixed

- The Homebrew update hint is now `brew update && brew upgrade paseo-tui`. Plain `brew upgrade`
  refreshes the tap only once a day, so it could report the old version as up to date.

## [0.4.0] - 2026-11-24

### Added

- The terminal title follows the selection: `workspace · agent tab — project`. It is cleared when
  paseo-tui exits.

### Changed

- In the command center, `ctrl+p` moves up (like `ctrl+n` moves down) instead of closing it. `esc` closes it.
- The AUR package isn't published yet, because it has no maintainer
  ([#8](https://github.com/emielvangoor/paseo-tui/issues/8)). The 0.3.0 notes listed it by mistake.
  Releases skip the AUR until it's set up.

## [0.3.0] - 2026-11-24

### Added

- **Package managers.** Install with Homebrew (`brew install emielvangoor/tap/paseo-tui`, macOS and Linux),
  the AUR (`paseo-tui-bin`), or Scoop on Windows. Every release also ships `.deb`, `.rpm` and `.apk`
  packages for x86_64 and ARM64.

### Changed

- `paseo-tui update` leaves package-managed installs alone and tells you the right command. The footer's
  update notice shows that command too, such as `brew upgrade paseo-tui`.

## [0.2.0] - 2026-11-24

### Added

- **Update notices and `paseo-tui update`.** A daily background check shows `↑ vX.Y.Z available` in the
  footer, and `paseo-tui update` installs the new release in place after verifying its signature and
  checksum. Turn the check off with `--no-update-check` or `PASEO_TUI_NO_UPDATE_CHECK=1`.

### Security

- Updated `golang.org/x/text` (GO-2026-5970) and `github.com/yuin/goldmark` (GO-2026-5320).
- Release archives now come with signed build provenance attestations, and `checksums.txt` is signed
  with the release key (`checksums.txt.sig`).

### Changed

- The release pipeline runs the full test suite on all three platforms before building, builds without
  write access, and publishes a release only once all its files are uploaded. Published releases are
  never changed.

Upgrading from 0.1.0: download this release once by hand. From here on, `paseo-tui update` does it for you.

## [0.1.0] - 2026-11-24

The first public release.

### Added

- **Workspace sidebar** that mirrors the desktop app: Pinned, then projects → workspaces, with the
  desktop's status icons (needs input, failed, working, ready to review, done), branch, PR and diff
  stats. `o` switches to grouping by status. Projects and workspaces are sorted by activity and can be
  folded; the layout is remembered per host.
- **Agent tabs** for each workspace, with `[` `]`, `tab` or `alt+1…9` to switch and mouse clicks.
- **Live transcript** streamed from the daemon: Markdown rendering, collapsible thinking, tool calls with
  verbose output (`v`), and a progress line with the running tool and the turn timer.
- **Command center** (`p`): fuzzy search across workspaces and the agents inside them, plus actions.
- **New workspace** (`N`) and **new agent** (`n`): project, project directory or new worktree, starting
  branch, provider, model and thinking, created in one call so the daemon names the workspace and branch
  after your prompt. Includes **add project** (existing folder, new folder, GitHub clone).
- **Subagents**: a track above the composer for Paseo child agents and provider subagents (Claude Code
  tasks, Oh My Pi runs). `u` lists them, and one can open as a read-only `↳` tab.
- **Agent settings** (`m`): switch model, thinking level and mode, and toggles such as Fast.
- **Details panel** (`d`): status, model, a context-window bar, tokens, cost, task list, subagents and
  workspace facts. Scrollable.
- **Archive menu** (`a`): archive an agent or a workspace, or remove a project, with confirmation and a
  warning about uncommitted changes.
- **Composer**: grows with your text, prompt history on `↑`/`↓`, image paste with `ctrl+v` or a dropped
  file path.
- **Permissions**: allow or deny pending tool permissions with `y` / `x`.
- **Mouse support**: click workspaces, project headers and tabs; drag the panel dividers to resize; select
  transcript text to copy it; the scroll wheel accelerates.
- **Self-contained binaries** for macOS, Linux and Windows (arm64 and amd64). The bridge is embedded,
  and Paseo's own runtime is used when Node.js isn't installed.

[Unreleased]: https://github.com/emielvangoor/paseo-tui/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/emielvangoor/paseo-tui/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/emielvangoor/paseo-tui/releases/tag/v0.1.0
