# D2L / Brightspace Valence API — Research Notes

Scope: a reference for working with the Calgary Board of Education tenant at
`https://d2l.cbe.ab.ca` from the `schooltools` CLI. Built by combining:

  - The official D2L Valence reference at <https://docs.valence.desire2learn.com/reference.html>
    (routing table, per-resource docs, per-endpoint OAuth2 scopes).
  - The community-maintained student-side endpoint catalog at
    <https://github.com/Aaryan-Kapoor/d2l-cli/blob/main/D2L_API_REFERENCE.md>.
  - Live probes against `d2l.cbe.ab.ca` performed 2026-09-04 with the stored
    session at `~/.config/schooltools/session.json`.

Throughout, every URL is relative to the tenant base:

```
https://d2l.cbe.ab.ca
```

---

## 1. Platform model

D2L's REST API is called **Valence** (now also "Brightspace Developer
Platform"). It is OAuth2‑authenticated, REST/JSON, versioned per product
family, and uses **per‑URL OAuth2 scopes** instead of a single global role.

### 1.1 API products (URL roots)

| Product code | Path root | What lives there |
| --- | --- | --- |
| `lp` | `/d2l/api/lp/<v>/…` | Learning Platform: users, enrollments, org structure, groups, sections, feed, profile, notifications. |
| `le` | `/d2l/api/le/<v>/<orgUnitId>/…` | Learning Environment: courses, content (modules/topics), dropbox, quizzes, surveys, discussions, calendar, news, grades, competencies, locker, LTI. |
| `bas` | `/d2l/api/bas/<v>/…` | Awards / Badges / Certificates. |
| `eP` | `/d2l/api/eP/<v>/…` | ePortfolio (artifacts, collections, reflections, presentations). |
| `lr` | `/d2l/api/lr/<v>/…` | Learning Object Repository (SCORM / LOR objects). |
| `bfp` | `/d2l/api/bfp/<v>/…` | Brightspace for Parents (parent↔child relationships). |

**Version discovery.** Every product exposes its supported versions:

```
GET /d2l/api/versions/                          → ["1.47", …] (all products)
GET /d2l/api/<product>/versions/                → e.g. ["1.40","1.41","1.47"]
GET /d2l/api/<product>/versions/<version>       → 200 OK if supported
POST /d2l/api/versions/check                    → bulk
```

Use the **highest** version listed. CBE's existing CLI hardcodes
`LEVersion = "1.47"` (see `internal/ua/ua.go:12`) — the Aaryan‑Kapoor ref
shows another tenant on `1.80`, but version discovery on CBE itself currently
fails with our stored session (see §3).

### 1.2 Authentication

For a **CLI tool acting on behalf of a user** (what schooltools is),
the right model is the **browser session cookie flow** — the user
authenticates via ADFS SAML, the resulting `d2lSessionVal` cookie is
persisted, and every subsequent Valence API call is made with that
cookie. This works without any registered application. The cookies
authenticate both the HTML routes (`/d2l/home`) and the JSON REST API
(`/d2l/api/lp/...`, `/d2l/api/le/...`).

For **third‑party applications** that want to call the API on behalf of
a user without their browser, D2L provides a separate OAuth2 flow at:

```
POST /d2l/auth/api/token                        → application/x-www-form-urlencoded
                                                grant_type=password | refresh_token | authorization_code | client_credentials
                                                username + password + client_id + client_secret
```

…or for the SAML/SSO case (which CBE uses), the password grant is
preceded by completing the ADFS login to obtain the `d2lSessionVal`
cookie, which is then exchanged at `/d2l/auth/api/token` for a Bearer
JWT (~1 hour). After Bearer auth:

```
Authorization: Bearer <token>
Accept: application/json
```

**schooltools uses the cookie flow, not the Bearer flow.** It is the
correct choice for a single-user CLI — no Client ID/Secret registration
in Brightspace Manage Extensibility is needed, no token refresh logic.
Tradeoff: the session expires server-side and must be re-established
with `schooltools login` periodically, which the CLI already does
automatically via `--auto-refresh`.

---

## 2. Conventions every endpoint shares

These were derived from the official docs and the community ref. They are
consistent across the `lp` and `le` products and worth memorising because
every CLI command has to handle them.

### 2.1 Pagination — bookmark (most lists)

```
{ "PagingInfo": { "Bookmark": "eyJ...", "HasMoreItems": true, "ItemsRemaining": N }, "Items": [ … ] }
```

Walk by passing `?bookmark=<value>` on the next call. There is no random
access — start at the empty/missing bookmark and follow `HasMoreItems`.

### 2.2 Pagination — page number (discussion posts only)

```
?pageSize=20&pageNumber=1
```

Discussion posts, surveys, and a few other places use this instead. See
the discussion routes in §6.

### 2.3 Comma‑separated batch inputs

When an endpoint takes a list of org units, users, or grade object IDs,
the convention is `<name>CSV=…`:

```
?orgUnitIdsCSV=123,456,789
```

Use this aggressively — it is the only way to avoid the API rate limit
on multi-course dashboards.

### 2.4 Datetime format

All datetimes on the wire are **UTC ISO 8601** with millisecond precision:

```
2026-09-15T00:00:00.000Z
```

