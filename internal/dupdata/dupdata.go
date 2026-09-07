// Package dupdata provides shared helpers for talking to the D2L/Brightspace
// Valence API: a generic JSON fetch, bookmark-paginated walkers, page-number
// walkers, and the small set of date/time flag resolvers used by the various
// CLI subcommands.
package dupdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aarav/schooltools/internal/d2lver"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

// PagingInfo matches the standard D2L "bookmark" pagination envelope that
// most list endpoints return. Page-number endpoints use different fields and
// are walked by WalkPageNumber instead.
type PagingInfo struct {
	Bookmark       string `json:"Bookmark"`
	HasMoreItems   bool   `json:"HasMoreItems"`
	ItemsRemaining int    `json:"ItemsRemaining"`
}

// Page mirrors the alternative "pageNumber / pageSize / totalNumberOfPages"
// pagination style used by some endpoints (e.g. discussion posts).
type Page struct {
	PageNumber       int `json:"pageNumber"`
	PageSize         int `json:"pageSize"`
	TotalNumberOfPages int `json:"totalNumberOfPages"`
	TotalNumberOfItems int `json:"totalNumberOfItems"`
}

// FetchJSON performs a single GET against rawURL, follows redirects, and
// unmarshals the body into out. The body Content-Type is not enforced — D2L
// returns "application/json" for almost all Valence calls, but the helper
// is tolerant of missing types.
func FetchJSON(rawURL string, jar *cookiejar.Jar, out any) error {
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return apiError(res, rawURL)
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", res.Request.URL, err)
	}
	return nil
}

// apiError builds an error that includes the response body so callers can
// see the D2L error envelope (e.g. `{ "Errors": [ { "Message": "..." } ] }`).
func apiError(res *http.Response, rawURL string) error {
	defer func() { _ = res.Body.Close() }()
	body, _ := httpclient.BodyBytes(res)
	msg := strings.TrimSpace(string(body))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Errorf("D2L API returned %d %s for %s: %s", res.StatusCode, res.Status, rawURL, msg)
}

