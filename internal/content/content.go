package content

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ua"
)

// LEVersion is the LE product version to talk to. Resolved through
// dupdata.D2LVersion() at call time so the package picks up the
// discovered version (or falls back to ua.LEAPIVersion if discovery
// hasn't run yet).
func LEVersion() string {
	return dupdata.D2LVersion()
}

// TocModule matches the D2L /d2l/api/le/<ver>/<orgId>/content/toc response.
type TocModule struct {
	ModuleId        int          `json:"ModuleId"`
	Title           string       `json:"Title"`
	IsHidden        bool         `json:"IsHidden"`
	IsLocked        bool         `json:"IsLocked"`
	LastModifiedDate string      `json:"LastModifiedDate"`
	Modules         []TocModule  `json:"Modules"`
	Topics          []TocTopic   `json:"Topics"`
}

// TocTopic matches each entry under Modules.Topics.
type TocTopic struct {
	TopicId         int    `json:"TopicId"`
	Title           string `json:"Title"`
	TypeIdentifier  string `json:"TypeIdentifier"`
	Url             *string `json:"Url"`
	LastModifiedDate string `json:"LastModifiedDate"`
	IsHidden        bool   `json:"IsHidden"`
	IsLocked        bool   `json:"IsLocked"`
	IsBroken        bool   `json:"IsBroken"`
	ActivityId      string `json:"ActivityId"`
	TopicType       int    `json:"TopicType"`
}

// FlatRow is the depth-first representation of a TOC: modules followed by their
// topics, recursively. JSON output preserves these field names so downstream
// scripts keep working.
type FlatRow struct {
	Kind         string `json:"kind"`
	Depth        int    `json:"depth"`
	Id           int    `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	LastModified string `json:"lastModified"`
	IsHidden     bool   `json:"isHidden"`
	IsLocked     bool   `json:"isLocked"`
	IsBroken     bool   `json:"isBroken"`
}

func topicType(t TocTopic) string {
	if t.TypeIdentifier != "" {
		return t.TypeIdentifier
	}
	if t.TopicType == 2 {
		return "Link"
	}
	return "File"
}

func topicURL(t TocTopic) string {
	if t.Url == nil {
		return ""
	}
	return *t.Url
}

// FlattenToc produces a depth-first list of rows. Mirrors the TS version
// (modules → topics → nested modules). Empty input → empty output.
func FlattenToc(modules []TocModule) []FlatRow {
	return flattenToc(modules, 0, nil)
}

func flattenToc(modules []TocModule, depth int, out []FlatRow) []FlatRow {
	for _, m := range modules {
		out = append(out, FlatRow{
			Kind:         "MODULE",
			Depth:        depth,
			Id:           m.ModuleId,
			Title:        m.Title,
			Type:         "Module",
			LastModified: m.LastModifiedDate,
			IsHidden:     m.IsHidden,
			IsLocked:     m.IsLocked,
			IsBroken:     false,
		})
		for _, t := range m.Topics {
			out = append(out, FlatRow{
				Kind:         "TOPIC",
				Depth:        depth + 1,
				Id:           t.TopicId,
				Title:        t.Title,
				Type:         topicType(t),
				URL:          topicURL(t),
				LastModified: t.LastModifiedDate,
				IsHidden:     t.IsHidden,
				IsLocked:     t.IsLocked,
				IsBroken:     t.IsBroken,
			})
		}
		if len(m.Modules) > 0 {
			out = flattenToc(m.Modules, depth+1, out)
		}
	}
	return out
}

// TocResponse mirrors the top-level JSON: { "Modules": [...] }
type TocResponse struct {
	Modules []TocModule `json:"Modules"`
}

func TocURL(orgUnitID string) string {
	return fmt.Sprintf("%s/d2l/api/le/%s/%s/content/toc", ua.D2LBase, LEVersion(), orgUnitID)
}

func TopicURL(orgUnitID string, topicID int) string {
	return fmt.Sprintf("%s/d2l/api/le/%s/%s/content/topics/%d", ua.D2LBase, LEVersion(), orgUnitID, topicID)
}

// FetchToc fetches the TOC JSON for a course using the given cookie jar.
func FetchToc(orgUnitID string, jar *cookiejar.Jar) (TocResponse, error) {
	res, err := httpclient.FollowRedirects(TocURL(orgUnitID), jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return TocResponse{}, err
	}
	if res.StatusCode != http.StatusOK {
		return TocResponse{}, fmt.Errorf("D2L API returned %d %s for %s", res.StatusCode, res.Status, res.Request.URL)
	}
	var toc TocResponse
	if err := json.NewDecoder(res.Body).Decode(&toc); err != nil {
		return TocResponse{}, err
	}
	return toc, nil
}