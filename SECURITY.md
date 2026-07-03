# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities privately. Do not open a public issue for
a suspected vulnerability.

- Preferred: open a private [GitHub Security Advisory](https://github.com/sphragis-oss/choragos/security/advisories/new).
- Alternatively, email **nonicked@protonmail.com** with the details.

Please include:

- A description of the issue and its impact.
- Steps to reproduce, or a proof of concept.
- Affected version or commit.
- Any suggested remediation, if you have one.

You will receive an acknowledgement within 5 business days. We will keep you
informed as we investigate, agree a disclosure timeline, and ship a fix. We aim
to triage within 10 business days and to credit reporters who wish to be named.

## Verifying releases

Release archives are built by GitHub Actions and signed keyless with
[cosign](https://docs.sigstore.dev/); build provenance is attested with
GitHub artifact attestations.

```bash
# 1. verify the signed checksums file (cosign v2+)
cosign verify-blob \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github.com/sphragis-oss/choragos/\.github/workflows/release\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

# 2. verify the archive against the checksums
shasum -a 256 --check --ignore-missing checksums.txt

# 3. verify build provenance (GitHub CLI)
gh attestation verify choragos_darwin_arm64.tar.gz --repo sphragis-oss/choragos
```

SBOMs (Syft) for every archive are attached to each release.

## Supported versions

Choragos is pre-1.0. Until a stable release line exists, only the latest
released version receives security fixes.

| Version | Supported |
|---------|-----------|
| latest release | yes |
| older | no |

## Scope

Choragos is a self-hosted multi-agent orchestrator TUI that runs locally. The
most security-relevant areas are:

- PTY bounds and input handling (preventing arbitrary command execution escapes).
- IPC socket permissions and hijacking.
- Configuration loading and unauthenticated local execution.

Reports in these areas are especially valued.
