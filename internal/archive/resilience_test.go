package archive

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		{TopicId: 6004, Title: "After the failure", LastModifiedDate: "2026-03-03T00:00:00Z"},
	}}}}
	f.topics[6001] = map[string]any{"TopicId": 6001, "Title": "Fine", "TypeIdentifier": "File", "Url": goodURL}
	f.topics[6004] = map[string]any{"TopicId": 6004, "Title": "After the failure"}
	// 6002 is flagged broken and 6003 is never registered, so the topics API 404s it.

	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()

	res, err := Run(Options{Root: dir, Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: false})
	if err != nil {
		t.Fatalf("run should not abort on a broken/failing topic: %v", err)
	}
	if res.TopicsChecked != 4 {
		t.Errorf("TopicsChecked=%d, want 4", res.TopicsChecked)
	}
	if res.TopicsFetched != 2 {
		t.Errorf("TopicsFetched=%d, want 2 (healthy topics on both sides of the failure)", res.TopicsFetched)
	}
	if res.TopicsSkipped != 1 {
		t.Errorf("TopicsSkipped=%d, want 1 (IsBroken topic)", res.TopicsSkipped)
	}
	if res.TopicsFailed != 1 {
		t.Errorf("TopicsFailed=%d, want 1 (404 topic)", res.TopicsFailed)
	}
	if calls := f.topicCallCount(6002); calls != 0 {
		t.Errorf("broken topic 6002 was fetched %d times, want 0 (must be skipped without an API call)", calls)
	}

	ci, err := LoadCourseIndex(dir, "4242")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	if ci.Topics[6001] == nil || ci.Topics[6001].Current == "" {
		t.Error("healthy topic 6001 was not archived")
	}
	if ci.Topics[6004] == nil || ci.Topics[6004].Current == "" {
		t.Error("topic 6004, queued after the failure, was not archived")
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

func TestRunAbortsAfterConsecutiveTransportFailures(t *testing.T) {
	f := newFakeD2L(t)
	var topics []content.TocTopic
	// Two topics fetch cleanly first, then the network goes away.
	for i := 7001; i <= 7002; i++ {
		topics = append(topics, content.TocTopic{TopicId: i, Title: "Good", LastModifiedDate: "2026-03-01T00:00:00Z"})
		f.topics[i] = map[string]any{"TopicId": i, "Title": "Good"}
	}
	for i := 7003; i <= 7008; i++ {
		topics = append(topics, content.TocTopic{TopicId: i, Title: "Gone", LastModifiedDate: "2026-03-01T00:00:00Z"})
		f.hijackTopics[i] = true
	}
	f.courses = []d2lCourse{{OrgUnitId: json.Number("4700"), Name: "Bio", Code: "BIO", IsActive: true}}
	f.tocs["4700"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: topics}}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	dir := t.TempDir()
	_, err := Run(Options{Root: dir, Jar: newJar(t), BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected the run to abort on consecutive transport failures")
	}
	if !strings.Contains(err.Error(), "consecutive transport failures") {
		t.Errorf("err = %v, want a consecutive-transport-failure abort", err)
	}
	// Six broken topics were queued; the pass must stop at the bound,
	// not grind through all of them during an outage.
	attempts := 0
	for i := 7003; i <= 7008; i++ {
		attempts += f.topicCallCount(i)
	}
	if attempts > maxConsecutiveTransportFailures {
		t.Errorf("%d transport attempts, want at most %d", attempts, maxConsecutiveTransportFailures)
	}
	// Whatever was fetched before the outage must stay archived: the
	// index is persisted on the abort path so the blobs already on disk
	// keep a reference.
	ci, lerr := LoadCourseIndex(dir, "4700")
	if lerr != nil {
		t.Fatalf("LoadCourseIndex: %v", lerr)
	}
	for _, id := range []int{7001, 7002} {
		if ci.Topics[id] == nil || ci.Topics[id].Current == "" {
			t.Errorf("topic %d fetched before the outage was not persisted in the index", id)
		}
	}
	if ci.Topics[7003] != nil {
		t.Error("a topic that never arrived must not be in the index")
	}
}

func TestRunToleratesHTTPFailuresAfterTransportBounded(t *testing.T) {
	// HTTP failures must not consume the transport budget: a mix of
	// 404s and one transport error keeps the pass going.
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("4701"), Name: "Bio", Code: "BIO", IsActive: true}}
	f.tocs["4701"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 7101, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7102, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7103, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7104, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7105, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7106, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
		{TopicId: 7107, Title: "Deleted", LastModifiedDate: "2026-03-01T00:00:00Z"},
	}}}}
	f.hijackTopics[7107] = true
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("HTTP failures must not abort the pass: %v", err)
	}
	if res.TopicsFailed != 7 {
		t.Errorf("TopicsFailed=%d, want 7", res.TopicsFailed)
	}
}

func TestRunAbortsOnExpiredToc(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("4610"), Name: "Bio", Code: "BIO", IsActive: true}}
	f.tocs["4610"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 7201, Title: "Cells", LastModifiedDate: "2026-03-01T00:00:00Z"},
	}}}}
	f.tocStatus = http.StatusForbidden
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	_, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected the run to abort when the TOC request 403s")
	}
	if !errors.Is(err, errSessionExpired) {
		t.Errorf("Run err = %v, want errSessionExpired", err)
	}

	_, derr := Diff(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, CourseIDs: []string{"4610"}})
	if derr == nil {
		t.Fatal("expected Diff to abort when the TOC request 403s")
	}
	if !errors.Is(derr, errSessionExpired) {
		t.Errorf("Diff err = %v, want errSessionExpired", derr)
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

func TestRunAbortsOnExpiredCourseList(t *testing.T) {
	f := newFakeD2L(t)
	f.coursesStatus = http.StatusUnauthorized
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	_, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected the run to abort when the course list 401s")
	}
	if !errors.Is(err, errSessionExpired) {
		t.Errorf("err = %v, want errSessionExpired", err)
	}
}

func TestRunAbortsOnLoginPageTopicResponse(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("556"), Name: "Bio", Code: "BIO", IsActive: true}}
	f.tocs["556"] = content.TocResponse{Modules: []content.TocModule{{
		ModuleId: 1, Topics: []content.TocTopic{{TopicId: 78, Title: "Cells", LastModifiedDate: "2026-03-01T00:00:00Z"}},
	}}}
	f.topicRaw[78] = "<html><head><title>Log In</title></head><body>d2l login page</body></html>"
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	dir := t.TempDir()
	_, err := Run(Options{Root: dir, Jar: newJar(t), BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected the run to abort when a topic fetch returns the login page")
	}
	if !errors.Is(err, errSessionExpired) {
		t.Errorf("err = %v, want errSessionExpired", err)
	}
	ci, lerr := LoadCourseIndex(dir, "556")
	if lerr != nil {
		t.Fatalf("LoadCourseIndex: %v", lerr)
	}
	if ci.Topics[78] != nil {
		t.Error("the login page must not be archived as topic metadata")
	}
}

func TestRunAbortsOnExpiredBodyDownload(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("557"), Name: "Bio", Code: "BIO", IsActive: true}}
	urlStr := "/content/enforced/557-BIO/notes.pdf"
	f.tocs["557"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 79, Title: "Notes", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &urlStr},
	}}}}
	f.topics[79] = map[string]any{"TopicId": 79, "Title": "Notes", "TypeIdentifier": "File", "Url": urlStr}
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	_, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: true})
	if err == nil {
		t.Fatal("expected the run to abort when a body download 401s")
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
