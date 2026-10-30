package progress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"

	"github.com/IamCoder18/schooltools/internal/httpclient"
	"github.com/IamCoder18/schooltools/internal/ua"
)

// UserProgress mirrors D2L's UserProgressData: the per user × topic record
// behind Content Statistics and Class Progress. TotalTime is seconds.
type UserProgress struct {
	ObjectId      int     `json:"ObjectId"`
	UserId        int     `json:"UserId"`
	CompletedDate *string `json:"CompletedDate"`
	IsRead        bool    `json:"IsRead"`
	NumVisits     int     `json:"NumVisits"`
	TotalTime     int     `json:"TotalTime"`
	LastVisited   *string `json:"LastVisited"`
	Visited       bool    `json:"Visited"`
	Completed     bool    `json:"Completed"`
	IsExempt      bool    `json:"IsExempt"`
}

// updateBody is the write payload for POST …/content/userprogress/. Every
// value field is required (omitting one is a 400) and IsExempt must be
// absent because it is output-only.
type updateBody struct {
	ObjectId      int     `json:"ObjectId"`
	UserId        int     `json:"UserId"`
	CompletedDate *string `json:"CompletedDate"`
	IsRead        bool    `json:"IsRead"`
	NumVisits     int     `json:"NumVisits"`
	TotalTime     int     `json:"TotalTime"`
	LastVisited   string  `json:"LastVisited"`
	Visited       bool    `json:"Visited"`
	Completed     bool    `json:"Completed"`
}

// APIError is a non-200 answer from a D2L route, with whatever message the
// body carried. Error shapes differ by route ({"Errors":[{"Message":…}]} vs
// Valence problem+json), so Message falls back to the raw body.
type APIError struct {
	StatusCode int
	Message    string
	URL        string
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("D2L API returned %d for %s: %s", e.StatusCode, e.URL, e.Message)
}

// Client performs userprogress reads and writes on behalf of one user.
// It attaches the CSRF headers the tenant eventually starts requiring and
// re-scrapes the token once when a call comes back 403.
type Client struct {
	Jar    *cookiejar.Jar
	UserID int
	Base   string
	Now    func() time.Time

	xsrf string
}

// NewClient builds a Client for the given user. Base defaults to the real
// tenant; tests override it with an httptest URL.
func NewClient(jar *cookiejar.Jar, userID int) *Client {
	return &Client{Jar: jar, UserID: userID, Base: ua.D2LBase, Now: time.Now}
}

// UserProgressURL builds the per-topic read route. The version slot is the
// literal "unstable": numeric LE versions 404 on this route.
func UserProgressURL(base, orgUnitID string, topicID, userID int) string {
	return fmt.Sprintf("%s/d2l/api/le/unstable/%s/content/userprogress/%d?userId=%d", base, orgUnitID, topicID, userID)
}

// UpdateUserProgressURL builds the write route (POST).
func UpdateUserProgressURL(base, orgUnitID string) string {
	return fmt.Sprintf("%s/d2l/api/le/unstable/%s/content/userprogress/", base, orgUnitID)
}

// Fetch reads the caller's progress record for one topic. Never-recorded
// topics come back as a default-state row, which makes this the right
// pre-write snapshot.
func (c *Client) Fetch(orgUnitID string, topicID int) (UserProgress, error) {
	url := UserProgressURL(c.Base, orgUnitID, topicID, c.UserID)
	body, err := c.call(http.MethodGet, url, "")
	if err != nil {
		return UserProgress{}, err
	}
	var p UserProgress
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return UserProgress{}, fmt.Errorf("parse userprogress for topic %d: %w", topicID, err)
	}
	return p, nil
}

// Update writes the record and returns the server's echo. The server
// forces LastVisited to its own clock and clamps TotalTime to
// min(requested, current + (now - LastVisited)), so the echo is the only
// reliable statement of the effective values.
func (c *Client) Update(orgUnitID string, p UserProgress) (UserProgress, error) {
	payload := updateBody{
		ObjectId:      p.ObjectId,
		UserId:        p.UserId,
		CompletedDate: p.CompletedDate,
		IsRead:        p.IsRead,
		NumVisits:     p.NumVisits,
		TotalTime:     p.TotalTime,
		LastVisited:   c.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Visited:       p.Visited,
		Completed:     p.Completed,
	}
	if payload.UserId == 0 {
		payload.UserId = c.UserID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return UserProgress{}, err
	}
	url := UpdateUserProgressURL(c.Base, orgUnitID)
	body, err := c.call(http.MethodPost, url, string(raw))
	if err != nil {
		return UserProgress{}, err
	}
	var echo UserProgress
	if err := json.Unmarshal([]byte(body), &echo); err != nil {
		return UserProgress{}, fmt.Errorf("parse userprogress echo for topic %d: %w", p.ObjectId, err)
	}
	return echo, nil
}