// FetchJSONList handles D2L endpoints that can return either a raw array
// (`[{...}, ...]`) or the more common wrapped form
// (`{ "Items": [...], "PagingInfo": {...} }`). When the response is a raw
// array, items is populated directly. When it is a wrapped object, the
// `Items` field is extracted (with `Objects` as a fallback because the
// calendar / due / update endpoints use that key).
//
// The function peeks at the first non-whitespace byte to choose; if it's
// `[`, the array form is used; otherwise the wrapped form is tried first
// and the raw array is a fallback.
func FetchJSONList[T any](rawURL string, jar *cookiejar.Jar) (items []T, err error) {
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, apiError(res, rawURL)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return nil, err
	}
	// Peek at first non-whitespace byte to decide the shape.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("decode array from %s: %w", res.Request.URL, err)
		}
		return items, nil
	}
	var wrapped struct {
		Items   []T `json:"Items"`
		Objects []T `json:"Objects"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", res.Request.URL, err)
	}
	if wrapped.Items != nil {
		return wrapped.Items, nil
	}
	return wrapped.Objects, nil
}

// CurrentUserID returns the D2L user id of the logged-in caller. It hits
// `/d2l/api/lp/<lp>/users/whoami` and reads the `Identifier` field. If
// the request fails, the UserID baked into token.json is used as a
// fallback (still correct, just not re-verified).
func CurrentUserID(jar *cookiejar.Jar) (string, error) {
	raw := fmt.Sprintf("%s/d2l/api/lp/%s/users/whoami", ua.D2LBase, LPVersion())
	var resp struct {
		Identifier string `json:"Identifier"`
	}
	if err := FetchJSON(raw, jar, &resp); err == nil && resp.Identifier != "" {
		return resp.Identifier, nil
	}
	// Fall back to the persisted token.
	if tok, err := session.LoadToken(); err == nil && tok != nil && tok.UserID != "" {
		return tok.UserID, nil
	}
	return "", fmt.Errorf("could not determine current user id (whoami failed and no token.json)")
}

// CourseOrgIDsCSV returns a CSV of the user's enrolled course orgUnit ids
// by hitting the manageCourses API. Used as the default scope for
// cross-course queries that require `orgUnitIdsCSV=`. The CBE's calendar,
// due, and update endpoints all reject an empty value, so the caller must
// always pass at least one id.
func CourseOrgIDsCSV(jar *cookiejar.Jar) (string, error) {
	// The manageCourses endpoint is on the LE widget, not the LE API. Reuse
	// the same query string the legacy `courses` command used.
	const url = ua.D2LBase + "/d2l/le/manageCourses/api/mycourses" +
		"?pageSize=20&sort=current&autoPinCourses=false" +
		"&orgUnitTypeId=3&promotePins=true&embedDepth=0&widgetId=287426"
	var resp struct {
		Courses []struct {
			OrgUnitId json.Number `json:"OrgUnitId"`
		} `json:"Courses"`
	}
	if err := FetchJSON(url, jar, &resp); err != nil {
		return "", err
	}
	if len(resp.Courses) == 0 {
		return "", fmt.Errorf("no enrolled courses found; pass --org CSV")
	}
	ids := make([]string, 0, len(resp.Courses))
	for _, c := range resp.Courses {
		ids = append(ids, c.OrgUnitId.String())
	}
	return strings.Join(ids, ","), nil
}

// Walk walks an endpoint that uses bookmark pagination. It repeatedly calls
// fetch with the next bookmark until PagingInfo.HasMoreItems is false (or
// maxPages safety kicks in), accumulating the items.
func Walk[T any](
	jar *cookiejar.Jar,
	firstURL string,
	maxPages int,
) (items []T, bookmarks []string, err error) {
	if maxPages <= 0 {
		maxPages = 50
	}
	next := firstURL
	pages := 0
	for next != "" {
		pages++
		if pages > maxPages {
			return items, bookmarks, fmt.Errorf("pagination: exceeded %d pages (possible infinite loop)", maxPages)
		}
		var resp struct {
			Items     []T     `json:"Items"`
			Paging    PagingInfo `json:"PagingInfo"`
		}
		if err := FetchJSON(next, jar, &resp); err != nil {
			return items, bookmarks, err
		}
		items = append(items, resp.Items...)
		bookmarks = append(bookmarks, resp.Paging.Bookmark)
		if !resp.Paging.HasMoreItems || resp.Paging.Bookmark == "" {
			break
		}
		next, err = AdvanceBookmark(next, resp.Paging.Bookmark)
		if err != nil {
			return items, bookmarks, err
		}
	}
	return items, bookmarks, nil
}

// AdvanceBookmark replaces (or sets) the "bookmark" query parameter on rawURL
// with the supplied value, preserving all other query parameters.
func AdvanceBookmark(rawURL, bookmark string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("bookmark", bookmark)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// WalkPageNumber walks an endpoint that uses page-number pagination. It
// calls fetch for page 1, 2, …, TotalNumberOfPages, decoding each page into
// the supplied decodeFn which must return the page's items and the per-page
// envelope so we know when to stop.
func WalkPageNumber[T any](
	jar *cookiejar.Jar,
	firstURL string,
) (items []T, err error) {
	u, err := url.Parse(firstURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	pageSize := 20
	if v := q.Get("pageSize"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			pageSize = n
		}
	}
	q.Set("pageSize", strconv.Itoa(pageSize))
	q.Set("pageNumber", "1")
	u.RawQuery = q.Encode()
	next := u.String()

	for {
		var resp struct {
			Items        []T   `json:"Items"`
			PageNumber   int   `json:"pageNumber"`
			PageSize     int   `json:"pageSize"`
			TotalNumberOfPages int `json:"totalNumberOfPages"`
		}
		if err := FetchJSON(next, jar, &resp); err != nil {
			return items, err
		}
		items = append(items, resp.Items...)
		if resp.TotalNumberOfPages == 0 || resp.PageNumber >= resp.TotalNumberOfPages {
			break
		}
		q2, _ := url.Parse(next)
		qq := q2.Query()
		qq.Set("pageNumber", strconv.Itoa(resp.PageNumber+1))
		q2.RawQuery = qq.Encode()
		next = q2.String()
	}
	return items, nil
}

// TimeRange holds the resolved --from / --to values for a time-windowed
// command, both as time.Time (for URL formatting).
type TimeRange struct {
	From time.Time
	To   time.Time
}

// ParseTimeRange resolves a --from/--to/--window triplet into a TimeRange.
// All three are optional but at least one of --from/--window must be present
// to bound the query (the API requires a date window).
//
//   - from/to: ISO 8601 date (YYYY-MM-DD) or RFC3339 datetime. Empty → zero.
//   - window: short form like "1d", "7d", "30d", "12h". Sets from=now-window
//     and to=now.
func ParseTimeRange(from, to, window string, now time.Time) (TimeRange, error) {
	tr := TimeRange{}
	if from != "" {
		t, err := parseISO(from)
		if err != nil {
			return tr, fmt.Errorf("--from: %w", err)
		}
		tr.From = t
	}
	if to != "" {
		t, err := parseISO(to)
		if err != nil {
			return tr, fmt.Errorf("--to: %w", err)
		}
		tr.To = t
	}
	if window != "" {
		d, err := parseDuration(window)
		if err != nil {
			return tr, fmt.Errorf("--window: %w", err)
		}
		tr.From = now.Add(-d)
		tr.To = now
	}
	if tr.From.IsZero() && tr.To.IsZero() {
		return tr, fmt.Errorf("at least one of --from, --to, or --window is required")
	}
	return tr, nil
}

func parseISO(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	// Try date-only first.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	// Then RFC3339.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected ISO 8601 date or RFC3339 datetime, got %q", s)
}

// parseDuration accepts "Nd" (days) and "Nh" (hours). The plan calls for
// exactly these two forms; anything else errors.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, fmt.Errorf("expected Nd or Nh, got %q", s)
	}
	unit := s[len(s)-1]
	num := s[:len(s)-1]
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("expected positive integer before %q, got %q", string(unit), s)
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	default:
		return 0, fmt.Errorf("expected 'd' or 'h' suffix, got %q in %q", string(unit), s)
	}
}

// AppendQuery adds or replaces a key=value pair on rawURL.
func AppendQuery(rawURL, key, value string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// D2LVersion returns the LE product version in use. Resolves through
// d2lver.Get first (discovered at session load) and falls back to the
// hardcoded constant if discovery hasn't run.
func D2LVersion() string {
	if v := d2lver.Get(nil); v.LE != "" {
		return v.LE
	}
	return ua.LEAPIVersion
}

// LPVersion returns the LP product version in use.
func LPVersion() string {
	if v := d2lver.Get(nil); v.LP != "" {
		return v.LP
	}
	return ua.LPAPIVersion
}

// BASVersion returns the BAS product version in use.
func BASVersion() string {
	if v := d2lver.Get(nil); v.BAS != "" {
		return v.BAS
	}
	return ua.BASAPIVersion
}
