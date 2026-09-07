# Security

The **schooltools** project takes the security of its users — and of
the Calgary Board of Education / D2L / Brightspace accounts it logs
into — seriously. This document explains how to report a vulnerability
and the scope of what we consider in scope.

---

## Supported versions

Only the latest release receives security fixes. We do not backport
patches to older tags. A release is "latest" while it is the most
recent tag on the [`main`](../../tree/main) branch.

| Version | Supported          |
| ------- | ------------------ |
| latest  | ✅                 |
| older   | ❌ — please upgrade |

---

## Reporting a vulnerability

**Please do not file a public GitHub issue for security bugs.**

Email **[iamcoder18@gmail.com](mailto:iamcoder18@gmail.com)** with:

- A clear description of the issue and its impact.
- Reproduction steps (commands, sample URLs, etc.).
- The commit / tag / version affected.
- Your name / handle for the acknowledgement line, or "anonymous".

You should expect an acknowledgement within **3 business days**. We
aim to ship a fix within **30 days** for high-severity issues and
**90 days** for everything else. The disclosure timeline is negotiable
if you need more time to coordinate a downstream rollout.

If we agree a fix is appropriate, we will:

1. Prepare a private patch and a CVE request (if applicable).
2. Coordinate an embargo with you until the fix is shipped.
3. Publish the fix in a tagged release with release notes crediting you
   (unless you asked to remain anonymous).
4. Add an entry to this file's "Disclosed vulnerabilities" section
   below.

---

## Threat model

schooltools is a **personal**, **single-user**, **headless** CLI that
authenticates as **you** to a CBE / D2L / Brightspace tenant. The
project is not a multi-tenant service, has no server, and accepts no
inbound network traffic. The threats we care about are:

| # | Concern | What we do |
| - | --- | --- |
| 1 | **Credential leakage to disk.** | `.env` is read once, never written. The session is stored at `~/.config/schooltools/session.json` with mode `0o600`; the parent directory is `0o700`. The JSONL auth log lives in the same directory, also `0o600`. |
| 2 | **Token leakage to logs.** | Auth-event log records redacted fingerprints only. Bearer tokens, cookies, and passwords never appear in stdout, stderr, or any log file. PRs that violate this rule will not be merged. |
| 3 | **Network eavesdropping.** | All D2L / Brightspace traffic is HTTPS. The internal HTTP client refuses to fall back to plaintext endpoints. |
| 4 | **CSRF / 401 handling.** | 401 responses from the Brightspace API trigger a one-shot re-mint of the OAuth bearer using the saved LMS session cookies — no full SAML round-trip unless explicitly requested. |
| 5 | **Session fixation / replay.** | The session file is overwritten on every successful login. There is no code path that loads a session without re-checking expiry first. |
| 6 | **Dependency supply-chain.** | Dependabot keeps `go.mod` and GitHub Actions up to date weekly. Pinned GitHub Actions are committed by SHA-equivalent major-version tags (e.g. `@v4`). |

### Out of scope

The following are **outside** the threat model and should not be
reported as security issues:

- Defects in the CBE / D2L / Brightspace servers themselves.
- Issues that require physical access to your machine.
- Issues that require an attacker to already control your account.
- "Spam" or "abuse" reports against the D2L service — report those to
  CBE IT, not us.

---

## Safe handling of `.env` and sessions

A few operational notes for users:

- `.env` is in `.gitignore`. **Do not** commit it. **Do not** paste its
  contents into issues, screenshots, or logs.
- `~/.config/schooltools/` is the only directory the CLI writes user
  data to. Back it up encrypted; do not sync it to a public location.
- `schooltools logout` deletes the session file and the bearer token.
  Run it from a shared machine when you're done.
- `--no-auth-log` (or `SCHOOLTOOLS_NO_AUTH_LOG=1`) disables the JSONL
  event log if you want to minimise on-disk metadata.

---

## Disclosed vulnerabilities

No vulnerabilities have been disclosed yet. The first entry will be
added here at disclosure time, in this format:

```
### YYYY-MM-DD — <short title>
- Affected: <version range>
- Fix: <tag>
- Credit: <reporter>
- Summary: <one paragraph>
```

---

## Acknowledgements

We are grateful to anyone who reports a vulnerability responsibly.
Reporters are credited in the disclosure entry above unless they ask
to remain anonymous.
