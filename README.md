# schooltools

CLI tools for CBE / D2L / Brightspace. Written in Go on top of the
Charmbracelet stack (cobra, bubbletea, lipgloss). Headless: no browser, no
interactive MFA prompt.

## What this CLI is for

schooltools is a headless command-line client for the Calgary Board of
Education's D2L / Brightspace LMS. **Six surfaces** cover the day-to-day
student workflow:

  - `course`         discover enrolled courses
  - `content`        browse a course's modules and topics
  - `download`       pull a topic's file or metadata
  - `archive`        snapshot every course to disk on a schedule
  - `assignment`     dropbox folders, submissions, feedback
  - `quiz`           quiz list and attempts

Everything else (`news`, `grade`, `discussion`, `calendar`, `due`,
`overdue`, `update`, `feed`, `award`) is
**implemented but rarely useful from a CLI** — see
[Advanced / rarely used](#advanced--rarely-used) below. If you're not
sure which command you need, start with the six above.

## Quickstart

```sh
go install .
```

1. Create a `.env` in your working directory:

   ```
   CBE_EMAIL=you@example.com
   CBE_PASSWORD=...
   ```
2. `schooltools login` (KMSI defaults on).
3. Pick a course to start with:

   ```sh
   schooltools course list
   schooltools content --course 1527886           # browse the TOC
   schooltools content --course 1527886 get 19278283  # one topic's metadata
   schooltools download --course 1527886 19278283    # download that topic's file
   schooltools archive update                       # snapshot every course
   schooltools archive find "safety contract"      # search your archive
   ```

## Commands (promoted)

These are the six surfaces a student opens the CLI for. They get the
README spotlight; everything else lives further down.

| Command | Purpose |
| --- | --- |
| `course` / `enrollment` | List courses (`course list`) or show a course's detail (`course get <orgUnitId>`). |
| `content` | Show a course's modules + topics. Scope with `--course <id>`; subcommand `get <topicId>` for one topic. |
| `download` | Save a single topic's file (or metadata JSON). Scope with `--course <id>`; positional is the topicId alone. |
| `archive` | Snapshot courses into a versioned, UUID-named blob store. Subcommand `list` for the saved index. |
| `assignment` / `dropbox` | List folders, show my submissions, download files. Scope with `--course <id>`. |
| `quiz` | List quizzes and show my attempts. Scope with `--course <id>`. |

## Common flags

The CLI follows the `huly` convention: singular nouns at top level, scope
via flags, no `me` parent.

```
--course <orgUnitId>     scope to one course
--org CSV                scope to many courses
--from X --to Y          ISO 8601 date or datetime, UTC
--window 1d|7d|30d       relative duration (alias for --from now-…)
--event-type N           calendar enum (1=Assignment, 2=Quiz, …)
-e, --env-file FILE      .env path (default ".env")
--auto-refresh           re-run login on expired session
--no-spinner             suppress progress UI
-v, --verbose            extra stderr noise
--all                    paginate fully
--limit N                cap items returned
--json                   machine-readable output
--yes                    skip confirmation on bulk destructive ops
```

Persistent root flags: `--no-auth-log`, `--version`, `-h/--help`.

## Session file

`~/.config/schooltools/session.json` holds the persisted cookies. Schema:

```json
[
  { "Name": "d2lSessionVal", "Value": "...", "Domain": ".cbe.ab.ca",
    "Path": "/", "Expires": "2026-09-02T23:00:00Z",
    "Secure": true, "HttpOnly": true, "SameSite": 3 }
]
```

`session` shows cookie expiry + the captured Brightspace token;
`token` shows just the token; `whoami` verifies the session live.

## Auth log

`~/.config/schooltools/auth.log` is a JSONL file append-only recording
login events, auto-refresh events, and logout events. Used to measure
session lifetimes and KMSI effectiveness over time. Opt out with
`--no-auth-log` or `SCHOOLTOOLS_NO_AUTH_LOG=1`. File mode `0o600`,
directory `0o700`.

## Download

`schooltools download --course <id> <topicId>` fetches a single D2L
topic and writes it under your home directory (override with `--out`):

```sh
schooltools download --course 1527886 19278283
```

For a **File** topic the underlying file is downloaded with the
server-reported filename. Example output:

```
Downloaded: /home/you/outline.pdf (823.45 KiB)
  course:   1527886
  topic:    19278283 — Course Outline
  source:   /content/enforced/1527886/outline.pdf
```

For a **Link** (or any hosted-HTML) topic the topic metadata JSON is
written instead (`~/topic-<courseId>-<topicId>.json` by default) and
the link URL is printed.

Flags:

- `--course <orgUnitId>` — course scope (required)
- `<topicId>` — positional topic id
- `--out DIR` — output directory (default `~/`)
- `--name NAME` — override the output filename
- `--no-clobber` — refuse to overwrite an existing file
- `--json` — print the topic metadata JSON to stdout instead of writing a file
- `--auto-refresh` — re-run `login` if the saved session is expired

The legacy positional form `download <courseId>/topics/<topicId>` still
works, prints a deprecation notice, and dispatches to the new form.

## Archive

`schooltools archive` snapshots your D2L courses into
`~/.config/schooltools/archive/` (override with `--dir`). The store is
**content-addressed by random UUID** and **versioned**: every blob's
filename is a 128-bit random hex string, and every topic keeps the full
history of versions we have ever saved for it.

```
~/.config/schooltools/archive/
  index.json                      # global manifest: courses + per-course counters
  blobs/<uuid>.json               # every metadata document, flat, named by UUID
  blobs/<sha256>.bin              # every file body (PDF/DOCX/…), content-addressed
  courses/<courseId>/
    index.json                    # per-course: topicId → { title, lastModified,
                                  #             current: <uuid>, versions: […],
                                  #             currentBody: <sha256>,
                                  #             bodyVersions: […] }
```

The CLI is organised around jobs, not storage internals. Bare
`schooltools archive` prints a status summary (counts, freshness,
missing bodies); the mutating pass is `archive update`.

### Commands

| Command | Job |
| --- | --- |
| `archive` | Status (counts, freshness, missing bodies, lock, `try:` hints) |
| `archive update [courseId...]` | Fetch new/changed + fill missing bodies |
| `archive update --dry-run` | Plan only — writes nothing |
| `archive list` | Courses in the store |
| `archive find [query...]` | Search titles / URLs (`--ext`, `--type`, `--with-bodies`, `--missing-bodies`) |
| `archive show <ref>` | Topic record + version history |
| `archive cat <ref>` | Bytes (File) / pretty JSON (else) to stdout |
| `archive path <ref>` | Absolute on-disk path |
| `archive export [--out DIR] [--flat] [--dry-run]` | Copy file bodies out as `<CourseCode>/<Title>.<ext>` |
| `archive prune [--delete]` | Drop old versions; plan only by default |
| `archive verify [--deep]` | Integrity check (pointers, dangling versions, orphans) |

`<ref>` is auto-detected from a numeric topicId, a 32-hex metadata UUID,
or a 64-hex body SHA-256. `--kind metadata|body` forces one kind on
`show`/`cat`/`path`.

### Workflow

```sh
schooltools archive update                        # snapshot every course
schooltools archive update 123456                 # one course
schooltools archive update --course 12345 67890   # two courses (repeatable --course)
schooltools archive update --dry-run              # preview the plan
schooltools archive find "safety contract"        # search
schooltools archive cat 19448654 > contract.pdf   # one file out
schooltools archive export --out ~/pdfs --ext pdf # many files out
schooltools archive prune --delete                # reclaim disk
schooltools archive verify                        # integrity check
```

### Convergence and idempotence

Each pass is incremental and **append-only** at the blob layer:
topics whose `LastModifiedDate` has not moved are skipped; new or
modified topics get a brand-new blob under `blobs/<uuid>.json`. The
old blob is left on disk untouched and the topic's `versions` history
grows. `update` converges: it also fills in any File topic whose
body is missing on disk (whether because the first attempt errored,
or because the body was deleted by a future prune that didn't yet
run), so the archive converges to "every File topic has a body" after
each pass.

The `archive` blob store is **never** modified or deleted by `update`.
Reclamation happens via `archive prune --delete`.

## Prune

`schooltools archive prune` walks every `courses/<id>/index.json`,
builds the set of "live" blobs (every topic's current metadata UUID
**and** current body SHA), and deletes every other file in `blobs/`.
After pruning, per-course indexes are rewritten in the same pass so
no `Versions` / `BodyVersions` record points at a deleted blob.

```sh
schooltools archive prune              # plan only — reports what would be deleted
schooltools archive prune --delete     # apply
schooltools archive prune --json       # machine-readable plan
schooltools archive prune --dir /other # prune a non-default archive root
```

`prune` is plan-only by default — destructive commands print a plan
first; applying is an explicit `--delete`. Use `archive prune --wait`
when you need to wait on the lock instead of failing.

## Advanced / rarely used

The commands in this section are **complete and tested**, but for most
users they are not part of the day-to-day workflow — the D2L web UI
covers them better. Listed here for completeness and for users who
specifically need scriptable access.

### Cross-course queries (flat top-level, no `me` parent)

| Command | Endpoint family | Notes |
| --- | --- | --- |
| `calendar [--from X --to Y] [--event-type N] [--org CSV]` | `/calendar/events/myEvents/` | Date window required (default: 30 days centred on today). |
| `due [--window 7d] [--from X --to Y] [--org CSV]` | `/content/myItems/due/` | Items with upcoming due dates. |
| `overdue [--org CSV]` | `/overdueItems/myItems` | Items past their due date. |
| `update [--org CSV]` | `/updates/myUpdates/` | Per-course unread message / unread grade counts. |
| `feed [--since X] [--until Y]` | `/d2l/api/lp/<lp>/feed/` | Aggregated activity feed across all tools. |
| `news [--since X]` | `/d2l/api/le/<le>/news/user/me/` | Cross-course news (omit `--course`). |
| `grade` | `/d2l/api/le/<le>/grades/final/values/myGradeValues/` | Cross-course finals, capped at 100. |

### Per-course (scope with `--course <id>`)

| Command | Endpoint family | Notes |
| --- | --- | --- |
| `news [--course <id>] [get <newsId>] [attachment <newsId> <fileId>]` | `/news/`, `/news/<id>/attachments/<fileId>` | Course announcements. |
| `grade [--course <id>] [--final] [get <gradeObjectId>]` | `/grades/values/myGradeValues/`, `/grades/final/values/myGradeValue` | Numeric grades. |
| `discussion [--course <id>] [forum <forumId>] [post <topicId>]` | `/discussions/forums/`, `/posts/` | Page-number pagination, per D2L research §2.2. |

### User-scoped

| Command | Endpoint family | Notes |
| --- | --- | --- |
| `award` | `/d2l/api/bas/<v>/issued/users/me/` | List issued. |
| `award get <id>` | same | One. |
| `award download <certificateId>` | `/d2l/api/bas/<v>/issued/certificates/<id>/pdf` | PDF stream. |

> **Removed surfaces (kept here as record):** `locker` and `eportfolio`. The CBE
> tenant ships an empty locker for students and disables the ePortfolio
> product (`/d2l/api/eP/<v>/` returns 403 Forbidden). The code is removed; the
> endpoints stay listed in `D2L_API_RESEARCH.md` for tenants that do enable
> them.

Each of these has full `--help` text with endpoint details.

## Migration notes

### 0.1.0 → 0.2.0

The CLI was previously TypeScript + commander + cheerio + tough-cookie;
the Go port at 0.1.0 replaced those with cobra, goquery, and
`net/http/cookiejar`. The 0.2.0 redesign adds D2L Valence feature
parity (see `.kilo/plans/cli-feature-parity-redesign.md`). Breaking
changes:

| Old | New |
| --- | --- |
| `schooltools content 1527886` | `schooltools content --course 1527886` |
| `schooltools download 1527886/topics/19278283` | `schooltools download --course 1527886 19278283` |
| `schooltools archive --list` | `schooltools archive list` |
| `schooltools archive --diff <id>` | `schooltools archive update --dry-run [--course <id>]` |
| `schooltools archive` (bare) | `schooltools archive` (now status) or `schooltools archive update` (fetch) |
| `schooltools archive files --ext pdf` | `schooltools archive find --type File --ext pdf` |
| `schooltools archive read <topicId>` | `schooltools archive cat <topicId>` (pass `--meta` to force the metadata blob) |
| `schooltools archive body <sha>` | `schooltools archive cat <sha>` |
| `schooltools prune --dry-run` | `schooltools archive prune` (plan only) |
| `schooltools prune --delete` | `schooltools archive prune --delete` |
| `schooltools courses` / `courses --json` / `courses --all` | `schooltools course list [--json] [--all] [--limit N]` |

The legacy positional forms of `content` and `download` still work, print
a one-line deprecation notice, and dispatch to the new form. They are
hidden from `schooltools --help` and will be removed in a future release.
Everything else (`login`, `logout`, `whoami`, `doctor`,
`session`, `session ttl`, `auth log`, `systemd`, `token`) is unchanged.

### Earlier (TypeScript → Go)

The old TypeScript CLI used a different session-file schema. Re-running
`schooltools login` once after upgrading was required; existing
`auth.log` entries are forward-compatible.
