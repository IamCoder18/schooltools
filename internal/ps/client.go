package ps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/aarav/schooltools/internal/httpclient"
)

// Client is the PowerSchool API client. It owns a cookie jar (set up
// by Login / EnsureSession) and applies the Origin header that the
// portal requires on POSTs.
type Client struct {
	Jar  *cookiejar.Jar
	Base string // usually ps.BaseURL

	// reauth, when set, is invoked once when the portal answers 401 or
	// 403 with a session that looked locally valid. PowerSchool issues
	// psaid and JSESSIONID without an Expires, so a saved session never
	// fails the local expiry check; only the server knows it is dead.
	// Reauthenticating here is what keeps a revoked session from
	// surfacing as a permanent opaque "HTTP 403" on every command.
	reauth func() (*cookiejar.Jar, error)

	mu sync.Mutex

	// refreshMu collapses a concurrent burst of 401/403 into a single
	// login. `sis assignments` fans out across workers, and without this
	// an expired session would trigger one full re-login per in-flight
	// request — a self-inflicted stampede on the very portal that just
	// rejected us.
	refreshMu  sync.Mutex
	refreshed  bool
	refreshJar *cookiejar.Jar
	refreshErr error
}

// NewClient wraps an existing jar. The base is fixed to BaseURL by
// default; tests can override.
func NewClient(jar *cookiejar.Jar) *Client {
	return &Client{Jar: jar, Base: BaseURL}
}

// SetReauth installs the hook used to recover from a server-side
// session expiry. It is safe to call concurrently with in-flight
// requests.
func (c *Client) SetReauth(fn func() (*cookiejar.Jar, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reauth = fn
}

func (c *Client) reauthFn() func() (*cookiejar.Jar, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reauth
}

// refreshOnce re-authenticates at most once for the lifetime of the
// Client. Concurrent callers that hit a 401/403 while a refresh is
// already in progress wait for that refresh and reuse its result rather
// than each starting their own login. The returned bool is false when no
// reauth hook is installed.
func (c *Client) refreshOnce() (*cookiejar.Jar, error, bool) {
	if c.reauthFn() == nil {
		return nil, nil, false
	}

	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if c.refreshed {
		return c.refreshJar, c.refreshErr, true
	}
	c.refreshed = true
	jar, err := c.reauthFn()()
	c.refreshJar, c.refreshErr = jar, err
	return jar, err, true
}

// Get issues an authenticated GET and decodes the JSON response.
func (c *Client) Get(path string, query url.Values, out any) error {
	u := c.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return c.fetchJSON(http.MethodGet, u, nil, out)
}

// Post issues an authenticated POST with a JSON body and decodes the
// response. Origin is automatically set to the base URL.
//
// Note: the cached ps.PostJSON variant from research-time differs from
// the GET by exactly one header (Origin) — we add it via the
// FetchOptions below. FetchOptions.Headers take precedence over the
// defaults set by browser.Headers(), so this is reliable.
func (c *Client) Post(path string, body any, out any) error {
	u := c.Base + path
	return c.fetchJSON(http.MethodPost, u, body, out)
}

// fetchJSON performs the request and decodes into out. A 401 or 403 with
// a reauth hook installed is retried exactly once against a freshly
// authenticated jar.
//
// For non-2xx responses the error carries the status and a sanitized
// error-code hint. The response body is never embedded: PowerSchool
// error pages can echo the student record or other identifiers, and this
// error text reaches the terminal and the auth log.
func (c *Client) fetchJSON(method, rawURL string, body any, out any) error {
	res, err := c.fetch(method, rawURL, body, jsonAccept, c.Base+"/guardian/grades.html")
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		hint := errorHint(res)
		return fmt.Errorf("%s %s: HTTP %d %s%s", method, redactedURL(rawURL), res.StatusCode, res.Status, hint)
	}
	if out == nil {
		_ = res.Body.Close()
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decode JSON: %w", method, redactedURL(rawURL), err)
	}
	return nil
}

// fetch issues one request and, on a 401/403 with a reauth hook
// installed, retries exactly once against a fresh jar. It is shared by
// the JSON and HTML call paths so both recover from a server-side
// session revocation.
func (c *Client) fetch(method, rawURL string, body any, accept, referer string) (*http.Response, error) {
	res, err := c.do(method, rawURL, body, accept, referer)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		if jar, rerr, ok := c.refreshOnce(); ok {
			_ = res.Body.Close()
			if rerr != nil {
				return nil, fmt.Errorf("%s %s: HTTP %d and re-authentication failed: %w", method, redactedURL(rawURL), res.StatusCode, rerr)
			}
			c.mu.Lock()
			c.Jar = jar
			c.mu.Unlock()
			res, err = c.do(method, rawURL, body, accept, referer)
			if err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}

func (c *Client) do(method, rawURL string, body any, accept, referer string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal POST body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	extra := map[string]string{
		"Accept": accept,
	}
	if method == http.MethodPost {
		extra["Content-Type"] = "application/json;charset=UTF-8"
		extra["Origin"] = c.Base
	}

	c.mu.Lock()
	jar := c.Jar
	c.mu.Unlock()

	res, err := httpclient.Fetch(rawURL, jar, httpclient.FetchOptions{
		Method:  method,
		Body:    bodyString(bodyReader),
		Headers: extra,
		Referer: referer,
	})
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, redactedURL(rawURL), err)
	}
	return res, nil
}