There is no timezone offset on the wire even when querying for a specific
school's day — the caller converts.

### 2.5 File endpoints

Endpoints whose path ends in `/file` (and a few `…/attachments/…`,
`…/submissions/…/files/…`, `…/certificate/…/pdf`, locker `/file` paths)
return **raw byte streams**, not JSON. Use `Content-Disposition` /
`Content-Length` from the response, not JSON.

### 2.6 Rate limiting

429 with `Retry-After`. The official doc notes "API call‑rate limit
exceeded" as a status on almost every route. Always batch with
`orgUnitIdsCSV`, and respect `Retry-After`. Existing CLI has a retry
package at `internal/retry/`.

### 2.7 Enumerated type constants

These recur everywhere:

  - **OrgUnit types**: `1`=Org, `2`=Department, `3`=Course Offering,
    `4`=Course Template, `5`=Course Section, `6`=Group, `7`=Group Category,
    `8`=Semester, `9`=Brightspace Community, `10`=Account, …
  - **Topic types**: `1`=File, `2`=Link, `3`=Lesson (HTML), `4`=Video,
    `5`=Audio, `6`=Image, `7`=LTI, `8`=SCORM, `9`=External tool, `10`=…
  - **Module `Type`**: `0`=sub-module, `1`=topic.
  - **Calendar `eventType`**: `1`=Assignment, `2`=Quiz, `3`=Discussion,
    `4`=Module, `5`=Custom.
  - **Calendar `association`**: `0`=All, `1`=AssociatedWithContent,
    `2`=NotAssociatedWithContent.

### 2.8 Release conditions are invisible

If a piece of content is gated by a release condition the user does not
satisfy, the API simply does not include it — there is no 403. Filtering
"what's visible" has to be done by re-walking the TOC after a release
event, not by asking for "hidden" content.

---

## 3. CBE tenant — live probe results (2026‑09‑04)

The session at `~/.config/schooltools/session.json` was loaded into a Go
probe (`/tmp/kilo/d2lprobe`) and used to GET a representative set of
endpoints. **Two probe passes were run:**

  - **Pass 1** (initial, pre‑login): the session had server‑side aged
    out, so cookies alone were rejected with 403 on every JSON API call
    while still being accepted on the HTML homepage.
  - **Pass 2** (after `schooltools login`): all four cookies are fresh
    and the full JSON API surface is reachable. **All the routes the
    existing CLI uses today work end‑to‑end** (`courses`, `content`,
    `archive`, `download`).

### 3.1 Pass 1 (stale session) — what we saw first

| Endpoint family | Result | Interpretation |
| --- | --- | --- |
| `GET /d2l/home` | 200 HTML | Server still recognises the cookie for HTML routes. |
| `GET /d2l/le/manageCourses/api/mycourses` (used by `schooltools courses`) | 200 HTML, body is login page | Same — the *content* is HTML, not a JSON payload. |
| `GET /d2l/api/versions/` | 403 | "Authentication required" — JSON API doesn't accept the stale cookie. |
| `GET /d2l/api/lp/1.47/users/whoami` | 403 | Same. |
| `GET /d2l/api/le/1.47/{cid}/content/toc` | 403 | Same. |

### 3.2 Pass 2 (fresh `schooltools login`) — what actually works

