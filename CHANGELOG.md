# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Security

- Updated `golang.org/x/text` (GO-2026-5970) and `github.com/yuin/goldmark` (GO-2026-5320).
- Release archives now come with signed build provenance attestations.

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

[Unreleased]: https://github.com/emielvangoor/paseo-tui/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/emielvangoor/paseo-tui/releases/tag/v0.1.0
