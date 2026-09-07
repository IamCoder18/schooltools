# schooltools — known issues / UX rough edges

Captured from real usage on **2026-09-06**. Each issue includes a
reproduction, observed vs. expected behaviour, and the source file(s)
most likely responsible.

---

## 1. `archive list` table hides the real topic count

**Repro:** `schooltools archive list`

**Observed:** Table prints `TopicsTotal / TopicsNew / TopicsStale` as `0`
for every course, even though the archive on disk holds 246 topics and
980 blobs across the 4 enrolled courses.

**Expected:** Table should reflect `topics` from each course's
`courses/<id>/index.json` (it does read these — they exist on disk and
are non-empty).

**Likely source:** `cmd/archive.go` — table rendering for `archive list`.
Also check `internal/archive` for the index schema field name used for
the count column.

---

## 2. `archive --diff` reports "0 fetched, 246 unchanged" while index says 0 topics

**Repro:**
```
schooltools archive --diff
schooltools archive list --json   # shows TopicsTotal: 0, TocFetchedAt: 0001-01-01T00:00:00Z
```

**Observed:** Diff claims 246 topics scanned and unchanged; per-course
index.json has `TopicsTotal: 0` and a zero-value `TocFetchedAt`.

**Expected:** If diff runs the TOC via the live API and concludes
"nothing new," it should still persist the TOC into the index and update
`TocFetchedAt`. Or at minimum surface a warning that the on-disk
index is stale.

**Likely source:** `cmd/archive.go` (`archiveRun` / diff path) and
`internal/archive` (TOC persistence logic).

---

## 3. `archive` (non-diff) after first run says "0 fetched, 246 unchanged"

**Repro:** `schooltools archive 1527886 1538295 1533188 1522695`

**Observed:** Reports "0 fetched, 246 unchanged" and "File bodies: 0
new, 0 unchanged (same SHA), 0 errors." But the bodies had never been
downloaded (only metadata `.json` blobs existed; no `.bin` blobs for the
two calendar topics until I went hunting manually).

**Expected:** "0 unchanged" should only print when there are actually
existing bodies to compare against. If the topic has no body blob, say
"0 new, N missing" or auto-fetch on first archive pass.

**Likely source:** `internal/archive` — body-tracking math conflates
"absent" with "unchanged."

---

## 4. `archive read <topicId>` returns metadata, not the file body

**Repro:**
```
schooltools archive read 19430869 > math_schedule.pdf
file math_schedule.pdf   # → "ASCII text", 89 bytes
```

**Observed:** Output is the topic metadata JSON (`Title`, `Url`,
`LastModifiedDate`, …), not the PDF bytes.

**Expected:** For `type: File` topics, `archive read` should default to
emitting the body (or expose a `--body` / `--meta` switch).

**Likely source:** `cmd/archive.go` (`archiveReadCmd`). Body SHA lives
in `topic.bodyVersions[0].sha256` (`currentBody`), distinct from the
metadata `current` SHA.

---

## 5. `archive path <topicId>` resolves to the metadata `.json`, not the body `.bin`

**Repro:**
```
schooltools archive path 19430869
# → /home/aarav/.config/schooltools/archive/blobs/862a49d5…json   (metadata)
```

**Observed:** For File-type topics, callers usually want the `.bin`
body path. Today you have to discover `currentBody` via
`archive topic --json` first, then call `archive path <bodySha>`.

**Expected:** `archive path <topicId>` should return the body path for
File topics. Or add `archive body-path <topicId>` / `archive extract
<topicId>` that does the full retrieval in one command.

**Likely source:** `cmd/archive.go` (`archivePathCmd`).

---

## 6. `archive topic <id>` requires `--course` even though the topic is uniquely indexed

**Repro:** `schooltools archive topic 19430869`

**Observed:** Errors: `--course <orgUnitId> is required to look up
archived topic 19430869`.

**Expected:** Topic IDs are unique across the archive; the reverse
lookup `topicId → courseId` should be derivable from the global index.

**Likely source:** `cmd/archive.go` (`archiveTopicCmd`). `internal/archive`
probably lacks a topicId→courseId map.

---

## 7. `schooltools news get <id>` requires `--course` for global IDs

**Repro:** `schooltools news get 2906441` (cross-course news id)

**Observed:** Errors: `--course <orgUnitId> is required for 'news get'`.

**Expected:** Cross-course news already flows without `--course` for
listing; `get` should accept the ID alone.

**Likely source:** `cmd/per_course_rare.go` (`newsGetCmd`).

---

## 8. `schooltools news list` does not exist

**Observed:** No listing subcommand; only `get` and `attachment`.
Listing requires `news --json --since YYYY-MM-DD` at the top level.

**Expected:** `news list [--course ID] [--since …] [--until …]` for
discoverability.

**Likely source:** `cmd/per_course_rare.go` (add the subcommand).

---

## 9. `news --json` emits a bare top-level array, not an envelope object

**Repro:**
```
schooltools news --json --since 2026-08-01 | head -c 200
# → '[\n  {\n    "Id": ...'
```

**Observed:** Top-level JSON is `[]item`. Other commands (e.g.
`content --json`, `course list --json`) wrap things in an object, so
parser code has to special-case news.

**Expected:** Wrap in `{ "news": [...], "since": "...", "count": N }`
or document the shape in the help.

**Likely source:** `cmd/per_course_rare.go` (`newsCmd` JSON marshal).

---

