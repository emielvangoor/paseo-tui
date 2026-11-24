# Security policy

## Supported versions

Only the latest release gets security fixes.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through
[GitHub's vulnerability reporting](https://github.com/emielvangoor/paseo-tui/security/advisories/new)
with the affected version, steps to reproduce and the impact. You'll get a reply within a week.

Problems in Paseo itself (the daemon, its protocol or the agents it runs) belong to the
[Paseo project](https://github.com/getpaseo/paseo/security).

## Verifying a release

`paseo-tui update` verifies downloads for you: `checksums.txt` must carry a valid Ed25519 signature
(`checksums.txt.sig`) from the release key in [`keys/release.pub.pem`](keys/release.pub.pem), which is
built into the binary, and the archive must match its checksum. Only the release workflow can use the
private key: it's a secret of the `release` environment, which only `v*` tags can deploy to.

To check by hand:

```sh
base64 -d checksums.txt.sig > checksums.sig.bin
openssl pkeyutl -verify -rawin -pubin -inkey release.pub.pem -in checksums.txt -sigfile checksums.sig.bin
```

Every release archive also comes with a SHA-256 checksum and a signed
[build provenance attestation](https://docs.github.com/actions/security-guides/using-artifact-attestations)
that proves it was built by this repository's release workflow from the tagged commit:

```sh
sha256sum --check --ignore-missing checksums.txt
gh attestation verify paseo-tui_linux_amd64.tar.gz --repo emielvangoor/paseo-tui
```
