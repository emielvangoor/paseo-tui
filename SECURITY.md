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

Every release archive comes with a SHA-256 checksum and a signed
[build provenance attestation](https://docs.github.com/actions/security-guides/using-artifact-attestations)
that proves it was built by this repository's release workflow from the tagged commit:

```sh
sha256sum --check --ignore-missing checksums.txt
gh attestation verify paseo-tui_linux_amd64.tar.gz --repo emielvangoor/paseo-tui
```