| Endpoint | HTTP | Content‑Type | Notes |
| --- | --- | --- | --- |
| `GET /d2l/home` | 200 | HTML | Now returns the real user home page (User: *Aarav Sharma*). |
| `GET /d2l/api/versions/` | 200 | JSON | Lists every product. |
| `GET /d2l/api/lp/versions/` | 200 | JSON | `LatestVersion: 1.97` for `le`, `1.63` for `lp`, `2.5` for `eP`, `1.6` for `bas`, `1.3` for `lr`. |
| `GET /d2l/api/lp/1.47/users/whoami` | 200 | JSON | `{"Identifier":"782002","FirstName":"Aarav","LastName":"Sharma","UniqueName":"1100081362","ProfileIdentifier":"ylir9fLLYf","Pronouns":null}` |
| `GET /d2l/api/lp/1.47/profile/myProfile` | 403 | HTML | Forbidden even with a valid session — likely a missing scope (`users:own_profile:read`?). |
| `GET /d2l/api/lp/1.47/organization/info` | 200 | JSON | `{"Identifier":"6605","Name":"Calgary Board of Education","TimeZone":"America/Edmonton"}` |
| `GET /d2l/api/lp/1.47/accountSettings/mySettings/locale/` | 200 | JSON | `en-CA`, locale id `100001`. |
| `GET /d2l/api/lp/1.47/enrollments/myenrollments/?canAccess=true&isActive=true` | 200 | JSON | 4 course offerings (Marketing, Math 10H, Social 10‑1IB, Science 10) + the CBE org itself. |
| `GET /d2l/api/le/1.47/content/myItems/due/` (no params) | 400 | problem+json | "Invalid Parameters" — likely requires `orgUnitIdsCSV` and/or `startDateTime`/`endDateTime`. |
| `GET /d2l/api/le/1.47/overdueItems/myItems` | 200 | JSON | `{"Objects":[],"Next":null}` — nothing overdue. |
| `GET /d2l/api/lp/1.47/feed/` | 200 | JSON | Aggregated activity feed. |
| `GET /d2l/api/le/1.47/1527886/content/toc` | 200 | JSON | 96 items, 16 modules, 80 topics. **Used by `schooltools content` / `archive`.** |
| `GET /d2l/api/le/1.47/1527886/content/topics/{topicId}` | 200 | JSON | Topic metadata. |
| `GET /d2l/api/le/1.47/1527886/news/` | 200 | JSON | News items. |
| `GET /d2l/api/le/1.47/1527886/updates/myUpdates` | 200 | JSON | `{"OrgUnitId":"1522695","UserId":"782002","UnreadDiscussions":0,…}` |
| `GET /d2l/api/le/1.47/1527886/grades/values/myGradeValues/` | 200 | JSON | `[]` (no grades released yet). |
| `GET /d2l/api/le/1.47/1527886/dropbox/folders/?onlyCurrentStudentsAndGroups=true` | 200 | JSON | `[]` (no dropbox folders for this student‑view). |
| `GET /d2l/api/le/1.47/1527886/quizzes/` | 200 | JSON | Quiz list (the test course has a "WHMIS and SDS" quiz). |
| `GET /d2l/api/le/1.47/1527886/discussions/forums/` | 200 | JSON | `[]` (no forums yet). |
| `GET /d2l/api/le/1.47/1527886/calendar/events/myEvents/?startDateTime=…&endDateTime=…` | 200 | JSON | Calendar events with explicit date range; without the range it 400s. |
| `GET /d2l/api/le/1.47/1527886/classlist/paged/?onlyShowShownInGrades=true` | 200 | JSON | Visible peers. |
| `GET /d2l/api/lp/1.47/1527886/groupcategories/` | 200 | JSON | `[{ "GroupCategoryId":144423, "Name":"* SIS Managed Groups", … }]`. |
| `GET /d2l/api/lp/1.47/tools/orgUnits/1527886/toolNames` | 200 | JSON | Tool id ↔ display name map (Assignments, Discussions, Announcements, …). |
| `GET /d2l/api/lp/1.47/orgstructure/1527886` | 403 | JSON | "Not Authorized" — students don't have permission to walk the org tree. |
| `GET /d2l/api/lp/1.47/courses/1527886` | 403 | HTML | Course‑offering detail requires a permission the student role doesn't have. |
| `GET /d2l/api/bas/1.6/issued/users/782002/` (not probed but expected 200) | — | — | Awards issued to me. |
| `GET /d2l/api/lr/1.3/repositories/all/` (not probed but version discovered) | — | — | LOR. |
| `GET /d2l/api/ep/2.5/objects/my/` (not probed but version discovered) | — | — | ePortfolio. |
| `POST /d2l/auth/api/token` (no client_id/secret) | 403 | text/plain | "Not authenticated." — the OAuth2 endpoint exists and rejects anonymous calls properly. |

### 3.3 Diagnosis (corrected)

The session cookies authenticate the JSON REST API just fine — **no
Bearer JWT exchange is required for the student CLI use case**. The
"expected JSON, got text/html" failures from `schooltools courses` and
`schooltools content` that I saw on the first probe were a **server‑side
session expiry** (the cookie values were still in `session.json` but the
corresponding D2L session had ended), not a structural auth issue. A
fresh `schooltools login` restored everything.

The OAuth2 `/d2l/auth/api/token` route is for a **different** use case:
third‑party applications that register a Client ID/Secret in the
Brightspace Manage Extensibility tool and use an Authorization Code
Grant flow. A personal CLI tool that uses the user's own session
cookies is the supported alternative path; it works without any
registered app.

### 3.4 What this means for the schooltools CLI

  - **No auth code is broken.** `courses`, `content`, `archive`,
    `download` all work after a fresh login.
  - **Session expiry is the only thing that breaks the CLI today.**
    The `--auto-refresh` flag already re‑runs `login` when the session
    ages out, and the existing 4 cookies are sufficient to keep the
    entire JSON API working.
  - **The hardcoded `LEVersion = "1.47"` in `internal/ua/ua.go:12`
    works** because CBE accepts the older version in the URL (1.47 is
    the *minimum* supported LE version on this tenant). But the tenant
    supports up to `le 1.97` and `lp 1.63`, so a startup version
    discovery call is a worthwhile quality‑of‑life improvement.
  - **One concrete gap** that the docs describe but the existing CLI
    doesn't use: `myItems/due/`, `updates/myUpdates`, and
    `calendar/events/myEvents/` all need **explicit query parameters**
    on this tenant — passing none gets 400. `--no-params` queries need
    at minimum `orgUnitIdsCSV` for `myItems` and a `startDateTime`/
    `endDateTime` window for `calendar/events`.

---

## 4. Endpoint families — by resource

For each family, only the endpoints most likely to be useful from a
student‑facing CLI are listed. Full list (with all verbs) lives in the
official routing table at <https://docs.valence.desire2learn.com/http-routingtable.html>.

Path template shorthand uses these substitutions:

  - `<lp>` = current LP version (e.g. `1.47`)
  - `<le>` = current LE version (e.g. `1.47`)
  - `<v>` = current version of whichever product
  - `<orgId>` = course / org unit id (D2LID)
  - `me` or `<me>` = the calling user's id (often implicit via `whoami`)