## 10. `news --json` items have `Body` as a nested `{Html, Text}` object

**Repro:**
```
python3 -c "import json,sys; print(json.load(sys.stdin)[0]['Body'])"
schooltools news --json --since 2026-08-01
# → {'Html': '<p>...', 'Text': ''}
```

**Observed:** Body is not a string, so naive slicing fails with
`TypeError: unhashable type` / `KeyError: slice`. The default table
view apparently renders `Body.Html`, but `--json` returns both raw.

**Expected:** Either flatten to a string in `--json`, or expose
`--body-format text|html|both`.

**Likely source:** `cmd/per_course_rare.go`.

---

## 11. `content --json` emits a bare top-level array too

**Repro:**
```
schooltools content --course 1538295 --json
# → '[\n  {\n    "Title": "General", ...'
```

**Observed:** Top level is a list of modules. Same discoverability
issue as #9.

**Expected:** `{ "courseId": ..., "modules": [...] }`.

**Likely source:** `cmd/content.go` (`contentRun` JSON path).

---

## 12. `content --tree` doesn't show file extensions on File topics

**Observed:** Topics print as `[File]` with no extension. Forced a
switch to `--json` to disambiguate `.pdf` vs `.docx` (and the
"science timeline" turned out to be `.docx`).

**Expected:** `[File.pdf]`, `[File.docx]`, `[File.pptx]`, etc., using
the URL extension.

**Likely source:** `cmd/content.go` (tree rendering).

---

## 13. Session/doctor reports contradict each other

**Repro:**
```
schooltools session        # Brightspace token EXPIRED at 2026-09-05T20:42:32Z
                           # SAML assertion EXPIRED at 2026-09-05T00:37:52Z
schooltools doctor         # "hasValidSession() ✓ valid"
```

**Observed:** `doctor` reports the session as valid while `session`
shows the SAML and Brightspace tokens are expired. The user has to
guess whether to `schooltools login` again.

**Expected:** Single source of truth. `doctor` should warn when either
the SAML window or the Brightspace OAuth token is expired.

**Likely source:** `internal/session` (validity predicate) vs.
`cmd/session.go` and `cmd/doctor.go`.

---

## 14. No obvious way to refresh just the Brightspace OAuth token

**Observed:** Only `schooltools login` exists (full SAML round-trip via
.env credentials). After SAML expires, no subcommand can refresh just
the OAuth bearer without re-running login.

**Expected:** `schooltools token --refresh` or `schooltools login --refresh-token`
if a refresh token is exposed by Brightspace's OAuth flow.

**Likely source:** `cmd/login.go`, `internal/login`.

---

## 15. Live `content --tree` calls frequently hang / are very slow

**Repro:** Several `schooltools content --course <id> --tree` calls
ran past their timeout window and were aborted. The same data was in
the archive and could be served instantly.

**Expected:** Surface progress / set a sensible per-request deadline;
or hint that the user should run `schooltools archive` first and use
`--archive`.

**Likely source:** `internal/httpclient` timeouts, `internal/content`
pagination.

---

## 16. No one-shot command to extract a File topic's body

**Observed:** End-to-end flow today to extract a PDF:

1. `schooltools archive list` (or scan the index)
2. `schooltools content --json --course …` to find `topicId`
3. `schooltools archive topic <id> --course … --json` to find
   `currentBody` SHA
4. `schooltools archive path <bodySha>` to get the path
5. `cp` it locally

**Expected:** `schooltools download <topicId> [--course …] [--out …]`
or `schooltools archive extract <topicId>`.

**Likely source:** `cmd/download.go` already exists — confirm it covers
this flow and either fix or document it.

---

## 17. `archive` body-tracking table column is misleading

**Observed:** "File bodies: 0 new, 0 unchanged (same SHA), 0 errors"
prints even when no bodies have ever been fetched.

**Expected:** "0 new, 0 unchanged" should not appear when the archive
has no bodies to compare against. Use "0 fetched, N eligible" or
similar.

**Likely source:** `cmd/archive.go` summary printer.

---

## 18. No machine-readable way to enumerate file topics across courses

**Observed:** To find "all PDFs in Content" across N courses you have
to write your own walker that parses `courses/<id>/index.json`,
because `archive list` doesn't expose a flat file index.

**Expected:** `schooltools archive files [--ext .pdf] [--course …]`
or JSON output from `archive list` that includes topic summaries.

**Likely source:** `cmd/archive.go`.

---

## 19. `--since` on `news` has no companion `--until`

**Observed:** Can pass `news --since 2026-08-01` but no `--until`. To
fetch an arbitrary window you have to switch to per-course `--course`
listing.

**Expected:** Symmetric `--since` / `--until`.

**Likely source:** `cmd/per_course_rare.go`.

---

## 20. `archive path <topicId>` should distinguish metadata vs body paths

**Observed:** With one SHA, you don't know which one you're getting.

**Expected:** Either separate subcommands (`archive meta-path`,
`archive body-path`) or an explicit `--metadata` / `--body` flag on
`archive path`.

**Likely source:** `cmd/archive.go`.

---

## Quick-fix priority suggestion

If only a few of these get fixed, the highest-impact ones for a CLI
user pulling real files out of D2L are:

- **#4 / #5 / #16** — make extracting a file body a one-command operation
- **#1 / #2 / #3** — fix the archive/index divergence so the user can
  trust `archive list`
- **#11 / #12** — stable JSON shape + file extensions in tree view
- **#13 / #14** — honest session/token status reporting
