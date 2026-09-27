# schooltools archive — UX redesign plan

Captured from real-CLI verification on **2026-09-24** and a UX
review that followed. The archive surface currently mirrors storage
internals (uuid, sha, blob, topic) instead of user jobs ("keep my
stuff fresh", "find the PDF", "get it out"). This plan reworks it
around jobs.

**Backwards compatibility is explicitly out of scope** — early stage,
so the command surface may be restructured freely.

---

## 0. Findings from verification (evidence)

All commands were exercised against the real CLI and live D2L:

| Command | Result |
| --- | --- |
| `archive list` (human + `--json`) | works; table wraps badly in narrow terminals |
| `archive topic <id>` (human, `--json`, `--course`) | works |
| `archive read <uuid>` / `read <topicId>` / `--meta` / `--body-format hex` | works |
| `archive body <sha>` | works |
| `archive path` (UUID, SHA, topicId, `--kind`, `--course`) | works |
| `archive files` (table, `--json`, `--ext`, `--course`, `--with-bodies`) | works (284 topics / 276 with bodies) |
| `archive --diff` (all + positional course) | works (333 topics, all unchanged) |
| `archive` | works (5 courses, 333 topics, no fetches) |
| `prune --dry-run` | **BUG: not a dry run — deleted 814 blobs / 1.22 GiB** |
| `prune` (real) | works (no-op after the above) |

### Bug P0: `prune --dry-run` is destructive

`cmd/prune.go` `runPruneDry` calls `archive.Prune()`, which is the
real, deleting implementation (`internal/archive/archive.go` —
`os.Remove` per orphan blob). The comment above `runPruneDry` claims
"without touching disk"; that is wrong. Observed: first
`prune --dry-run --json` reported `would delete: 814 (1.22 GiB)`, and
the blobs directory went from 1147 → 333 entries before any other
command ran.

Archive integrity survived (all `current`/`currentBody` pointers still
resolve) but version history was destroyed by a command documented as
read-only. Every fix in this plan is downstream of taking that
seriously: **destructive commands must default to a plan.**

### Bug P1: `prune` leaves dangling version pointers

Prune deletes non-current blobs but leaves `versions`/`bodyVersions`
entries pointing at them. `archive topic` then prints
`meta v1 uuid=…` for blobs that no longer exist. The index and the
blob store must change together.

### Bug P1: missing bodies never fill in

Body download only runs for topics whose **metadata** changed in that
pass (`internal/archive/archive.go`, body stage gate). Consequences:

- The 8 of 284 file topics with `hasBody=false` will *never* get
  bodies unless someone edits them in D2L.
- Any body download error (`bodiesFailed`) leaves a permanent hole.

"Update" must mean "the archive is complete", not "changed rows are
current".

### Papercuts (P2)

1. **Bare `archive` mutates.** No preview, no confirmation; a
   multi-course network pass starts on Enter.
2. **`--diff` is a flag, not a subcommand** — `archive diff` errors.
   Inconsistent with the rest of the CLI.
3. **Course scoping is three different things:**
   `archive topic --course X`, `archive path --course X`, but the
   parent `archive --course X` errors ("unknown flag") — scoping is
   positional there.
4. **`--files` flag vs `files` subcommand name collision.**
   `--files` defaults to `true`, so runs silently re-hash bodies; the
   summary line ("new / reused / errors") is opaque.
5. **`read <topicId>` magic.** Returns body bytes for File topics,
   metadata JSON for everything else, while `--help` says "metadata
   blob". `read` and `body` are near-duplicates.
6. **Identifier soup.** `path` auto-detects 32-hex UUID / 64-hex SHA /
   numeric topicId — helpful, but undocumented in `--help`.
7. **Double error printing.** Failures print the message on stderr and
   stdout, then exit 1 (cobra echo + `RunE` return).
8. **JSON shapes inconsistent** (already KNOWN_ISSUES #9/#10/#11):
   `files --json` has a `count` envelope, `list --json` emits the raw
   index, `topic --json` an ad-hoc map.
9. **Table layout.** `archive list` columns assume 120+ col terminals;
   one row wrapped into 13 lines at 80 cols. `Body` column uses `●`/`○`
   with no legend. Absolute timestamps eat width.
10. **`prune` is top-level** while everything else is under `archive`.
11. **Duplicate definitions.** `--dir`/`--json` redeclared on every
    subcommand instead of persistent flags on the group.
12. **Session UX.** `--auto-refresh` defaults to false even though KMSI
    can renew silently; `-e` short flag for `--env-file` is noise.

---

## 1. Design principles

1. **Bare commands teach, never mutate.** The parent with no args
   shows status and next steps.
2. **Destructive commands print a plan first; applying is an explicit
   flag.** (`prune` → plan; `prune --delete` → apply.)
3. **One rule per concept.** One `ref` grammar, one `--course` flag,
   one JSON envelope, one output-mode trio (`human` / `--json` /
   `--plain`).
4. **Update converges.** After `archive update`, every File topic has
   a body on disk (unless `--no-bodies`) and every pointer resolves.
5. **Unix-friendly.** `cat`/`path` write exactly the bytes/path to
   stdout and nothing else; everything else goes to stderr.

---

## 2. Target command surface

| Command | Job | Replaces |
| --- | --- | --- |
| `schooltools archive` | Landing **status**: last update, counts, missing bodies, disk use, freshness warning, "try next" hints | *(new — bare command previously ran a mutating pass)* |
| `archive update [scope]` | Fetch new/changed **and fill missing bodies** | bare `archive` |
| `archive update --dry-run` | Preview the plan | `archive --diff` |
| `archive list` | Courses in the store | `archive list` |
| `archive find [query]` | Search topics by title/URL; `--ext`, `--course`, `--type` filters | `archive files` (= `find --type File`) |
| `archive show <ref>` | One topic record + version history | `archive topic` |
| `archive cat <ref>` | Bytes (File topics) / pretty JSON (else) to stdout | `archive read` + `archive body` (merged) |
| `archive path <ref>` | Absolute on-disk path | `archive path` |
| `archive export --out DIR` | Copy files out as `CourseCode/Friendly Title.pdf`, collision-safe names | *(new — biggest missing job: "give me my PDFs")* |
| `archive prune` | Drop old versions; prints plan; `--delete` applies | top-level `prune` |
| `archive verify` | Integrity check: pointers resolve, SHA names match content, orphan report | *(new — the safety net)* |

### The `ref` grammar (one concept, documented once)

`<ref>` is any of, auto-detected:

- **topicId** — numeric D2L topic id (`19448654`)
- **metadata UUID** — 32 hex chars (`13d4b7a480969f146225f65e811a3621`)
- **body SHA-256** — 64 hex chars (`c1a8e46b…472b`)

`--kind metadata|body` forces interpretation. `show`/`cat`/`path`
accept exactly this. `cat` on a File topic emits raw bytes (redirect
to `.pdf`); anything else pretty-prints JSON.

---

## 3. Behavior changes (the trust fixes)

1. **Bare `archive` = status.** Shows: schema version, last update
   (relative + warning when stale), courses/topics/files counts,
   `N of M file topics have bodies`, blob bytes on disk, lock state,
   and 2–3 "try next" lines. Empty state: *"No archive yet — run
   `schooltools archive update`."*
2. **`update` converges the store.** Body stage runs for:
   (a) new/modified File topics (as today), (b) any File topic with
   `currentBody == ""`, (c) any topic whose current body blob is
   missing on disk. `--no-bodies` opts out (metadata only).
3. **`update --dry-run` replaces `--diff`.** Same plan output as
   today's diff table/JSON, no writes except nothing (today `Diff`
   still calls `SaveIndex` — a dry run must write **nothing**, not
   even the index).
4. **`prune` semantics fixed.** Plan-only by default:
   ```
   $ schooltools archive prune
   Would delete 814 blobs (1.22 GiB); keeping 333 current versions.
   Version history older than current will be dropped.
   Re-run with --delete to apply.
   ```
   `--delete` performs it. When a blob is deleted, its
   `versions`/`bodyVersions` record is trimmed from the index in the
   same transaction (fixes dangling pointers) — history becomes
   "current only" after prune, and `show` can never print a dead uuid.
5. **`verify` checks:** every `TopicIndex.Current` resolves to an
   existing `.json` blob; every `CurrentBody` resolves to an existing
   `.bin` whose SHA-256 matches its filename; per-course index parses;
   global index course ids have directories; orphan blobs reported.
   `--deep` re-hashes bodies. Exit 0 clean / 1 problems found. JSON
   envelope for cron.
6. **Errors print once.** Set `SilenceErrors` and print a single line
   (or `{"error": …}` when `--json`).
7. **`--auto-refresh` default on.** `--no-auto-refresh` opts out.

---

## 4. Consistency rules

1. **Course scoping: `--course <id>` (repeatable) on every command.**
   Filter on `list`/`find`/`export`; disambiguator on
   `show`/`cat`/`path`. `update --course X` (and shorthand positional
   `update X`) scopes the pass; no other positional course args.
2. **Persistent flags on the `archive` group**, defined once:
   `--dir`, `--json`, `--plain`, `--quiet`, `--no-spinner`,
   `--no-auth-log` stays global. Kill `-e`; keep `-v`, `-q`, `-h`.
   `--files` goes away as a name (→ `update --no-bodies`).
3. **JSON envelopes everywhere.** Collections:
   `{"count": N, "items": [...]}`; records: a single object;
   summaries: flat object (as `update` does today). Human tables show
   relative times (`3d ago`); JSON shows absolute ISO-8601.
4. **`--plain` TSV** (header row + tab-separated values) on `list`,
   `find`, `export --list`, `show` so pipelines don't need jq:
   `find --type File --plain | fzf | cut -f1 | xargs archive cat`.
5. **Tables fit 80 columns.** Truncate with ellipsis (full value via
   `show`/`--json`); replace `●`/`○` with `yes`/`—` (or keep glyphs
   with a one-line legend on TTY). Relative timestamps free width.
6. **Examples in every `--help`** (spread `path`'s style):
   ```
   Examples:
     schooltools archive find "safety contract"
     schooltools archive export --course 1522695 --ext pdf --out ~/Documents/
     schooltools archive cat 19448654 > contract.pdf
   ```
7. **Helpful failures.** "topic 999 not found" → append
   `try: schooltools archive find <title words>`. Lock held →
   include lock path and exit nonzero (scripts should notice), with
   `--wait` to block instead.

---

## 5. `export` sketch (new)

```
schooltools archive export --out ~/Documents/course-pdfs \
    --course 1522695 --ext pdf
```

- Layout: `<out>/<CourseCode>/<sanitized Title>.pdf`
- Collisions: `Title (2).pdf`
- Missing bodies: skip with a stderr warning and nonzero summary count
  (after the convergence fix this should be rare)
- `--flat` to skip the per-course folder; `--dry-run` prints the copy
  plan (files + destinations + sizes)
- `export <ref>` copies single refs too (sugar for
  `cp "$(archive path …)"`)

---

## 6. Migration landmines

1. **systemd timer** (`internal/systemd/units/*.tmpl`) almost
   certainly runs `schooltools archive`, which will become read-only
   `status`. Units and docs **must** move to
   `schooltools archive update` in the same change or backups stop
   silently. Same for any user cron.
2. **SchemaVersion bump** (v3 → v4) if prune trims history records;
   `verify`/`status` should detect and explain older stores.
3. **Store location** (optional): default root is
   `~/.config/schooltools/archive` holding 1.2 GiB of PDFs — XDG
   *data*, not config. Consider `XDG_DATA_HOME`/`~/.local/share/…`
   with a "found store at old path, pass `--dir` or move it" hint.
4. **Close stale KNOWN_ISSUES** that this work supersedes: #4
   (`read` returning metadata for File topics — fixed by `cat` rules),
   #18 (no machine-readable file enumeration — fixed by `find`), and
   #9/#10/#11 when the JSON envelope rule lands.

---

## 7. Suggested implementation order

1. **P0 bugfix:** `prune --dry-run` → real dry run (stop the
   bleeding), plus dangling-pointer fix in the same PR.
2. Safety defaults: `prune` plan/`--delete`, error printing once.
3. Convergence: `update` fills missing bodies + missing blobs.
4. Rename/restructure: `update`, `find`, `show`, `cat` merge,
   `--course` everywhere, persistent flags. Update systemd templates
   in this PR.
5. `status` as bare command + freshness warnings + empty states.
6. Output polish: envelopes, `--plain`, 80-col tables, relative
   times, examples in help.
7. `verify` (+ optional `--deep`).
8. `export`.
9. Optional: `open <ref>`, shell completions, XDG data-dir move.

Each step keeps `go build ./...`, `go test -race ./...`,
`go vet ./...` (and `golangci-lint run --timeout=5m`) clean, adds
tests beside new `internal/<pkg>` code, and updates `CHANGELOG.md`
under `[Unreleased]` — per `AGENTS.md`. One concern per PR.

---

## 8. Optional niceties (backlog)

- `archive open <ref>` — `xdg-open` the resolved body path
- Shell completions for refs (topicIds from `find`)
- `status` reflects systemd timer state ("last run by timer 6h ago")
- `find --limit N` default with "…and N more" footer
- Lock `--wait` mode for cron overlap