// CreditTime records want seconds of viewing time on a topic, following the
// idle-gap credit rule: each write can credit at most the wall-clock gap
// since the record's LastVisited, and a never-visited topic credits 0 on
// its first write. When wait is true the remaining time is slept out and
// re-posted until fully credited; otherwise the first write's clamp wins
// and the shortfall is reported.
func (c *Client) CreditTime(orgUnitID string, cur UserProgress, want int, wait bool, sleep func(time.Duration)) (CreditResult, error) {
	res := CreditResult{Requested: want, Before: cur}
	if want <= 0 {
		res.After = cur
		return res, nil
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	target := cur.TotalTime + want
	attempts := 0
	for {
		attempts++
		req := cur
		req.TotalTime = target
		echo, err := c.Update(orgUnitID, req)
		if err != nil {
			return res, err
		}
		res.After = echo
		res.Credited = echo.TotalTime - res.Before.TotalTime
		remaining := want - res.Credited
		if remaining <= 0 || !wait || attempts >= 8 {
			return res, nil
		}
		sleep(time.Duration(remaining)*time.Second + 500*time.Millisecond)
		cur = echo
		target = echo.TotalTime + remaining
	}
}

// CreditResult reports what a CreditTime call actually achieved.
type CreditResult struct {
	Requested int          `json:"requested"`
	Credited  int          `json:"credited"`
	Before    UserProgress `json:"before"`
	After     UserProgress `json:"after"`
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) call(method, url, body string) (string, error) {
	send := func(token string) (int, string, error) {
		headers := map[string]string{
			"Accept":             "application/json",
			"X-Requested-With":   "XMLHttpRequest",
			"X-D2L-Content-Type": "application/json",
		}
		if token != "" {
			headers["X-Csrf-Token"] = token
		}
		if body != "" {
			headers["Content-Type"] = "application/json"
		}
		res, err := httpclient.Fetch(url, c.Jar, httpclient.FetchOptions{
			Method:  method,
			Headers: headers,
			Body:    body,
		})
		if err != nil {
			return 0, "", err
		}
		text, err := httpclient.BodyString(res)
		if err != nil {
			return res.StatusCode, "", err
		}
		return res.StatusCode, text, nil
	}

	token, terr := c.token()
	code, text, err := send(token)
	if err != nil {
		return "", err
	}
	if code == http.StatusForbidden && terr == nil {
		c.xsrf = ""
		if token, terr = c.token(); terr == nil {
			code, text, err = send(token)
			if err != nil {
				return "", err
			}
		}
	}
	if code != http.StatusOK {
		return "", &APIError{StatusCode: code, Message: parseAPIErrorMessage(text), URL: url}
	}
	return text, nil
}

func (c *Client) token() (string, error) {
	if c.xsrf != "" {
		return c.xsrf, nil
	}
	home := strings.TrimSuffix(c.Base, "/") + "/d2l/home"
	t, err := httpclient.FetchXSRFTokenFrom(c.Jar, home)
	if err != nil {
		return "", err
	}
	c.xsrf = t
	return t, nil
}

func parseAPIErrorMessage(body string) string {
	var wrapped struct {
		Errors []struct {
			Message string `json:"Message"`
		} `json:"Errors"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &wrapped); err == nil {
		if len(wrapped.Errors) > 0 && wrapped.Errors[0].Message != "" {
			return wrapped.Errors[0].Message
		}
		if wrapped.Detail != "" {
			return wrapped.Detail
		}
		if wrapped.Title != "" {
			return wrapped.Title
		}
	}
	compact := strings.Join(strings.Fields(body), " ")
	if len(compact) > 200 {
		compact = compact[:200] + "…"
	}
	return compact
}

// ParseUserID converts a whoami Identifier into the integer UserId the
// userprogress body requires.
func ParseUserID(identifier string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(identifier))
	if err != nil {
		return 0, fmt.Errorf("parse user id %q: %w", identifier, err)
	}
	return id, nil
}
