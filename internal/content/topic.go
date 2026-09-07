package content

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"

	"github.com/aarav/schooltools/internal/httpclient"
)

// Topic matches the document returned by
// /d2l/api/le/<ver>/<orgId>/content/topics/<topicId>. We declare only the
// fields the download command needs to make a routing decision; the raw JSON
// body is preserved verbatim by the archive package, so unrecognised fields
// are not lost.
type Topic struct {
	TopicId         int     `json:"TopicId"`
	Title           string  `json:"Title"`
	TypeIdentifier  string  `json:"TypeIdentifier"`
	Url             *string `json:"Url"`
	FileName        string  `json:"FileName"`
	LastModifiedDate string `json:"LastModifiedDate"`
	IsHidden        bool    `json:"IsHidden"`
	IsLocked        bool    `json:"IsLocked"`
	IsBroken        bool    `json:"IsBroken"`
	ActivityId      string  `json:"ActivityId"`
	TopicType       int     `json:"TopicType"`
}

// IsFile reports whether the topic's underlying document is a downloadable
// file (as opposed to a Link, a hosted HTML page, etc.).
func (t Topic) IsFile() bool {
	if strings.EqualFold(t.TypeIdentifier, "File") {
		return true
	}
	if t.TypeIdentifier == "" && t.TopicType == 1 {
		return true
	}
	return false
}

// IsLink reports whether the topic is just a clickable link to an external or
// internal resource. Downloading a link target is out of scope; the caller is
// expected to save the topic metadata JSON instead.
func (t Topic) IsLink() bool {
	if strings.EqualFold(t.TypeIdentifier, "Link") {
		return true
	}
	if t.TypeIdentifier == "" && t.TopicType == 2 {
		return true
	}
	return false
}

// FileURL returns the absolute download URL for the topic's underlying file
// (empty string for non-file topics).
func (t Topic) FileURL() string {
	if !t.IsFile() || t.Url == nil {
		return ""
	}
	return *t.Url
}

// FetchTopic retrieves a single topic's metadata document.
func FetchTopic(orgUnitID string, topicID int, jar *cookiejar.Jar) (Topic, error) {
	res, err := httpclient.FollowRedirects(TopicURL(orgUnitID, topicID), jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return Topic{}, err
	}
	if res.StatusCode != http.StatusOK {
		return Topic{}, fmt.Errorf("D2L API returned %d %s for %s", res.StatusCode, res.Status, res.Request.URL)
	}
	var t Topic
	if err := json.NewDecoder(res.Body).Decode(&t); err != nil {
		return Topic{}, err
	}
	return t, nil
}

// ParseTopicRef accepts either:
//   - "<courseId>/topics/<topicId>" (D2L web URL fragment)
//   - "<courseId>/<topicId>"        (shortcut)
//   - "<courseId> topics <topicId>" (whitespace form, lenient)
//
// and splits it into the two integer identifiers D2L expects.
func ParseTopicRef(ref string) (courseID string, topicID int, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", 0, fmt.Errorf("topic reference is empty")
	}
	parts := strings.FieldsFunc(ref, func(r rune) bool {
		return r == '/' || r == ' ' || r == '\t'
	})
	if len(parts) != 2 && len(parts) != 3 {
		return "", 0, fmt.Errorf("expected <courseId>/topics/<topicId>, got %q", ref)
	}
	courseID = strings.TrimSpace(parts[0])
	if courseID == "" {
		return "", 0, fmt.Errorf("missing course id in %q", ref)
	}
	if _, cerr := strconv.Atoi(courseID); cerr != nil {
		return "", 0, fmt.Errorf("invalid course id %q in %q", courseID, ref)
	}
	var topicIDRaw string
	if len(parts) == 3 {
		marker := strings.ToLower(strings.TrimSpace(parts[1]))
		if marker != "topics" && marker != "topic" {
			return "", 0, fmt.Errorf("expected 'topics' between course and topic ids, got %q", marker)
		}
		topicIDRaw = strings.TrimSpace(parts[2])
	} else {
		topicIDRaw = strings.TrimSpace(parts[1])
	}
	id, perr := strconv.Atoi(topicIDRaw)
	if perr != nil || id <= 0 {
		return "", 0, fmt.Errorf("invalid topic id %q in %q", topicIDRaw, ref)
	}
	return courseID, id, nil
}
