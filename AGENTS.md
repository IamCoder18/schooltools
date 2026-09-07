# AGENTS.md — guide for AI coding agents

This document is read by AI coding agents (Kilo, Claude Code, Codex,
etc.) working in this repository. It captures the project-specific
guardrails that a generic Go skill would not know.

---

## Project summary

- **What it is:** A headless CLI for the Calgary Board of Education's
  D2L / Brightspace LMS. One user, one tenant, no server.
- **Language:** Go (1.26+). Module path
  `github.com/aarav/schooltools`.
- **Frameworks:** cobra for commands, bubbletea/lipgloss for the
  spinner / tables, goquery for the legacy HTML scraper, plain
  `net/http` for the D2L Valence JSON API.
- **Layout:** `main.go` → `cmd.Execute()`. One cobra command per file
  in `cmd/`. Shared logic in `internal/`.

Read [`README.md`](./README.md) before making changes. Read
[`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md) before opening a PR — most
"missing" features are already filed there.

---

## Hard rules

1. **Never read, write, log, or print `.env`, `CBE_PASSWORD`, or the
   `session.json` cookie file.** The CLI already reads `.env` once in
   `internal/env`; agents must not re-introduce that path anywhere
   else. The Bearer token and the SAML assertion also never go to
   stdout/stderr/logs. See `SECURITY.md` §"Threat model" for the
   underlying rule.
2. **Never touch `vendor/`, `~/.config/schooltools/`, or anything under
   `dist/`.** Those are user data and build output respectively.
3. **Don't add a new top-level dependency without flagging it.** The
   core stack is `cobra`, `bubbletea`, `lipgloss`, `goquery`,
   `testify`. Anything else is case-by-case.
4. **One concern per PR.** Refactors go in their own PR; drive-by
   formatting goes in its own PR; feature work does not include
   reformatting unrelated files.
5. **`go test ./...` and `go vet ./...` must be clean** before a PR
   is considered done. The CI matrix runs them on Linux and macOS with
   Go 1.26 and `-race`.
6. **No comments unless the user asked for them.** This is a project
   policy, not a personal preference — see `CONTRIBUTING.md` §"Coding
   style".
7. **Do not amend history / force-push / skip hooks** unless the user
   explicitly asked. See `AGENTS.md` rules in `.kilo/`.

---

## Where to make changes

| Want to change … | Look here |
| --- | --- |
| A new top-level command | `cmd/` (one file per command) |
| A per-course, rarely used command | `cmd/per_course_rare.go` |
| A cross-course, rarely used command | `cmd/cross_rare.go` |
| A user-scoped, rarely used command | `cmd/user_rare.go` |
| Auth, cookies, login flow | `internal/login/`, `internal/session/`, `internal/cookies/`, `internal/httpclient/auth.go` |
| HTTP transport / retries | `internal/httpclient/`, `internal/retry/` |
| The on-disk archive store | `internal/archive/` |
| The JSONL auth log | `internal/authlog/` |
| Terminal UI helpers | `internal/ui/`, `internal/table/`, `internal/tree/` |
| systemd units | `internal/systemd/` and the `.tmpl` files under `internal/systemd/units/` |
| The CLI version string | `cmd/root.go` (`rootCmd.Version`) |
| The CHANGELOG | `CHANGELOG.md` (Keep a Changelog format) |

---

## Common gotchas

- **Singular nouns at the top level.** New commands are `course`, not
  `courses`. Scope to one course with `--course <orgUnitId>`. There is
  no `me` parent.
- **The session file is the single source of truth for cookies.** Don't
  re-implement cookie storage inside a command. Call
  `internal/httpclient.New(...)`.
- **401s auto-refresh the OAuth bearer once** via the saved LMS
  session cookies. If you add a new D2L call site, route it through
  the shared client in `internal/httpclient` so the refresh kicks in.
- **JSON output shapes are inconsistent right now** (see KNOWN_ISSUES
  #9, #10, #11). If you add a new `--json` output, prefer an
  **envelope object** with a `count` field; do not emit a bare
  top-level array.
- **`archive` is append-only at the blob layer.** New archive code
  must not delete or overwrite existing blobs. Pruning is the
  separate `prune` command.

---

## Testing

- `internal/<pkg>/` packages should have a `*_test.go` next to them.
- Use `testify/require` for assertion-heavy tests, plain `t.Errorf` for
  trivial ones. The codebase already imports `testify` — don't add a
  second assertion library.
- HTTP-touching tests should use `internal/httpclient`'s in-memory
  transport if one exists; otherwise `httptest.NewServer`.
- Don't write tests that hit the real CBE / D2L tenant.

---

## Before you finish

- [ ] `go build ./...` is clean.
- [ ] `go test -race ./...` is clean.
- [ ] `go vet ./...` is clean.
- [ ] `golangci-lint run --timeout=5m` is clean (if installed).
- [ ] New code has tests next to it.
- [ ] No new top-level dependencies without a note in the PR.
- [ ] CHANGELOG.md updated under `[Unreleased]`.
- [ ] No comments added unless asked.
