package archive

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aarav/schooltools/internal/content"
)

func TestRunSurvivesBrokenAndFailingTopics(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("4242"), Name: "Marketing", Code: "MKT", IsActive: true}}
	goodURL := "/content/enforced/4242-MKT/ok.pdf"
	f.tocs["4242"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 6001, Title: "Fine", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &goodURL},
		{TopicId: 6002, Title: "Dead dropbox link", LastModifiedDate: "2022-03-15T16:25:36Z", IsBroken: true},
		{TopicId: 6003, Title: "Vanished", LastModifiedDate: "2026-03-02T00:00:00Z"},
	}}}}
	f.topics[6001] = map[string]any{"TopicId": 6001, "Title": "Fine", "TypeIdentifier": "File", "Url": goodURL}
	// 6002 is flagged broken and 6003 is never registered, so the topics API 404s it.

	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()

	res, err := Run(Options{Root: dir, Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: false})
	if err != nil {
		t.Fatalf("run should not abort on a broken/failing topic: %v", err)
	}
	if res.TopicsChecked != 3 {
		t.Errorf("TopicsChecked=%d, want 3", res.TopicsChecked)
	}
	if res.TopicsFetched != 1 {
		t.Errorf("TopicsFetched=%d, want 1 (only the healthy topic)", res.TopicsFetched)
	}
	if res.TopicsSkipped != 1 {
		t.Errorf("TopicsSkipped=%d, want 1 (IsBroken topic)", res.TopicsSkipped)
	}
	if res.TopicsFailed != 1 {
		t.Errorf("TopicsFailed=%d, want 1 (404 topic)", res.TopicsFailed)
	}

	ci, err := LoadCourseIndex(dir, "4242")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	if ci.Topics[6001] == nil || ci.Topics[6001].Current == "" {
		t.Error("healthy topic was not archived")
	}
	if ci.Topics[6002] != nil || ci.Topics[6003] != nil {
		t.Error("broken/failing topics must not be recorded in the index")
	}

	res2, err := Run(Options{Root: dir, Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: false})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res2.TopicsFetched != 0 || res2.TopicsSkipped != 1 || res2.TopicsFailed != 1 {
		t.Errorf("second run: fetched=%d skipped=%d failed=%d, want 0/1/1", res2.TopicsFetched, res2.TopicsSkipped, res2.TopicsFailed)
	}
}

func TestRunAbortsOnSessionExpired(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("555"), Name: "Bio", Code: "BIO", IsActive: true}}
	f.tocs["555"] = content.TocResponse{Modules: []content.TocModule{{
		ModuleId: 1, Topics: []content.TocTopic{{TopicId: 77, Title: "Cells", LastModifiedDate: "2026-03-01T00:00:00Z"}},
	}}}
	f.topicStatus[77] = http.StatusUnauthorized
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	_, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected the run to abort on 401")
	}
	if !errors.Is(err, errSessionExpired) {
		t.Errorf("err = %v, want errSessionExpired", err)
	}
}

func TestDiffSkipsBrokenTopics(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("777"), Name: "Hist", Code: "HIS", IsActive: true}}
	f.tocs["777"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 11, Title: "New topic", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 12, Title: "Broken topic", LastModifiedDate: "2022-01-01T00:00:00Z", IsBroken: true},
	}}}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res, err := Diff(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if res.TopicsChecked != 2 {
		t.Errorf("TopicsChecked=%d, want 2", res.TopicsChecked)
	}
	if res.TopicsWouldFetch != 1 {
		t.Errorf("TopicsWouldFetch=%d, want 1 (broken topic excluded)", res.TopicsWouldFetch)
	}
	if res.TopicsBroken != 1 {
		t.Errorf("TopicsBroken=%d, want 1", res.TopicsBroken)
	}
}
