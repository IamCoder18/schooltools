package ps

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"

	stderrors "github.com/IamCoder18/schooltools/internal/errors"
	"github.com/IamCoder18/schooltools/internal/httpclient"
)

// markTerms are the reporting-term columns on /guardian/grades.html, in
// page order.
var markTerms = []string{"T1", "T2", "S1", "T3", "T4", "S2", "Y1"}

// MarkTerms returns the reporting-term codes in page order, as a copy
// callers may iterate without mutating the package-level table.
func MarkTerms() []string {
	return append([]string(nil), markTerms...)
}

// TermMark is one reporting-term grade for a course. Mark is empty when
// PowerSchool has not posted a grade for that term.
type TermMark struct {
	Term string `json:"term"`
	Mark string `json:"mark"`
}

// CourseMark is one row of the guardian "Assignments & Grades" table:
// the course, its teacher, and one mark per reporting term.
type CourseMark struct {
	Expression string     `json:"expression,omitempty"`
	Course     string     `json:"course"`
	Teacher    string     `json:"teacher,omitempty"`
	Marks      []TermMark `json:"marks"`
}

// MarkFor returns the mark for term, or "" when it is not present.
func (c CourseMark) MarkFor(term string) string {
	for _, m := range c.Marks {
		if m.Term == term {
			return m.Mark
		}
	}
	return ""
}

// GetCourseMarks fetches /guardian/grades.html and returns one row per
// course with a mark for every reporting term. It is the server-rendered
// page behind the portal's "Assignments & Grades" view, so the values
// match what the portal displays.
func (c *Client) GetCourseMarks() ([]CourseMark, error) {
	rawURL := c.Base + "/guardian/grades.html"
	res, err := c.fetch(http.MethodGet, rawURL, nil, htmlAccept, c.Base+"/guardian/home.html")
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if isSigninRedirect(res) {
			_ = res.Body.Close()
			return nil, sessionStaleError(fmt.Sprintf("HTTP %d", res.StatusCode))
		}
		_ = res.Body.Close()
		return nil, statusError(http.MethodGet, rawURL, res)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := httpclient.BodyString(res)
	if err != nil {
		return nil, err
	}
	marks, err := ParseCourseMarks(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(marks) == 0 && !strings.Contains(body, "Assignments") {
		return nil, stderrors.New("PS_SESSION_STALE",
			fmt.Sprintf("PowerSchool returned %d bytes without any course rows; the saved SIS session is likely expired — run 'schooltools sis login'", len(body)))
	}
	return marks, nil
}

// ParseCourseMarks extracts the course-mark rows from the guardian
// grades page. The page is a legacy server-rendered HTML table; rows are
// identified by their ccid_ id and cells are read by position because
// the second cell is a hidden attendance placeholder.
func ParseCourseMarks(r io.Reader) ([]CourseMark, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	var out []CourseMark
	doc.Find("tr[id^='ccid_']").Each(func(_ int, row *goquery.Selection) {
		cells := row.ChildrenFiltered("td")
		if cells.Length() < len(markTerms)+3 {
			return
		}
		course := courseName(cells.Eq(2))
		if course == "" {
			return
		}
		cm := CourseMark{
			Expression: strings.TrimSpace(cells.Eq(0).Text()),
			Course:     course,
			Teacher:    teacherName(cells.Eq(2)),
			Marks:      make([]TermMark, 0, len(markTerms)),
		}
		for i, term := range markTerms {
			cm.Marks = append(cm.Marks, TermMark{
				Term: term,
				Mark: normalizeMark(cells.Eq(3 + i).Text()),
			})
		}
		out = append(out, cm)
	})
	return out, nil
}

// courseName reads the course title from the cell, dropping the teacher
// link, email link, room span, and the <br> that follows the title.
func courseName(sel *goquery.Selection) string {
	clone := sel.Clone()
	clone.Find("a, span, img, em, div, br").Remove()
	return strings.TrimSpace(clone.Text())
}

func teacherName(sel *goquery.Selection) string {
	title, ok := sel.Find("a[title^='Details about']").First().Attr("title")
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(title, "Details about"))
}

func normalizeMark(s string) string {
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	switch s {
	case "", "-", "Not available":
		return ""
	}
	return s
}