### 4.1 Auth & identity (`lp`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/auth/api/token` | (none — token endpoint) | Returns `{ "access_token", "token_type": "Bearer", "expires_in": 3600, "scope": "..." }`. |
| `GET /d2l/api/lp/<lp>/users/whoami` | `users:own_profile:read` | First call from any new CLI session. Returns `Identifier, FirstName, LastName, UniqueName, ProfileIdentifier, Pronouns`. |
| `GET /d2l/api/lp/<lp>/profile/myProfile` | `users:own_profile:read` | Full profile. |
| `GET /d2l/api/lp/<lp>/profile/myProfile/image?size=300` | `users:own_profile:read` | PNG; size is a minimum, returned image is ≥ that on each side. |
| `GET /d2l/api/lp/<lp>/users/mypronouns` / `…/mypronouns/visibility` | `users:own_pronoun:read` | |
| `GET /d2l/api/lp/<lp>/organization/info` | (org public) | Institution name, id, timezone. |

### 4.2 Enrollments & org structure (`lp`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/lp/<lp>/enrollments/myenrollments/?canAccess=true&isActive=true&orgUnitTypeId=3&bookmark=…` | `enrollment:own_enrollment:read` | **Primary course-discovery call** — replaces the broken `manageCourses` endpoint for CLI use. `orgUnitTypeId=3` filters to course offerings. |
| `GET /d2l/api/lp/<lp>/enrollments/myenrollments/<orgId>` | `enrollment:own_enrollment:read` | Single. |
| `GET /d2l/api/lp/<lp>/enrollments/myenrollments/<orgId>/access` | same | Abbreviated `IsActive`/`CanAccess`/`StartDate`/`EndDate`. |
| `GET /d2l/api/lp/<lp>/enrollments/myenrollments/<orgId>/parentOrgUnits/` | same | Walk the org tree upward. |
| `GET /d2l/api/lp/<lp>/courses/<orgId>` | `enrollment:own_enrollment:read` | Course-offering metadata: name, code, dates, description. |
| `GET /d2l/api/lp/<lp>/courses/<orgId>/image?width=&height=` | same | Course banner image. |
| `GET /d2l/api/lp/<lp>/orgstructure/<orgId>` | `organizations:orgstructure:read` | Code, type, path. |
| `GET /d2l/api/lp/<lp>/orgstructure/<orgId>/parents/` / `…/ancestors/` / `…/colours` | same | |
| `GET /d2l/api/lp/<lp>/outypes/` | `organizations:outypes:read` | All org-unit types. |
| `GET /d2l/api/lp/<lp>/roles/` | `role:role:read` | |
| `GET /d2l/api/lp/<lp>/timezones/` / `…/locales/` | (public) | |

### 4.3 Groups & sections (`lp`)

| Route | Required scope |
| --- | --- |
| `GET /d2l/api/lp/<lp>/<orgId>/groupcategories/` | `groups:group:read` |
| `GET /d2l/api/lp/<lp>/<orgId>/groupcategories/<gcId>/groups/<gId>/enrollments` | `groups:group:read` |
| `GET /d2l/api/lp/<lp>/<orgId>/sections/mysections/` | `sections:section:read` |

### 4.4 Course content (`le`)

The most‑trodden family for schooltools today.

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/content/toc` | `content:toc:read` | Full tree. **Already used by `schooltools content` / `archive`.** |
| `GET /d2l/api/le/<le>/<orgId>/content/root/` | `content:modules:readonly` | Top-level modules. |
| `GET /d2l/api/le/<le>/<orgId>/content/modules/<moduleId>` | `content:modules:readonly` | |
| `GET /d2l/api/le/<le>/<orgId>/content/modules/<moduleId>/structure/` | `content:modules:readonly` | Children: sub-modules + topics. `Type 0` = module, `1` = topic. |
| `GET /d2l/api/le/<le>/<orgId>/content/topics/<topicId>` | `content:topics:readonly` | Topic metadata. **Already used by `schooltools download`.** |
| `GET /d2l/api/le/<le>/<orgId>/content/topics/<topicId>/file` | `content:file:read` | **Raw bytes**, not JSON. **Already used by `schooltools download`.** |
| `GET /d2l/api/le/<le>/<orgId>/content/bookmarks` | `content:toc:read` | Bookmarked topics. |
| `GET /d2l/api/le/<le>/<orgId>/content/recent` | `content:toc:read` | |
| `GET /d2l/api/le/<le>/<orgId>/content/completions/mycount/` | `content:completions:read` | |
| `GET /d2l/api/le/<le>/<orgId>/content/userprogress/<topicId>` | `content:completions:read` | |

### 4.5 Due items, calendar, updates, news (`le`)

The "what's due this week" stack.

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/content/myItems/` | `content:my_items:read` | Scheduled items across courses. `?completion=&orgUnitIdsCSV=&startDateTime=&endDateTime=` |
| `GET /d2l/api/le/<le>/content/myItems/due/` | same | Items with upcoming due dates. |
| `GET /d2l/api/le/<le>/content/myItems/itemCounts/` | same | |
| `GET /d2l/api/le/<le>/content/myItems/due/itemCounts/` | same | |
| `GET /d2l/api/le/<le>/overdueItems/myItems` | `content:overdue_items:read` | `?orgUnitIdsCSV=…` |
| `GET /d2l/api/le/<le>/updates/myUpdates/` | `updates:my_updates:read` | `?orgUnitIdsCSV=…` — new-messages / new-grades counts per course. |
| `GET /d2l/api/le/<le>/<orgId>/news/` | `news:news:read` | `?since=…` |
| `GET /d2l/api/le/<le>/<orgId>/news/<newsId>` | same | |
| `GET /d2l/api/le/<le>/<orgId>/news/<newsId>/attachments/<fileId>` | same | Raw bytes. |
| `GET /d2l/api/le/<v>/news/user/<id>/` | same | Cross-course news for one user. |
| `GET /d2l/api/lp/<lp>/feed/` | (aggregate) | `?since=…&until=…` — aggregated activity feed across all tools. |