func bodyString(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(b)
}

// errorHint distils a PowerSchool error body into a short, non-identifying
// hint. JSON error envelopes expose a code/message pair; anything else is
// reduced to "<N bytes>" so no response content reaches a log.
func errorHint(res *http.Response) string {
	if res == nil || res.Body == nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(res.Body, buf)
	if n == 0 {
		return ""
	}
	raw := buf[:n]
	var envelope struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		for _, candidate := range []string{envelope.Message, envelope.Msg, envelope.Error} {
			if candidate = strings.TrimSpace(candidate); candidate != "" && isPrintableASCII(candidate) {
				return ": " + truncate(candidate, 120)
			}
		}
		if code := fmt.Sprint(envelope.Code); code != "" && code != "<nil>" && isPrintableASCII(code) {
			return ": " + truncate(code, 40)
		}
	}
	return fmt.Sprintf(" (%d bytes of non-JSON response omitted)", n)
}

// isPrintableASCII rejects control characters and non-ASCII runes so a
// message cannot smuggle terminal escapes into the output.
func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// redactedURL strips the query string from a URL before it is embedded in
// an error message. The PS API carries student DCIDs and section IDs in
// query parameters.
func redactedURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.RawQuery == "" {
		return rawURL
	}
	return u.Scheme + "://" + u.Host + u.Path + "?<redacted>"
}

// GetEnrollments returns the student profile + every active section.
//
// Path: GET /ws/xte/student/{DCID}/enrollments?status=A,I,P&year_id=36
//
// `status` filters enrollment codes: A=active, I=inactive, P=pre-registered.
// CBE uses A and P; we pass the full set.
func (c *Client) GetEnrollments(dcid int64, yearID int) (*Student, error) {
	q := url.Values{}
	q.Set("status", "A,I,P")
	q.Set("year_id", strconv.Itoa(yearID))
	var out Student
	if err := c.Get(fmt.Sprintf("/ws/xte/student/%d/enrollments", dcid), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetYears returns the school-year picker for a student.
//
// Path: GET /ws/xte/standard_grade_section/student/years?student_id={DCID}
func (c *Client) GetYears(dcid int64) ([]Year, error) {
	q := url.Values{}
	q.Set("student_id", strconv.FormatInt(dcid, 10))
	var out []Year
	if err := c.Get("/ws/xte/standard_grade_section/student/years", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStandards returns the standards attached to the given sections.
//
// Path: GET /ws/xte/section/standard?include_comment_only=true&include_nonassignment=true&section_ids={csv}
func (c *Client) GetStandards(sectionDCIDs []int64) ([]Standard, error) {
	q := url.Values{}
	q.Set("include_comment_only", "true")
	q.Set("include_nonassignment", "true")
	ids := make([]string, len(sectionDCIDs))
	for i, s := range sectionDCIDs {
		ids[i] = strconv.FormatInt(s, 10)
	}
	q.Set("section_ids", JoinCSV(ids))
	var out []Standard
	if err := c.Get("/ws/xte/section/standard", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStandardGrades returns one row per (student, section, standard)
// for the given scope. We pass `store_codes` in year_ids' place since
// PowerSchool's SPA does the same.
//
// Path: POST /ws/xte/standard_grade_section
// Body: {"section_ids":[…],"student_ids":[…],"year_ids":["36"]}
func (c *Client) GetStandardGrades(dcid int64, sectionDCIDs []int64, yearID int) ([]StandardGrade, error) {
	body := map[string]any{
		"section_ids": sectionDCIDs,
		"student_ids": []int64{dcid},
		"year_ids":    []string{strconv.Itoa(yearID)},
	}
	var out []StandardGrade
	if err := c.Post("/ws/xte/standard_grade_section", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSchoolCommentLimits returns per-school max-comment-size config.
//
// Path: GET /ws/xte/school/comment
func (c *Client) GetSchoolCommentLimits() ([]SchoolCommentLimit, error) {
	var out []SchoolCommentLimit
	if err := c.Get("/ws/xte/school/comment", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStemAssignment returns the per-cell score for one
// (student, section, standard). May return an empty StemAssignmentsResponse
// if no score has been posted.
//
// Path: GET /guardian/studentSectionStemAssignments.json
//
// Note: `studentId` must be the student's `id` field (e.g. the `id` field from the enrollments response), not
// the `dcid` — this is the only PowerSchool endpoint that uses `id`.
func (c *Client) GetStemAssignment(studentID, sectionDCID, standardID int64) (*StemAssignmentsResponse, error) {
	q := url.Values{}
	q.Set("studentId", strconv.FormatInt(studentID, 10))
	q.Set("sectionDcid", strconv.FormatInt(sectionDCID, 10))
	q.Set("standardId", strconv.FormatInt(standardID, 10))
	var out StemAssignmentsResponse
	if err := c.Get("/guardian/studentSectionStemAssignments.json", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// JoinCSV joins a slice into a comma-separated string. The PS portal
// accepts both `section_ids=1,2,3` and `section_ids[]=1&section_ids[]=2`
// — the CSV form is shorter and matches the live SPA traffic.
func JoinCSV(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
