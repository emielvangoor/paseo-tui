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

1. Move the **Unreleased** entries in `CHANGELOG.md` under a new heading such as `## [0.2.0] - 2026-12-01`,
   commit, push and wait for CI to pass.
2. Tag and push: `git tag -a v0.2.0 -m v0.2.0 && git push origin v0.2.0`. A tag with a suffix such as
   `v0.2.0-rc.1` becomes a pre-release.
3. The **Release** workflow then:
   - runs the full CI suite against the tagged commit;
   - builds the bridge from scratch, with no cache and no write access, and builds archives for all
     six platforms;
   - attests their build provenance;
   - uploads everything to a draft release and publishes it only once the upload is complete.

   The notes come from the changelog section, and the run fails if that section is missing.

The publish step also signs `checksums.txt` with the `RELEASE_SIGNING_KEY` secret of the `release`
environment, and checks the signature against `keys/release.pub.pem` before anything is published.
`paseo-tui update` refuses releases without a valid signature. If the key ever has to change, ship a release
that embeds both the old and the new public key before signing with the new one.

### Package managers

For stable releases (no `-suffix`), the build job also creates `.deb`, `.rpm` and `.apk` packages with
nfpm (`packaging/nfpm.yaml`) and renders the Homebrew formula, Scoop manifest and AUR PKGBUILD
(`packaging/render.sh`). After the release is published, one job per channel pushes them with
`packaging/publish.sh`:

| Channel | Repository | Secret (on the `release` environment) |
|---|---|---|
| Homebrew | [emielvangoor/homebrew-tap](https://github.com/emielvangoor/homebrew-tap) | `HOMEBREW_TAP_DEPLOY_KEY`, a write deploy key of that repo |
| Scoop | [emielvangoor/scoop-bucket](https://github.com/emielvangoor/scoop-bucket) | `SCOOP_BUCKET_DEPLOY_KEY`, a write deploy key of that repo |
| AUR | [paseo-tui-bin](https://aur.archlinux.org/packages/paseo-tui-bin) | `AUR_SSH_KEY`, whose public key is on the maintainer's AUR account |

Then the verify jobs install from each channel on a clean macOS, Windows and Arch Linux machine. If a
channel fails, fix the cause and use **Re-run failed jobs**.

A published release is never changed. If something is wrong, tag a new patch version. If a run fails
before publishing, fix the cause and use **Re-run all jobs**: the leftover draft is replaced.

### Updating Paseo

The bridge is built from Paseo `PASEO_REF`, which must resolve to `PASEO_COMMIT` (both are in the
`Makefile`). To move to a new Paseo version, update both, run `make distclean && make && make test`, and
try the app against a daemon of that version.

## Code of conduct

Be kind and assume good intent. Harassment or personal attacks aren't tolerated.