### 4.6 Calendar (`le`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/calendar/events/myEvents/` | `calendar:my_events:read` | `?association=&eventType=&orgUnitIdsCSV=&startDateTime=&endDateTime=…` |
| `GET /d2l/api/le/<le>/<orgId>/calendar/events/myEvents/` | same | Per-course. |
| `GET /d2l/api/le/<le>/<orgId>/calendar/event/<eventId>` | `calendar:events:read` | |
| `GET /d2l/api/le/<le>/<orgId>/calendar/events/` | `calendar:events:read` | All in course. |
| `GET /d2l/api/le/<le>/<orgId>/calendar/events/orgunits/` | `calendar:events:read` | Bookmark-paginated. |

### 4.7 Grades (`le`)

The "how am I doing in [course]" stack.

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/grades/values/myGradeValues/` | `grades:own_grades:read` | All my grades for one course. |
| `GET /d2l/api/le/<le>/<orgId>/grades/<gradeObjectId>/values/myGradeValue` | same | Single grade item. |
| `GET /d2l/api/le/<le>/<orgId>/grades/final/values/myGradeValue` | same | Course final grade. |
| `GET /d2l/api/le/<le>/grades/final/values/myGradeValues/` | same | `?orgUnitIdsCSV=…` (max 100) — cross-course finals. |
| `GET /d2l/api/le/<le>/<orgId>/grades/` | `grades:gradeobjects:read` | Column definitions (instructor-side metadata). |
| `GET /d2l/api/le/<le>/<orgId>/grades/<gradeObjectId>/statistics` | `grades:gradestatistics:read` | Min/max/avg/median only if the instructor has shared them. |
| `GET /d2l/api/le/<le>/<orgId>/grades/categories/` | `grades:gradecategories:read` | |
| `GET /d2l/api/le/<le>/<orgId>/grades/schemes/` | `grades:gradeschemes:read` | Letter-grade mapping. |
| `GET /d2l/api/le/<le>/<orgId>/grades/setup/` | `grades:gradesettings:read` | Calculation method. |
| `GET /d2l/api/le/<le>/grades/courseCompletion/<userId>/` | `grades:coursecompletion:read` | `?startExpiry=&endExpiry=&bookmark=…` |

### 4.8 Dropbox / assignments (`le`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/?onlyCurrentStudentsAndGroups=true` | `dropbox:folders:read` | |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>` | same | |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/attachments/<fileId>` | same | Instruction-rubric file. |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/submissions/mysubmissions/` | same | **My submissions.** |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/submissions/<submissionId>/files/<fileId>` | same | Download submission file. |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/feedback/<entityType>/<entityId>` | same | Feedback on my submission (`entityType` = `user` or `group`). |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/feedback/<entityType>/<entityId>/attachments/<fileId>` | same | Download feedback file. |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/folders/<folderId>/feedback/<entityType>/<entityId>/links/<linkId>` | same | Media link. |
| `GET /d2l/api/le/<le>/<orgId>/dropbox/categories/` / `…/<categoryId>` | `dropbox:folders:read` | |

### 4.9 Quizzes (`le`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/quizzes/` | `quizzing:quiz:read` | List. |
| `GET /d2l/api/le/<le>/<orgId>/quizzes/<quizId>` | same | One quiz's metadata, dates, attempts allowed, time limit. |
| `GET /d2l/api/le/<le>/<orgId>/quizzes/<quizId>/attempts/` | same | `?userId=me`. |
| `GET /d2l/api/le/<le>/<orgId>/quizzes/<quizId>/attempts/<attemptId>` | same | |
| `GET /d2l/api/le/<le>/<orgId>/quizzes/categories/` / `…/<categoryId>` | same | |

**Students cannot enumerate quiz questions via the API outside an active
attempt** — that route is instructor-only. The `/attempts/<id>/questions/`
set is the only way in.

### 4.10 Discussions (`le`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/discussions/forums/` | `discussions:forums:readonly` | |
| `GET /d2l/api/le/<le>/<orgId>/discussions/forums/<forumId>` | same | |
| `GET /d2l/api/le/<le>/<orgId>/discussions/forums/<forumId>/topics/` | `discussions:topics:readonly` | |
| `GET /d2l/api/le/<le>/<orgId>/discussions/forums/<forumId>/topics/<topicId>` | same | |
| `GET /d2l/api/le/<le>/<orgId>/discussions/forums/<forumId>/topics/<topicId>/posts/?pageSize=20&pageNumber=1` | `discussions:posts:readonly` | **Page-number pagination, not bookmark.** |
| `GET …/posts/<postId>` / `…/ReadStatus` / `…/Flag` / `…/Rating` / `…/Votes` | various | Per-post flags/ratings/votes. |

