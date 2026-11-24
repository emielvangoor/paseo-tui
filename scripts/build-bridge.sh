#!/usr/bin/env sh
# Builds bridge/dist/paseo-bridge.mjs, the bundle that paseo-tui embeds.
#
# The bridge reuses Paseo's own client library and CLI code, so it is bundled
# from a Paseo source checkout:
#   PASEO_SRC=/path/to/paseo   use an existing, already-built checkout
#   (unset)                    fetch Paseo $PASEO_REF into .paseo-src and build it once
set -eu

PASEO_REF=${PASEO_REF:-v0.17.3}
PASEO_COMMIT=${PASEO_COMMIT:-}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

if [ -z "${PASEO_SRC:-}" ]; then
  PASEO_SRC="$ROOT/.paseo-src"
  STAMP="$PASEO_SRC/.paseo-tui-built"
  if [ ! -f "$STAMP" ] || [ "$(cat "$STAMP")" != "$PASEO_REF" ]; then
    if [ -d "$PASEO_SRC" ]; then
      echo "==> .paseo-src is stale or incomplete; remove it (make distclean) and run again" >&2
      exit 1
    fi
    echo "==> fetching Paseo $PASEO_REF into .paseo-src"
    git clone --quiet --depth 1 --branch "$PASEO_REF" https://github.com/getpaseo/paseo.git "$PASEO_SRC"
    got=$(git -C "$PASEO_SRC" rev-parse HEAD)
    if [ -n "$PASEO_COMMIT" ] && [ "$got" != "$PASEO_COMMIT" ]; then
      echo "==> Paseo $PASEO_REF is commit $got, expected $PASEO_COMMIT; refusing to build." >&2
      echo "    Check the new commit, update PASEO_COMMIT, then run make distclean." >&2
      exit 1
    fi
    echo "==> installing Paseo's dependencies (one-time, takes a few minutes)"
    (cd "$PASEO_SRC" && ELECTRON_SKIP_BINARY_DOWNLOAD=1 PUPPETEER_SKIP_DOWNLOAD=1 \
      npm ci --no-audit --no-fund --loglevel=error)
    echo "==> building Paseo's protocol, client, server and CLI packages"
    (cd "$PASEO_SRC" && npm run build:server >/dev/null)
    echo "$PASEO_REF" > "$STAMP"
  fi
fi
PASEO_SRC=$(CDPATH= cd -- "$PASEO_SRC" && pwd)

echo "==> bundling the bridge against $PASEO_SRC"
NODE_PATH="$PASEO_SRC/node_modules" "$PASEO_SRC/node_modules/.bin/esbuild" bridge/bridge.ts \
  --bundle --platform=node --format=esm --target=node20 \
  --alias:paseo-cli="$PASEO_SRC/packages/cli/src" \
  --banner:js="import { createRequire as __cr } from 'module'; const require = __cr(import.meta.url);" \
  --log-level=warning \
  --outfile=bridge/dist/paseo-bridge.mjs

# The CLI code reads its version from ../package.json relative to the bundle.
node -e 'const p=require(process.argv[1]); require("fs").writeFileSync("bridge/package.json", JSON.stringify({name:"paseo-tui-bridge", private:true, type:"module", version:p.version}, null, 2)+"\n")' \
  "$PASEO_SRC/packages/cli/package.json"
echo "==> bridge/dist/paseo-bridge.mjs ($(du -h bridge/dist/paseo-bridge.mjs | cut -f1))"
