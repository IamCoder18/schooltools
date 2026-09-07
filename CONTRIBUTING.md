# Contributing to schooltools

Thanks for your interest in **schooltools**! This document explains how
to get the project running locally, the conventions we use, and the
process for opening a pull request.

> **TL;DR:** open an issue before sending a non-trivial PR, run `go test
> ./...` and `go vet ./...` locally, and keep PRs scoped to one concern.

---

## Code of Conduct

By participating, you agree to abide by the
[Code of Conduct](./CODE_OF_CONDUCT.md). Please read it before posting
in issues, pull requests, or discussions.

---

## Project layout

```
schooltools/
├── main.go                 # tiny entrypoint → cmd.Execute()
├── cmd/                    # one cobra command per file (or grouping)
│   ├── root.go
│   ├── course.go
│   ├── content.go
│   ├── download.go
│   ├── archive.go
│   ├── ...
│   └── per_course_rare.go  # commands behind --course that are rarely used
├── internal/               # importable only by this module
│   ├── httpclient/         # shared *http.Client + 401 auto-refresh
│   ├── session/            # session.json read/write
│   ├── login/              # SAML + KMSI login
│   ├── content/, archive/, authlog/, ...
│   └── ...
├── go.mod / go.sum
└── D2L_API_RESEARCH.md     # long-form notes on the CBE / D2L Valence surface
```

Conventions:

- Top-level commands are **singular nouns** (`course`, `content`,
  `archive`). Scope to one course with `--course <orgUnitId>`. There is
  no `me` parent.
- Anything reusable belongs in `internal/`. Nothing in this repo is
  intended to be imported by other modules; keep packages internal.
- One command (or one tightly related group) per `cmd/*.go` file. Long
  or rarely used per-course commands live in `per_course_rare.go` /
  `cross_rare.go` / `user_rare.go`.

---

## Local development

### Prerequisites

- **Go 1.26+** — `go version` should report `go1.26` or later.
- A CBE / D2L / Brightspace account, **or** the `--help` text is enough
  if you're not changing auth or HTTP code paths.

### Build & test

```sh
go build ./...
go test -race ./...
go vet ./...
```

We require `go vet ./...` to be clean. `go test -race` is what CI runs.

### Optional: install into your `$GOBIN`

```sh
go install .
```

### Live testing against CBE / D2L

Create a local `.env` (**never commit it** — it's already in
`.gitignore`):

```
CBE_EMAIL=you@example.com
CBE_PASSWORD=...
```

Then:

```sh
schooltools login
schooltools course list
schooltools content --course <orgUnitId> --tree
schooltools archive --diff <orgUnitId>
```

The session is written to `~/.config/schooltools/session.json` (mode
`0o600`); the JSONL auth event log goes to `~/.config/schooltools/auth.log`.

---

## Coding style

- **Go formatting:** `gofmt` / `goimports` defaults. CI runs
  `golangci-lint`; please run it locally before pushing.
- **No exported symbols without docs:** every exported name in
  `internal/` should have a doc comment starting with the name itself.
- **Errors:** wrap with `fmt.Errorf("doing X: %w", err)`; never use
  `panic` for recoverable error paths.
- **Concurrency:** the CLI is short-lived; favour simple goroutines with
  `sync.WaitGroup` over channels. Any new shared state must have a unit
  test.
- **Dependencies:** please discuss before adding a new module dependency.
  Charmbracelet + `spf13/cobra` + `PuerkitoBio/goquery` are the core
  stack; everything else is case-by-case.

---

## Opening an issue

Use the issue templates under
[`.github/ISSUE_TEMPLATE/`](./.github/ISSUE_TEMPLATE). For bug reports,
please include:

1. `schooltools --version` output.
2. The exact command you ran.
3. Observed vs. expected behaviour.
4. A redacted `auth.log` line if it's a session/auth issue.

For feature requests, please describe the user story, not just the
solution.

---

## Opening a pull request

1. **Open an issue first** for anything non-trivial (a bug fix with a
   known repro is fine without one).
2. **Branch off `main`.** Branch names like `fix/archive-topic-lookup`
   and `feat/extract-command` are encouraged.
3. **Keep PRs focused.** One concern per PR. Refactors go in their own
   PR; drive-by formatting changes go in their own PR.
4. **Tests:** add or update unit tests for any behaviour change. New
   packages need at least one `*_test.go` file.
5. **CI must be green.** The PR template links to the CI run; please
   fix any failure before requesting review.
6. **Fill out the PR template** (`.github/PULL_REQUEST_TEMPLATE.md`).
7. **Squash-merge** is the default; write a commit message that
   describes the user-visible change.

---

## Releasing

Releases are cut from `main` by tagging a commit:

```sh
git tag -a v0.3.0 -m "Release v0.3.0"
git push origin v0.3.0
```

The `.github/workflows/release.yml` workflow builds the binaries, signs
them with a SHA-256 manifest, and attaches everything to a GitHub
release with auto-generated notes.

The version in `cmd/root.go` (`Version: "..."`) must be updated in the
same commit as the tag. CHANGELOG.md is updated in the same PR.

---

## Reporting security issues

**Please do not file a public issue for security bugs.** See
[SECURITY.md](./SECURITY.md) for the disclosure process.

---

## License

By contributing, you agree that your contributions will be licensed
under the [MIT License](./LICENSE).