### 4.11 Surveys, checklists, rubrics (`le`)

| Route | Required scope |
| --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/surveys/` / `…/<surveyId>` | `surveys:survey:read` |
| `GET /d2l/api/le/<le>/<orgId>/surveys/<surveyId>/questions/` | same |
| `GET /d2l/api/le/<le>/<orgId>/surveys/<surveyId>/attempts/` | same |
| `GET /d2l/api/le/<le>/<orgId>/checklists/` / `…/<id>/items/` | `checklists:checklist:read` |
| `GET /d2l/api/le/<le>/<orgId>/rubrics?objectType=…&objectId=…` | `rubrics:rubric:read` |
| `GET /d2l/api/le/<le>/<orgId>/assessment?…&userId=me` | `rubrics:assessment:read` |

### 4.12 Classlist & accommodations (`le`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/le/<le>/<orgId>/classlist/paged/?onlyShowShownInGrades=true` | `enrollment:orgunit:read` | Visible peers. |
| `GET /d2l/api/le/<le>/accommodations/<orgId>/myaccommodations` | `accommodations:profile:read` | "Extra time on quizzes" etc. |

### 4.13 Locker (`le`)

| Route | Notes |
| --- | --- |
| `GET /d2l/api/le/<le>/locker/myLocker/<path>` | Folders return a JSON listing; files return raw bytes. `<path>` is the locker path; root is `/`. |
| `GET /d2l/api/le/<le>/<orgId>/locker/group/<groupId>/<path>` | Group lockers. |

### 4.14 Awards (`bas`)

| Route | Required scope | Notes |
| --- | --- | --- |
| `GET /d2l/api/bas/<v>/associations/availableToEarn/?orgUnitId=…&limit=…&offset=…` | `awards:association:read` | |
| `GET /d2l/api/bas/<v>/issued/users/<me>/` | `awards:issued:read` | Awards issued to me. |
| `GET /d2l/api/bas/<v>/issued/certificates/<certificateId>` | `awards:certificate:read` | |
| `GET /d2l/api/bas/<v>/issued/certificates/<issuedId>/pdf` | same | **PDF stream.** |
| `GET /d2l/api/bas/<v>/creditSummary` | same | |
| `GET /d2l/api/bas/<v>/awards/` / `…/<awardId>` | `awards:awards:read` | |

### 4.15 ePortfolio (`eP`)

| Route | Notes |
| --- | --- |
| `GET /d2l/api/eP/<v>/objects/my/` | My portfolio objects. Bookmark pagination. |
| `GET /d2l/api/eP/<v>/objects/shared/` | Objects shared with me. |
| `GET /d2l/api/eP/<v>/object/<objectId>` / `…/content` / `…/associations/` / `…/shares/` / `…/tags/` / `…/comments/` | Per-object. `content` is the file. |
| `GET /d2l/api/eP/<v>/collection/<id>` / `…/contents/` | Collections. |
| `GET /d2l/api/eP/<v>/artifact/<id>` / `…/file/<id>` / `…/link/<id>` | Artifact file/link. |
| `GET /d2l/api/eP/<v>/reflection/<id>` | Reflection. |
| `GET /d2l/api/eP/<v>/presentation/<id>` | Presentation. |
| `GET /d2l/api/eP/<v>/activity/my/` / `…/shared/` | Activity streams. |
| `GET /d2l/api/eP/<v>/dashboard/` / `…/newsfeed/` | Dashboards. |

### 4.16 LOR / LTI / Widgets / CPD

| Route family | Path root |
| --- | --- |
| Learning Repository (SCORM) | `/d2l/api/lr/<v>/repositories/all/`, `…/objects/search/`, `…/objects/<id>/properties/`, `…/objects/<id>/link/`, `…/objects/<id>/download/`, `…/objects/<id>/downloadfile/` |
| LTI 1.x | `/d2l/api/le/<le>/lti/link/<orgId>/` / `…/<linkId>` |
| LTI 1.3 | `/d2l/api/le/<le>/ltiadvantage/links/orgunit/<orgId>/` / `…/<linkId>` |
| Custom widget data | `/d2l/api/lp/<lp>/<orgId>/widgetdata/<customWidgetId>/mydata` |
| Tool inventory | `/d2l/api/lp/<lp>/tools/orgUnits/<orgId>/toolNames` |
| CPD records (mostly teachers/admins) | `/d2l/api/le/<le>/cpd/record/user/<userId>`, `…/cpd/record/<id>`, `…/cpd/target/progress/user/<userId>` |

### 4.17 Admin / framework (not student-useful)

Out of scope for a student tool but listed for completeness because they
do exist on every tenant:

  - `datahub:*` — Data Export framework, scheduling jobs.
  - `datasets:*` — BDS / Brightspace Data Sets extracts.
  - `intelligentagents:*` — IA triggers and categories.
  - `ipsis:*` — SIS-integration config.
  - `organizations:config:*` — system configuration variables.
  - `permissions:*` — permission descriptions.
  - `datahub:dataexports:*` — Data Export admin.
  - `import:job:*` — IMS / Common Cartridge imports.

