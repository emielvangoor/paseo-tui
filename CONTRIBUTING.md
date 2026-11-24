# Contributing to paseo-tui

Thanks for helping! Bug reports, ideas and pull requests are all welcome.

## Getting set up

You need **Go** (see `go.mod` for the version), **Node.js 22+** and **git**.

```sh
git clone https://github.com/emielvangoor/paseo-tui.git
cd paseo-tui
make            # first run fetches Paseo's source into .paseo-src and builds the bridge (a few minutes)
./paseo-tui     # talks to the local Paseo daemon
```

If you already have a built Paseo checkout, point the bridge build at it and skip the fetch:

```sh
PASEO_SRC=~/src/paseo make
```

`make help` lists every target.

## How the code is laid out

paseo-tui is a [Bubble Tea](https://github.com/charmbracelet/bubbletea) app. It talks to Paseo only
through `bridge/bridge.ts`, a small Node.js process built on Paseo's own client library, exchanging
newline-delimited JSON over stdio.

| File | What lives there |
|---|---|
| `main.go` | flags, finding a JavaScript runtime and the bridge, starting the program |
| `app.go` | the model: state, update loop, keys, main pane, footer |
| `bridge.go`, `bridge/bridge.ts` | the Go ⇄ Node protocol and the daemon calls |
| `sidebar.go`, `workspace.go` | the project → workspace tree and status buckets |
| `timeline.go` | turning timeline items into the rendered transcript |
| `form.go`, `project.go` | New workspace / new agent, and Add project |
| `palette.go` | the command center (`p`) |
| `subagents.go`, `settings.go`, `details.go`, `actions.go` | subagents, model settings, details panel, archive menu |
| `selection.go`, `images.go`, `history.go`, `resize.go` | text selection, image paste, prompt history, resizable panels |
| `runtime.go` | the embedded bridge and the Node / Paseo runtime lookup |

When a feature exists in the desktop app, follow its behaviour and wording; the desktop source in
[getpaseo/paseo](https://github.com/getpaseo/paseo) (`packages/app`) is the reference.

## Before you open a pull request

```sh
make fmt      # gofmt
make lint     # fmt-check + go vet
make test     # unit tests
```

- Keep pull requests focused: one change, with a clear description of the why.
- Add or update tests for logic you touch (`logic_test.go` shows the style).
- Update `CHANGELOG.md` under **Unreleased**.
- For UI changes, include a screenshot or a short recording in the PR.

## Releasing (maintainers)

1. Move the **Unreleased** entries in `CHANGELOG.md` under a new version heading.
2. Tag and push: `git tag v0.2.0 && git push origin v0.2.0`.
3. The **Release** workflow builds every platform and publishes the GitHub release, with the notes
   taken from the changelog.

## Code of conduct

Be kind and assume good intent. Harassment or personal attacks aren't tolerated.
