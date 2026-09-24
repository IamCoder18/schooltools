# Changelog

All notable changes to **schooltools** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `archive` (bare) prints a one-shot status: schema version, last update
  freshness warning, courses/topics/file-topics counts, `N of M file
  topics have bodies`, on-disk blob bytes, lock state, and 2–3 `try:`
  hints. Empty state prints *"No archive yet — run `schooltools archive
  update`."* This replaces the previous behaviour where the bare command
  silently started a mutating pass.
- `archive update [courseId...]` is the new name for the fetch pass
  (`schooltools archive 12345`). Scope can be set positionally or via
  repeated `--course`. `update` converges: it fills in any File topic
  whose body is missing on disk even when its metadata is unchanged
  (was a permanent hole for 8 of 284 file topics in the live store).
- `archive update --dry-run` is the new preview — writes nothing (no
  index, no directory, no metadata fetch), shows the same plan table
  as the old `--diff`. `--no-bodies` opts out of body downloads
  (metadata only).
- `archive find [query...]` walks the archive index once (single load)
  and matches topics whose title, URL, or type contains all
  whitespace-separated tokens (AND, case-insensitive). Filters:
  `--ext <pdf|docx|…>`, `--type File|Link`, `--with-bodies`,
  `--missing-bodies`. Replaces `archive files` and provides a
  `--plain` TSV stream for `fzf | xargs archive cat`.
- `archive show <ref>` prints one topic record + current blob pointers
  + version history. `<ref>` is auto-detected from a numeric topicId,
  a 32-hex metadata UUID, or a 64-hex body SHA-256.
- `archive cat <ref>` writes a topic's bytes (File topics) or pretty
  JSON (everything else) to stdout — merges `archive read` and
  `archive body`.
- `archive path <ref>` (unchanged behaviour) with the new ref grammar.
- `archive export [ref...] --out DIR [--flat] [--dry-run]` copies file
  bodies out as `<CourseCode>/<Sanitized Title>.<ext>`, with
  collision-safe ` (2)` suffixes. Pass refs to copy just one.
- `archive prune [--delete] [--wait]` is now under the `archive` group.
  Plan-only by default — `--delete` applies. After pruning, every
  per-course index record points at a blob that exists on disk, so
  `archive show` can never print a dead uuid (fixes the dangling-pointer
  bug in the old top-level `prune`).
- `archive verify [--deep]` checks pointer resolution, dangling
  version records, missing body blobs, per-course index parse, missing
  course directories, schema-version compatibility, and orphan blobs.
  `--deep` re-hashes body contents. Exits 0 / 1; JSON envelope.
- Persistent flags on the `archive` group: `--dir`, `--json`,
  `--plain`, `--quiet`/`-q`, `--no-spinner`, `-v`/`--verbose`, and
  `--course` (repeatable). The short `-e` flag for `--env-file` is
  removed.
- JSON envelopes everywhere: collections are `{count, items, ...}`,
  records are objects, summaries are flat objects. `--plain` TSV is
  available on `list`, `find`, `show`, `export --dry-run` for
  pipelines (`fzf`, `cut`, `xargs`).
- `internal/archive.Store` loads the global index plus every per-course
  index exactly once and serves O(1) topic / metadata-uuid / body-sha
  lookups. `archive find`, `archive show`, `archive cat`, `archive path`,
  `archive status`, `archive verify`, and `archive export` all use it,
  so repeated queries never re-read the per-course indexes.
- `internal/archive.Verify`, `internal/archive.Export`,
  `internal/archive.PrunePlan`, `internal/archive.Prune`,
  `internal/archive.OpenStore`, `internal/archive.LoadStatus` are the
  new public API in `internal/archive/`.
- `internal/archive.WaitLock` blocks on the archive lock for `--wait`.
- `news list` subcommand (alias for the root `news` command) for
  discoverability, and `news --until <iso>` to complement `--since`.
- `news --body-format text|html|both` to flatten the `{Html,Text}` body
  shape into a single string for `--json` consumers.
- `archive files [--ext <ext>] [--course <id>] [--with-bodies] [--json]`
  to enumerate File-type topics across all archived courses
  (KNOWN_ISSUES #18).
- `archive path --kind metadata|body` to force a specific blob kind when
  auto-detection is ambiguous (KNOWN_ISSUES #20).
- `archive read <topicId>` now defaults to the archived file body for
  File topics, falling back to the metadata blob when no body is archived
  or `--meta` is passed (KNOWN_ISSUES #4).
- `archive topic <id>` accepts the topicId alone and performs a global
  reverse lookup; `--course` is now an optional hint, not a hard
  requirement (KNOWN_ISSUES #6).
- `archive path <topicId>` resolves to the body path for File topics with
  an archived body, otherwise the metadata path (KNOWN_ISSUES #5).
- `token --refresh` mints a fresh Brightspace OAuth token using the saved
  session cookies — no full SAML re-login required (KNOWN_ISSUES #14).
- `content --timeout <duration>` aborts the live TOC fetch after a
  deadline (default 60s) and prints a hint pointing at `--archive` when
  the limit is hit (KNOWN_ISSUES #15).
- `internal/archive.FindTopicID` exposes the global `topicId → courseId`
  reverse lookup as a reusable helper.
- `internal/archive.FileTopics` enumerates File-type topics across the
  archive with optional extension / body filters.

### Changed
- `archive list` table now reflects real per-course topic counts and
  TOC-fetched timestamps after a real update pass. The `--diff` /
  `update --dry-run` preview no longer writes the index — TOC timestamps
  land only after a real update pass (KNOWN_ISSUES #1).
- `archive` body-tracking math no longer conflates "absent" with
  "unchanged" — first-ever File topics now count as `BodiesFetched`,
  not `BodiesReused` (KNOWN_ISSUES #3).
- `archive update` body downloads now converge: any File topic whose
  body is missing on disk (or whose `CurrentBody` is empty) is
  re-fetched even when the metadata is unchanged (previously a
  permanent hole).
- `prune` (moved under `archive prune`) is plan-only by default and
  requires `--delete` to apply. After pruning, the per-course indexes
  no longer carry version records that point at deleted blobs.
- SchemaVersion bumped 3 → 4 because prune now trims version/bodyVersion
  records in the same pass.
- `archive` body summary line is suppressed when no bodies were
  attempted this pass, so cron output no longer says
  "0 new, 0 unchanged" before any download has happened
  (KNOWN_ISSUES #17).
- `news --json` now wraps items in an envelope `{news, count, since,
  until, courseCount, courses, perCourse}`; `Body` is flattened to a
  string field per `--body-format` (KNOWN_ISSUES #9, #10).
- `news get <id>` (and `news attachment`) auto-discover the owning
  course across all enrolled courses when `--course` is omitted
  (KNOWN_ISSUES #7).
- `content --json` wraps items in `{courseId, count, items, source}`
  for both the live and archive-backed paths (KNOWN_ISSUES #11).
- `content --tree` renders File-type topics as `[File.pdf]` /
  `[File.docx]` etc. using the URL extension (KNOWN_ISSUES #12).
- `doctor` reports SAML and Brightspace token expiry alongside
  `hasValidSession()`, and emits actionable warnings ("re-login
  required" or "try `schooltools token --refresh`") when they are out
  of date — no more false-positive valid session when the bearer has
  expired (KNOWN_ISSUES #13).
- `internal/tree.Options` gained `TopicLabelHook` so callers can attach
  extra context (file extensions, etc.) to the type bracket without
  coupling the renderer to URL parsing.
- `internal/login.MintTokenFromSavedSession` is the new public entry
  point for token refresh, exposed through the `token --refresh`
  command.
- The systemd timer unit now executes `archive update --no-auto-refresh`
  (previously the mutating bare `archive`); the "no silent reauth"
  guarantee from the old unit template is preserved via `--no-auto-refresh`.

### Fixed
- **P0:** `archive prune` (was top-level `prune --dry-run`) was
  documented as a no-op preview but actually deleted blobs. `PrunePlan`
  is now the read-only planner; `Prune` requires `--delete` and is the
  only thing that removes files. KNOWN_ISSUES #4 (the destructive
  `--dry-run`) was filed in `KNOWN_ISSUES.md` under the original
  issue number for traceability.
- **P0:** `prune` previously deleted every `.bin` body blob, including
  the live `CurrentBody` of every File topic — the `live` set only
  contained metadata UUIDs and the body-blob extension was never
  matched. `Prune` now keeps every blob (metadata and body) that is
  referenced by any current pointer.
- **P1:** `prune` left `Versions` / `BodyVersions` records pointing at
  deleted blobs (dangling pointers). `Prune` now rewrites per-course
  indexes in the same pass, so `archive show` can never print a dead
  uuid.
- **P1:** Bodies that failed to download (or were skipped because the
  metadata was unchanged) stayed missing forever. `archive update`
  now retries every File topic whose body is absent on disk, even when
  the metadata hasn't moved.
- Errors now print exactly once: cobra's default `Error:` echo is
  silenced (`SilenceErrors = true`) and `Execute()` prints the message
  once to stderr. JSON-mode commands emit `{"error": …}` to stdout
  instead.

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