---

## 5. Route map for `schooltools`

A condensed, **CLI-relevant** subset of every route above, with the
existing command that already uses it (or could grow into one):

| Capability | Routes used | `schooltools` command today | Status |
| --- | --- | --- | --- |
| Verify session | `GET /d2l/home` | `whoami` | works (HTML) — also returns your real name now |
| List courses | `GET /d2l/le/manageCourses/api/mycourses` (LE widget) | `courses` | works; 4 courses returned |
| Show TOC | `GET /d2l/api/le/<le>/<orgId>/content/toc` | `content` | works; 96 items in 1527886 |
| Download topic file | `GET /d2l/api/le/<le>/<orgId>/content/topics/<topicId>/file` | `download` | works |
| Archive diff | TOC + per-topic metadata | `archive`, `prune` | works; no archive yet — run `archive` to seed |
| **What's due** | `/d2l/api/le/<le>/content/myItems/due/?orgUnitIdsCSV=…&startDateTime=…&endDateTime=…`, `/d2l/api/le/<le>/overdueItems/myItems`, `/d2l/api/le/<le>/calendar/events/myEvents/?…` | — | not yet implemented; endpoint exists, requires date window |
| **Grades** | `/d2l/api/le/<le>/<orgId>/grades/values/myGradeValues/`, `/d2l/api/le/<le>/<orgId>/grades/final/values/myGradeValue`, `/d2l/api/le/<le>/<orgId>/grades/<id>/statistics` | — | not yet implemented; endpoint works |
| **Dropbox** | `/d2l/api/le/<le>/<orgId>/dropbox/folders/`, `…/submissions/mysubmissions/`, `…/feedback/...` | — | not yet implemented; endpoint works |
| **Quizzes** | `/d2l/api/le/<le>/<orgId>/quizzes/`, `…/attempts/` | — | not yet implemented; endpoint works |
| **Discussions** | `/d2l/api/le/<le>/<orgId>/discussions/forums/`, `…/topics/<id>/posts/?pageNumber=1&pageSize=20` | — | not yet implemented (note: page-number pagination, not bookmark) |
| **News** | `/d2l/api/le/<le>/<orgId>/news/`, `/d2l/api/lp/<lp>/feed/` | — | not yet implemented; endpoint works |
| **Updates counts** | `/d2l/api/le/<le>/<orgId>/updates/myUpdates` | — | not yet implemented; endpoint works |
| **Awards** | `/d2l/api/bas/<v>/issued/users/<me>/`, `…/certificates/<id>/pdf` | — | not yet implemented; product version `bas 1.6` available |
| **ePortfolio** | `/d2l/api/eP/<v>/objects/my/`, `…/collection/<id>/contents/`, `…/artifact/<id>` | — | not yet implemented; product version `ep 2.5` available |
| **Locker** | `/d2l/api/le/<le>/locker/myLocker/<path>` | — | not yet implemented; endpoint works (empty) |

---

## 6. Gap analysis: schooltools today vs the full API

What the existing CLI does well:

  - **Auth state machine** — ADFS SAML form-post, KMSI handling, cookie
    persistence, expiry-aware `--auto-refresh`. Solid. Lives in
    `internal/login/` and `internal/session/`.
  - **Course discovery** — `manageCourses` (LE widget API) returns the
    4 courses for this user.
  - **TOC walk** — module/topic tree traversal with `LastModifiedDate`
    diffing.
  - **Archive blob store** — append-only UUID-named blobs with a version
    history. Sound pattern.
  - **Topic metadata + file download** — `content/topics/{id}` + `…/file`
    round-trip; for File topics the raw bytes are saved with the
    server-reported filename, for Link topics the metadata JSON is
    written instead.

Confirmed working **with a fresh session** (post-`schooltools login`):

  - `schooltools whoami` → ✓ (lands on `/d2l/home`).
  - `schooltools courses` → 4 courses listed.
  - `schooltools content 1527886` → 96 items, 16 modules, 80 topics.
  - `schooltools archive --list` → no errors.

What is *missing* relative to the full API surface (not "broken" — just
not yet implemented):

1. **Startup version discovery.** `internal/ua/ua.go:12` hardcodes
   `LEVersion = "1.47"`. CBE actually supports `le 1.97` and `lp 1.63`
   today. The CLI's hardcoded 1.47 works because 1.47 is the *minimum*
   supported LE version, but using the latest gives access to any new
   fields/endpoints that were added in 1.48–1.97. A one‑line `GET
   /d2l/api/<product>/versions/` on startup, pick the highest, persist
   to the session file.

2. **Bookmark pagination helper.** Current commands don't paginate
   (manageCourses returns one ~20‑course page, content/toc returns one
   tree). New list endpoints — `myenrollments`, `myItems/due`,
   `updates/myUpdates`, news, awards, ePortfolio objects — all need
   bookmark walking.

3. **Page‑number pagination helper.** Discussion posts use
   `?pageSize=&pageNumber=` instead of bookmark.

