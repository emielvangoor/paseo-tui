#!/usr/bin/env sh
# Pushes a manifest rendered by render.sh (dist/pkg/) to its package channel.
# Usage: DEPLOY_KEY="$(cat key)" packaging/publish.sh homebrew|scoop|aur vX.Y.Z
# DEPLOY_KEY is an SSH private key with write access to that channel's repository only.
set -eu

CH=${1:?usage: publish.sh homebrew|scoop|aur vX.Y.Z}
TAG=${2:?usage: publish.sh homebrew|scoop|aur vX.Y.Z}
case "$CH" in
  homebrew) URL=git@github.com:emielvangoor/homebrew-tap.git BRANCH=main
            FILES="homebrew/paseo-tui.rb:Formula/paseo-tui.rb" ;;
  scoop)    URL=git@github.com:emielvangoor/scoop-bucket.git BRANCH=main
            FILES="scoop/paseo-tui.json:bucket/paseo-tui.json" ;;
  aur)      URL=ssh://aur@aur.archlinux.org/paseo-tui-bin.git BRANCH=master
            FILES="aur/PKGBUILD:PKGBUILD aur/.SRCINFO:.SRCINFO" ;;
  *) echo "publish.sh: unknown channel $CH" >&2; exit 2 ;;
esac
if [ -z "${DEPLOY_KEY:-}" ]; then
  echo "::error::No deploy key for $CH; set its secret on the release environment" >&2
  exit 1
fi

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/paseo-tui-$CH.XXXXXX")
(umask 077 && printf '%s\n' "$DEPLOY_KEY" > "$WORK/key")

# Pinned host keys, checked against the fingerprints GitHub and the AUR publish.
cat > "$WORK/known_hosts" <<'EOF'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
aur.archlinux.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEuBKrPzbawxA/k2g6NcyV5jmqwJ2s+zpgZGZ7tpLIcN
EOF
export GIT_SSH_COMMAND="ssh -i $WORK/key -o IdentitiesOnly=yes -o UserKnownHostsFile=$WORK/known_hosts -o StrictHostKeyChecking=yes"

if ! git clone -q "$URL" "$WORK/repo" 2>"$WORK/err"; then
  cat "$WORK/err" >&2
  if [ "$CH" = aur ]; then
    echo "::error::The AUR rejected the key. Add the release workflow's public key (aur-deploy.pub) to the AUR account that maintains paseo-tui-bin (My Account → SSH Public Key), then re-run the failed jobs." >&2
  fi
  exit 1
fi

for f in $FILES; do
  src=${f%%:*} dst=${f#*:}
  mkdir -p "$WORK/repo/$(dirname "$dst")"
  cp "$ROOT/dist/pkg/$src" "$WORK/repo/$dst"
done

cd "$WORK/repo"
git add -A
if git diff --cached --quiet; then
  echo "$CH already has paseo-tui $TAG"
  exit 0
fi
git -c user.name="paseo-tui release" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
  commit -qm "paseo-tui ${TAG#v}"
git push -q origin "HEAD:$BRANCH"
echo "==> published paseo-tui $TAG to $CH"
