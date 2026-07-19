# Security Policy

## Threat model

Choragos runs AI coding agents as regular child processes of your user, the
same way you would run `claude` or `aider` from a shell. It is not a sandbox
and does not claim to be one.

What Choragos provides:

- **Least privilege per role, environment only.** `env_allow` / `env_deny`
  filter the environment a role's process receives, so a reviewer never gets
  your `AWS_*` credentials in its env. This is process-env hygiene, not
  isolation.
- **Approval gates.** Human (`approve`) and machine (`judge`) gates hold a
  delegation before it reaches a role, and any judge ambiguity falls closed
  to a human gate.
- **Checkpoints.** Agent file changes are checkpointed with git plumbing and
  can be rolled back without touching history.
- **Gateway in the LLM path (optional).** With Sphragis enabled, each worker
  is launched with its LLM base URL pointed at the local gateway for PII
  redaction and a tamper-evident audit log, and delegation is refused while
  the gateway is down.

What Choragos explicitly does not prevent:

- An agent reading any file your user can read (`~/.ssh`, dotfiles,
  repository secrets, local config).
- An agent reaching the network directly (cloud instance metadata, arbitrary
  endpoints). `ANTHROPIC_BASE_URL` routes cooperating clients through the
  gateway; it is not egress enforcement.
- An agent executing any binary your user can execute.

OS-level isolation belongs to the OS. A role's `command` is arbitrary, so it
can be a wrapper such as `docker run`, `sandbox-exec`, or `bwrap`, and the
whole deck can run inside a container or VM. Choragos composes with those
layers instead of reimplementing them.

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