4. **Datetime helpers.** None of the existing commands deal with UTC ISO
   8601 datetimes on the wire. `myItems/due`, `calendar/events/myEvents`,
   `feed` all return them.

5. **Required-parameter handling.** Confirmed via probe: `myItems/due/`
   and `calendar/events/myEvents/` 400 on this tenant when called with
   no query string. The current CLI has no real "calendar" or "due"
   commands, so this is future work, but worth noting: these endpoints
   need `orgUnitIdsCSV` (capped at 100) and/or an explicit
   `startDateTime`/`endDateTime` window.

6. **Org-structure and course-offering detail calls return 403 for
   students.** `/d2l/api/lp/<lp>/orgstructure/{id}` and `…/courses/{id}`
   are not accessible to a student-role context. The course detail
   `schooltools content {orgId}` covers the same information for the
   parts a student actually needs.

7. **File‑stream vs JSON branching.** `download` already handles this
   for topic files; the pattern generalises for dropbox submission
   files, feedback attachments, certificate PDFs, and locker files.

8. **`myProfile` is 403 even for the calling user.** Worth knowing —
   the `whoami` endpoint works (`/d2l/api/lp/<lp>/users/whoami`) but the
   extended `/d2l/api/lp/<lp>/profile/myProfile` is forbidden on this
   tenant for student context, presumably a permission-scope the
   student role doesn't have. CLI code that needs profile details
   should call `whoami`, not `myProfile`.

---

## 7. Recommended next steps for the CLI

The auth path is **not** the priority — the existing cookie flow works.
The next-step list is now just "add features that the API supports and
the user wants." In priority order, smallest‑change → biggest‑surface‑area:

1. **Startup version discovery.** Replace the hardcoded `LEVersion =
   "1.47"` in `internal/ua/ua.go:12` with a call to
   `GET /d2l/api/<product>/versions/` on first use, parse the
   `LatestVersion`, and persist it. ~30 lines + a small migration on
   the session file. Unlocks every new endpoint field added in `le
   1.48–1.97`.

2. **Swap `courses` from `manageCourses` to `enrollments/myenrollments`.**
   The latter is the official Valence route, returns bookmark‑paginated
   paged results, and lets the CLI include OrgUnit type/parent path.
   Same UX, but portable to any D2L tenant (manageCourses is CBE‑only
   via the LE widget).
   `GET /d2l/api/lp/<lp>/enrollments/myenrollments/?canAccess=true&isActive=true&orgUnitTypeId=3`.

3. **`due`** subcommand. The headline student feature.
   `content/myItems/due/?orgUnitIdsCSV=…&startDateTime=…&endDateTime=…` +
   `overdueItems/myItems` + `calendar/events/myEvents/?…`. Note the
   `orgUnitIdsCSV` and date window are required on CBE (probe confirmed).

4. **`grades <orgId>`** subcommand. `values/myGradeValues/` +
   `final/values/myGradeValue` for one course; `final/values/myGradeValues/`
   with `orgUnitIdsCSV` for a cross‑course summary.

5. **`news`** subcommand. `/d2l/api/le/<le>/<orgId>/news/` +
   `/d2l/api/lp/<lp>/feed/`. Easy.

6. **`dropbox`** subcommand. Folders, my submissions, feedback.
   The submissions and feedback endpoints have a `…/files/<fileId>` raw
   byte route that fits the existing `download` pattern.

7. **`quizzes`** subcommand. Quiz list + my attempts (no question
   bodies — those are instructor‑only outside an active attempt).

8. **`discussions`** subcommand. Forums → topics → posts
   (`?pageSize=&pageNumber=`, **not** bookmark — different helper from
   other list endpoints).

9. **`awards`** subcommand. `/d2l/api/bas/<v>/issued/users/<me>/` +
   `…/certificates/<id>/pdf` for raw‑byte download.

10. **`eportfolio`** subcommand. Object browser + download.

11. **`locker`** subcommand. `/d2l/api/le/<le>/locker/myLocker/<path>`.

12. **Bookmark/page‑number/datetime helpers** in a new
    `internal/d2lrest/` package that every new command above uses.
    Centralises the pagination and timestamp conventions so each
    command just declares its shape and gets the helper for free.

Each one is a single new command following the existing pattern in
`cmd/*.go`, backed by an internal package in `internal/<area>/` that
defines the JSON shapes and URL builders — exactly the way the existing
`internal/content/content.go` already models it.

---

## 8. Source links

  - Official Valence reference (entry point): <https://docs.valence.desire2learn.com/reference.html>
  - Routing table: <https://docs.valence.desire2learn.com/http-routingtable.html>
  - Scopes table: <https://docs.valence.desire2learn.com/http-scopestable.html>
  - First steps with the Brightspace APIs: <https://docs.valence.desire2learn.com/basic/firstlist.html>
  - OAuth2 topic (currently 404'd — D2L appears to have moved it; follow
    "Authenticate with your LMS" from firstlist.html): <https://docs.valence.desire2learn.com/basic/firstlist.html#authenticate-with-your-lms>
  - Community-maintained student CLI reference: <https://github.com/Aaryan-Kapoor/d2l-cli/blob/main/D2L_API_REFERENCE.md>
  - Local: `~/.config/schooltools/session.json` (the auth state used for
    the probes).