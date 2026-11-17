# Changelog

All notable changes to **schooltools** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- PowerSchool MyCBE support via the new `sis` command tree. The
  Calgary Board of Education's PowerSchool SIS parent/student portal
  (https://sis.cbe.ab.ca) is reverse-engineerable through the Angular
  SPA's internal REST endpoints at `/ws/xte/…`. New subcommands:
  - `sis login` — authenticate against MyCBE (plaintext passwords;
    CBE customised doPCASLogin to skip HMAC-MD5)
  - `sis logout` — clear the saved SIS session
  - `sis whoami` — verify the saved session and print the student
    profile
  - `sis years` — list the year picker (5 historical years)
  - `sis grades` — list enrolled classes for the active school year
  - `sis standards` — list standards (rubric items) per section
  - `sis assignments` — list posted assignments (often empty early
    in a term). Walks every (student, section, standard) row via
    `/ws/xte/standard_grade_section` and drills into the legacy
    `/guardian/studentSectionStemAssignments.json` per-cell endpoint
    to surface each teacher's posted assignment — including due
    date, category, score token, and any flags (`isCollected`,
    `isLate`, `isMissing`, `isExempt`, `isAbsent`, `isIncomplete`).
    The per-cell fan-out runs with bounded concurrency, is capped by
    `--max-cells` (default 200) so a mistyped `--section` cannot
    trigger unbounded live requests, and reports per-cell failures as
    a summary instead of aborting the run.
    Note: the Neon SPA does not use `/ws/xte/assignment/lookup` —
    that endpoint returns `[]` even when assignments exist; we
    only get real data through the per-cell legacy surface.
  - `sis school` — print school-level config (max comment size, etc.)
  - `sis standard-grades` — (student, section, standard) rows with
    the proficiency code and calculated percentage
  - `sis scale` — the CBE High School Proficiency Scale with nominal
    percentages
  - `sis grades --marks` — current T1/S1 course marks from the
    guardian "Assignments & Grades" page
  - `--year <id>` on `sis whoami`, `sis school`, `sis grades`,
    `sis standards`, `sis assignments`, and `sis standard-grades`.
    Defaults to the newest year from the portal's year picker; every
    subcommand now resolves the year through one shared helper so they
    cannot disagree about which year they are reporting.
- New env vars: `SIS_STUDENT_NUMBER` + `SIS_PASSWORD` (preferred;
  fall back to `CBE_STUDENT_NUMBER` + `CBE_PASSWORD` and finally
  `CBE_EMAIL` + `CBE_PASSWORD`, with a stderr warning when the
  fallback resolves to an email address, which MyCBE will reject).
  The session file is `~/.config/schooltools/sis-session.json` and is
  independent of the D2L session.json.
- `SIS_DCID` env var (optional) — when set, skips the
  credentials-link scrape that `sis whoami` uses to discover the
  student's internal DCID. The `sis` command tree now loads `--env-file`
  once up front, so `SIS_DCID` is honoured when set in `.env` and not
  only when exported in the shell.
- `sis --auto-refresh` (default `true`) — re-authenticate when the saved
  session is missing. Set `--auto-refresh=false` to make `sis logout`
  sticky. Independently of the flag, a `sis` command that receives a
  401/403 from the portal re-authenticates once and retries, because
  PowerSchool issues `psaid`/`JSESSIONID` without an `Expires` and a
  server-side revocation is invisible to the local expiry check.
- `internal/errors.AuthError` now satisfies `interface{ AuthCode() string }`
  so PowerSchool login failure codes (`INVALID_CREDS`, `NO_PSAID`,
  `LOGIN_FOLLOW_FAILED`, …) reach the auth log instead of being
  collapsed into a single generic label.
- Login refuses to follow a redirect that leaves the portal's origin or
  downgrades the scheme, and never interpolates a `Location` query string
  into an error message, so a server-supplied URL cannot land in
  `auth.log`.
- New `internal/ps` package with the API client, parsers, and login
  flow. Mirrors the structure of the existing `internal/session` /
  `internal/login` packages.
- Initial public release on GitHub.

### Changed
- Every `sis --json` payload is now one consistent envelope: a `count`
  field plus snake_case keys. `sis grades` flattens the nested
  `_coursesectioninfo` object, `sis standard-grades` uses
  `section_ids` instead of a mixed `sections` list, and `sis
  assignments`, `sis standards`, `sis years` and `sis school` follow
  the same shape.
- The `.env` file is optional for read-only `sis` subcommands. A
  missing file is tolerated when the saved SIS session is usable; only
  `sis login` and the 401/403 re-auth path require credentials. This
  also makes `sis logout` work without an `.env`.

### Added
- `sis standard-grades` surfaces `standardgrade` and
  `standardpercent` from PowerSchool; the table now shows the
  proficiency code and the calculated percentage.
- `sis assignments` resolves score tokens to their label and nominal
  percentage (`score_label`, `score_percent`), and unescapes HTML
  entities in names and categories.
- `internal/ps.ParseCourseMarks` and `Client.GetCourseMarks` parse the
  guardian grades page.
- `internal/ps.Scale` and `LookupProficiency` expose the CBE
  proficiency scale.

### Fixed
- A guardian dashboard that does not contain `credentials.html` is now
  reported as an expired session with a `schooltools sis login` hint,
  instead of an opaque "could not parse DCID" message. The error
  carries a stable `PS_SESSION_STALE` code.
- `assignment submissions --json` emits a `{"count", "submissions"}`
  envelope and drops the all-zero placeholder row some D2L tenants
  return instead of an empty array.
- `grade --json` emits a `{"count", "grades"}` envelope instead of a
  bare top-level array.
- `course list` columns fit a narrow terminal without wrapping.
- HTML entities in assignment names and categories are unescaped once,
  in both the table and JSON output.

## [0.2.0] - 2026-09-05

### Added
- Full D2L Valence feature-parity redesign. See
  `D2L_API_RESEARCH.md` and the original design notes in
  `.kilo/plans/cli-feature-parity-redesign.md`.
- New top-level commands: `course`, `content`, `download`, `archive`,
  `assignment` / `dropbox`, `quiz`.
- `archive list`, `archive path`, `archive read`, `archive topic` for
  inspecting the on-disk content-addressed archive store.
- `prune` to reclaim old versions after `archive` runs.
- `systemd` subcommand with a `schooltools-archive.timer` template for
  nightly archival.
- `auth log` and `--no-auth-log` opt-out for the JSONL auth event log.

### Changed
- **Breaking:** CLI flag convention switched to singular nouns with
  scope-via-flag (`--course <id>`, `--org CSV`). See `README.md` for the
  full old → new table.
- HTTP client moved to a single `internal/httpclient` package with a
  Bearer-token auto-refresh path on 401.
- Session storage moved to `~/.config/schooltools/session.json` with the
  JSON cookie schema documented in the README.

### Deprecated
- Legacy positional forms of `content` and `download` still work but emit
  a one-line deprecation notice. They are hidden from `--help` and will
  be removed in a future release.

## [0.1.0] - 2026-08-15

### Added
- Go port of the previous TypeScript CLI: cobra, goquery, and
  `net/http/cookiejar` replaced commander, cheerio, and tough-cookie.

## [0.0.x] - 2026-07 and earlier

### Added
- Original TypeScript CLI prototype.

[Unreleased]: https://github.com/aarav/schooltools/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/aarav/schooltools/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aarav/schooltools/releases/tag/v0.1.0
