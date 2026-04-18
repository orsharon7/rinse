# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| Latest (`master`) | ✅ |
| Older releases | ❌ |

Only the latest release on `master` receives security fixes. If you are on an older version, upgrade first.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report vulnerabilities privately via GitHub's built-in private vulnerability reporting:

1. Go to **[Security → Report a vulnerability](https://github.com/orsharon7/rinse/security/advisories/new)**
2. Describe the vulnerability, steps to reproduce, and potential impact
3. We will acknowledge receipt within **48 hours** and aim to ship a fix within **7 days** for critical issues

If you prefer email, reach out to the maintainer at the address on [Or Sharon's GitHub profile](https://github.com/orsharon7).

## What to include in your report

- RINSE version (`rinse --version`)
- Operating system and version
- Steps to reproduce the vulnerability
- Potential impact (data exposure, privilege escalation, arbitrary code execution, etc.)
- A proof-of-concept or minimal reproduction if possible

## Scope

RINSE runs locally on your machine and communicates with GitHub via `gh` CLI and GitHub Copilot via `opencode`/`claude`. The attack surface includes:

- **Local file access** — RINSE reads `.rinse.json`, `~/.rinse/`, and git worktrees
- **Shell invocation** — RINSE shells out to `gh`, `git`, `opencode`, and `claude`
- **GitHub API** — RINSE calls GitHub REST and GraphQL APIs via `gh` (your token, your permissions)
- **Webhook delivery** — `RINSE_WEBHOOK_URL` receives a JSON POST after each cycle; ensure the endpoint is trusted

Out of scope: vulnerabilities in `gh`, `opencode`, `claude`, or GitHub itself.

## Disclosure policy

We follow coordinated disclosure. Once a fix is shipped:

1. We will publish a GitHub Security Advisory
2. We will credit the reporter (unless you prefer anonymity)
3. We will add the fix to the `[Unreleased]` section of `CHANGELOG.md`

Thank you for helping keep RINSE secure.
