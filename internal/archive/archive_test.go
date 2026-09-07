package archive

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/content"
	"github.com/aarav/schooltools/internal/d2lver"
	"golang.org/x/net/publicsuffix"
)

func TestSafeID(t *testing.T) {
	cases := map[string]string{
		"123456":   "123456",
		"":         "_",
		"  ":       "_",
		"a/b":      "a_b",
		"a\\b":     "a_b",
		"../etc":   "__etc",
		"a/../b/c": "a___b_c",
	}
	for in, want := range cases {
		if got := safeID(in); got != want {
			t.Errorf("safeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewUUID(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		id, err := NewUUID()
		if err != nil {
			t.Fatalf("NewUUID: %v", err)
		}
		if len(id) != 32 {
			t.Errorf("uuid len = %d, want 32", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate uuid: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestFlattenTopics(t *testing.T) {
	modules := []content.TocModule{
		{
			ModuleId: 1,
			Topics: []content.TocTopic{
				{TopicId: 10},
				{TopicId: 11},
			},
			Modules: []content.TocModule{
				{
					ModuleId: 2,
					Topics: []content.TocTopic{
						{TopicId: 20},
					},
				},
			},
		},
	}
	got := flattenTopics(modules)
	ids := make([]int, len(got))
	for i, t := range got {
		ids[i] = t.TopicId
	}
	want := []int{10, 11, 20}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("flattenTopics ids = %v, want %v", ids, want)
	}
}

func TestSortedCourseIDs(t *testing.T) {
	idx := &Index{
		Courses: map[string]*IndexEntry{
			"foo": {},
			"3":   {},
			"100": {},
			"20":  {},
			"abc": {},
		},
	}
	got := SortedCourseIDs(idx)
	want := []string{"3", "20", "100", "abc", "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortedCourseIDs = %v, want %v", got, want)
	}
}

func TestParseD2LTime(t *testing.T) {
	cases := map[string]time.Time{
		"":                            time.Time{},
		"2026-01-02T03:04:05Z":        time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		"2026-01-02T03:04:05.0000000Z": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	for in, want := range cases {
		got := parseD2LTime(in)
		if !got.Equal(want) {
			t.Errorf("parseD2LTime(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadIndexMissing(t *testing.T) {
	dir := t.TempDir()
	idx, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex on missing file: %v", err)
	}
	if idx.Version != SchemaVersion {
		t.Errorf("Version = %d, want %d", idx.Version, SchemaVersion)
	}
	if len(idx.Courses) != 0 {
		t.Errorf("Courses not empty: %v", idx.Courses)
	}
}

func TestSaveAndLoadIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{
		Version:   SchemaVersion,
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Courses: map[string]*IndexEntry{
			"123": {
				OrgUnitId:    "123",
				Name:         "Math 30-1",
				Code:         "MAT3191",
				IsActive:     true,
				TocFetchedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				TopicsTotal:  42,
				TopicsNew:    5,
				TopicsStale:  37,
			},
		},
	}
	if err := SaveIndex(dir, idx); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, indexFileName)); err != nil {
		t.Fatalf("expected index file: %v", err)
	}
	got, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if got.Version != idx.Version {
		t.Errorf("Version = %d, want %d", got.Version, idx.Version)
	}
	entry := got.Courses["123"]
	if entry == nil {
		t.Fatalf("missing course 123 in reloaded index")
	}
	if entry.Name != "Math 30-1" || entry.Code != "MAT3191" || entry.TopicsTotal != 42 {
		t.Errorf("entry mismatch: %+v", entry)
	}
}

func TestSaveIndexAtomicity(t *testing.T) {
	dir := t.TempDir()
	if err := SaveIndex(dir, &Index{Version: 1, Courses: map[string]*IndexEntry{}}); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, indexFileName+".tmp")); !os.IsNotExist(err) {
		t.Errorf("expected no .tmp file, got err=%v", err)
	}
}

func TestLoadCourseIndexMissing(t *testing.T) {
	dir := t.TempDir()
	ci, err := LoadCourseIndex(dir, "999")
	if err != nil {
		t.Fatalf("LoadCourseIndex on missing: %v", err)
	}
	if ci.CourseID != "999" || ci.Topics == nil || len(ci.Topics) != 0 {
		t.Errorf("empty index wrong: %+v", ci)
	}
}

func TestCourseIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	ci := &CourseIndex{
		CourseID: "42",
		Name:     "Test Course",
		Code:     "TST 101",
		IsActive: true,
		UpdatedAt: now,
		Topics: map[int]*TopicIndex{
			100: {
				TopicID:      100,
				Title:        "Syllabus",
				Type:         "File",
				LastModified: now,
				Current:      "abc123",
				Versions: []TopicVersion{
					{UUID: "abc123", LastModified: now, Size: 42, SavedAt: now},
				},
			},
		},
	}
	if err := SaveCourseIndex(dir, "42", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}
	got, err := LoadCourseIndex(dir, "42")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	if got.CourseID != "42" || got.Name != "Test Course" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	ti := got.Topics[100]
	if ti == nil || ti.Current != "abc123" || len(ti.Versions) != 1 {
		t.Errorf("topic mismatch: %+v", ti)
	}
}

func TestSaveCourseIndexCreatesDir(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCourseIndex(dir, "777", &CourseIndex{CourseID: "777", Topics: map[int]*TopicIndex{}}); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}
	want := filepath.Join(dir, coursesDirName, "777", courseIndexFileName)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected index at %s: %v", want, err)
	}
}

func TestSaveBlobAndLoad(t *testing.T) {
	dir := t.TempDir()
	uuid, size, err := SaveBlob(dir, []byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}
	if uuid == "" || size != int64(len(`{"hello":"world"}`)) {
		t.Errorf("bad uuid/size: %s %d", uuid, size)
	}
	body, err := LoadBlob(dir, uuid)
	if err != nil {
		t.Fatalf("LoadBlob: %v", err)
	}
	if string(body) != `{"hello":"world"}` {
		t.Errorf("blob body mismatch: %q", body)
	}
}

func TestLoadBlobMissing(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, blobsDirName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := LoadBlob(dir, "deadbeef")
	if err != nil {
		t.Fatalf("LoadBlob missing: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil, got %q", data)
	}
}

func TestLoadBlobEmptyUUID(t *testing.T) {
	if data, err := LoadBlob(t.TempDir(), ""); err != nil || data != nil {
		t.Errorf("LoadBlob empty: data=%v err=%v", data, err)
	}
}

// ---- Prune -----------------------------------------------------------------

func TestPruneKeepsCurrentDropsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	// Seed three blobs: one current, one historical version, one orphan.
	keepA, _, err := SaveBlob(dir, []byte(`"A"`))
	if err != nil {
		t.Fatalf("SaveBlob A: %v", err)
	}
	oldB, _, err := SaveBlob(dir, []byte(`"B"`))
	if err != nil {
		t.Fatalf("SaveBlob B: %v", err)
	}
	orphan, _, err := SaveBlob(dir, []byte(`"ORPHAN"`))
	if err != nil {
		t.Fatalf("SaveBlob orphan: %v", err)
	}

	ci := &CourseIndex{
		CourseID: "1",
		Topics: map[int]*TopicIndex{
			10: {
				TopicID:      10,
				Current:      keepA,
				LastModified: time.Now().UTC(),
				Versions: []TopicVersion{
					// keepA is the current blob. oldB is a previous version
					// that Prune should drop.
					{UUID: oldB, LastModified: time.Now().UTC(), Size: 2, SavedAt: time.Now().UTC()},
					{UUID: keepA, LastModified: time.Now().UTC(), Size: 2, SavedAt: time.Now().UTC()},
				},
			},
		},
	}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.BlobsBefore != 3 {
		t.Errorf("BlobsBefore = %d, want 3", res.BlobsBefore)
	}
	if res.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2 (old version + orphan)", res.Deleted)
	}
	if res.BlobsAfter != 1 {
		t.Errorf("BlobsAfter = %d, want 1", res.BlobsAfter)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, orphan+blobExt)); !os.IsNotExist(err) {
		t.Errorf("orphan should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, oldB+blobExt)); !os.IsNotExist(err) {
		t.Errorf("old version should be gone, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, keepA+blobExt)); err != nil {
		t.Errorf("current missing: %v", err)
	}
}

func TestPruneNoBlobsDir(t *testing.T) {
	dir := t.TempDir()
	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune on empty dir: %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", res.Deleted)
	}
}

func TestPruneDropsOldVersions(t *testing.T) {
	dir := t.TempDir()
	cur, _, err := SaveBlob(dir, []byte(`"current"`))
	if err != nil {
		t.Fatalf("SaveBlob cur: %v", err)
	}
	old1, _, err := SaveBlob(dir, []byte(`"old1"`))
	if err != nil {
		t.Fatalf("SaveBlob old1: %v", err)
	}
	old2, _, err := SaveBlob(dir, []byte(`"old2"`))
	if err != nil {
		t.Fatalf("SaveBlob old2: %v", err)
	}

	ci := &CourseIndex{
		CourseID: "1",
		Topics: map[int]*TopicIndex{
			10: {
				TopicID:      10,
				Current:      cur,
				LastModified: time.Now().UTC(),
				Versions: []TopicVersion{
					{UUID: old2, LastModified: time.Now().UTC(), Size: 5, SavedAt: time.Now().UTC()},
					{UUID: old1, LastModified: time.Now().UTC(), Size: 4, SavedAt: time.Now().UTC()},
					{UUID: cur, LastModified: time.Now().UTC(), Size: 8, SavedAt: time.Now().UTC()},
				},
			},
		},
	}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Deleted != 2 {
		t.Errorf("Deleted = %d, want 2 (old1 and old2 removed; current kept)", res.Deleted)
	}
}

func TestPruneKeepsLiveAcrossCourses(t *testing.T) {
	dir := t.TempDir()
	blobA, _, _ := SaveBlob(dir, []byte(`"A"`))
	blobB, _, _ := SaveBlob(dir, []byte(`"B"`))
	orphan, _, _ := SaveBlob(dir, []byte(`"ORPHAN"`))

	ci1 := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {TopicID: 10, Current: blobA, LastModified: time.Now().UTC()},
	}}
	ci2 := &CourseIndex{CourseID: "2", Topics: map[int]*TopicIndex{
		20: {TopicID: 20, Current: blobB, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci1); err != nil {
		t.Fatalf("SaveCourseIndex 1: %v", err)
	}
	if err := SaveCourseIndex(dir, "2", ci2); err != nil {
		t.Fatalf("SaveCourseIndex 2: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, orphan+blobExt)); !os.IsNotExist(err) {
		t.Errorf("orphan still there: err=%v", err)
	}
}

// ---- end-to-end with stubbed HTTP -----------------------------------------

type fakeD2L struct {
	t          *testing.T
	courses    []d2lCourse
	tocs       map[string]content.TocResponse
	topics     map[int]map[string]any
	tocCalls   int
	topicCalls map[int]int
}

func newFakeD2L(t *testing.T) *fakeD2L {
	// Force d2lver cache to fall back to the hardcoded constants so the
	// fakeD2L mock's `/d2l/api/le/1.47/` handler is hit. Without this, the
	// persisted `~/.config/schooltools/d2lver.json` (written by real
	// sessions) routes archive calls through `/d2l/api/le/1.97/...`, which
	// the test fixture doesn't mock.
	oldHome := os.Getenv("HOME")
	oldCfg := os.Getenv("XDG_CONFIG_HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Cleanup(func() {
		t.Setenv("HOME", oldHome)
		t.Setenv("XDG_CONFIG_HOME", oldCfg)
		d2lver.Reset()
	})
	d2lver.Reset()
	return &fakeD2L{
		t:          t,
		tocs:       map[string]content.TocResponse{},
		topics:     map[int]map[string]any{},
		topicCalls: map[int]int{},
	}
}

func (f *fakeD2L) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/d2l/le/manageCourses/api/mycourses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d2lCoursesResponse{Courses: f.courses})
	})
	mux.HandleFunc("/d2l/api/le/1.47/", func(w http.ResponseWriter, r *http.Request) {
		parts := splitPath(r.URL.Path)
		if len(parts) < 7 || parts[5] != "content" {
			http.NotFound(w, r)
			return
		}
		course := parts[4]
		switch parts[6] {
		case "toc":
			f.tocCalls++
			toc, ok := f.tocs[course]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(toc)
		case "topics":
			if len(parts) < 8 {
				http.NotFound(w, r)
				return
			}
			id := atoi(parts[7])
			f.topicCalls[id]++
			topic, ok := f.topics[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(topic)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, c := range p {
		if c == '/' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func newJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return jar
}

// TestRunVersioning_KeepsOldBlobs verifies the central promise: a topic that
// changes gets a brand-new blob; the old blob stays on disk and the index's
// Version history grows.
func TestRunVersioning_KeepsOldBlobs(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("1001"), Name: "Math 30-1", Code: "MAT3191", IsActive: true},
	}
	f.tocs["1001"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 5001, Title: "Syllabus", LastModifiedDate: "2026-01-01T00:00:00Z"},
	}}}}
	f.topics[5001] = map[string]any{"TopicId": 5001, "Title": "Syllabus"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)

	res, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.TopicsFetched != 1 {
		t.Errorf("first run: fetched=%d, want 1", res.TopicsFetched)
	}

	// Inspect what we wrote.
	ci, err := LoadCourseIndex(dir, "1001")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	ti := ci.Topics[5001]
	if ti == nil || ti.Current == "" {
		t.Fatalf("topic missing or no current: %+v", ti)
	}
	firstUUID := ti.Current
	if len(ti.Versions) != 1 {
		t.Errorf("first run: versions=%d, want 1", len(ti.Versions))
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, firstUUID+blobExt)); err != nil {
		t.Errorf("first blob missing: %v", err)
	}

	// Update the TOC: same topic, new LastModifiedDate.
	f2 := newFakeD2L(t)
	f2.courses = f.courses
	f2.tocs["1001"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 5001, Title: "Syllabus v2", LastModifiedDate: "2026-02-01T00:00:00Z"},
	}}}}
	f2.topics[5001] = map[string]any{"TopicId": 5001, "Title": "Syllabus v2"}
	srv.Config.Handler = f2.handler()

	if _, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	ci2, err := LoadCourseIndex(dir, "1001")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	ti2 := ci2.Topics[5001]
	if ti2.Current == firstUUID {
		t.Errorf("current uuid should have changed; still %s", firstUUID)
	}
	if len(ti2.Versions) != 2 {
		t.Errorf("second run: versions=%d, want 2", len(ti2.Versions))
	}

	// Both blobs must still be on disk: the UUID store never overwrites.
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, firstUUID+blobExt)); err != nil {
		t.Errorf("old blob missing after update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, ti2.Current+blobExt)); err != nil {
		t.Errorf("new blob missing: %v", err)
	}

	// Prune: old blob goes away, current stays.
	pr, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pr.Deleted != 1 {
		t.Errorf("Prune: deleted=%d, want 1", pr.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, firstUUID+blobExt)); !os.IsNotExist(err) {
		t.Errorf("old blob still present after Prune: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, ti2.Current+blobExt)); err != nil {
		t.Errorf("current blob missing after Prune: %v", err)
	}

	// Per-course index must reflect the post-prune state — the index still
	// lists both UUIDs in Versions[], but only the current one is on disk.
	// The Versions history is intentional: it's the audit trail, and Prune
	// does not rewrite history, it only removes orphan files. Re-running
	// archive after Prune will still see no new fetch needed (the current
	// UUID matches the live one) and will not write a third blob.
	f3 := newFakeD2L(t)
	f3.courses = f.courses
	f3.tocs["1001"] = f2.tocs["1001"]
	f3.topics[5001] = f2.topics[5001]
	srv.Config.Handler = f3.handler()
	res3, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if res3.TopicsFetched != 0 {
		t.Errorf("third run: fetched=%d, want 0 (nothing changed since second run)", res3.TopicsFetched)
	}
}

func TestRunSelectiveCourseIDs_SkipsManageCourses(t *testing.T) {
	f := newFakeD2L(t)
	f.tocs["42"] = content.TocResponse{}
	f.tocs["43"] = content.TocResponse{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	dir := t.TempDir()
	jar := newJar(t)

	var phases []string
	res, err := Run(Options{
		Root:      dir,
		Jar:       jar,
		BaseURL:   srv.URL,
		CourseIDs: []string{"42", "43", ""},
		Progress: func(ev ProgressEvent) {
			phases = append(phases, ev.Phase)
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.CoursesScanned != 2 {
		t.Errorf("scanned=%d, want 2 (empty arg filtered)", res.CoursesScanned)
	}
	got := strings.Join(phases, ",")
	if !strings.Contains(got, "list,toc,course-done") {
		t.Errorf("phases=%q, want to contain list,toc,course-done", got)
	}
}

func TestEmitNilSafe(t *testing.T) {
	emit(nil, ProgressEvent{Phase: "list"})
	// No panic = pass.
}

func TestTopicURLHelper(t *testing.T) {
	if topicType(content.TocTopic{TopicType: 2}) != "Link" {
		t.Errorf("topicType=Link failed")
	}
	if topicType(content.TocTopic{TopicType: 1}) != "File" {
		t.Errorf("topicType=File failed")
	}
	if topicType(content.TocTopic{TypeIdentifier: "X"}) != "X" {
		t.Errorf("topicType=TypeIdentifier failed")
	}
	u := "https://x"
	if topicURL(content.TocTopic{Url: &u}) != "https://x" {
		t.Errorf("topicURL: %s", topicURL(content.TocTopic{Url: &u}))
	}
	if topicURL(content.TocTopic{}) != "" {
		t.Errorf("topicURL empty: should be empty")
	}
}

// Sanity: sort.Slice helper for stable test ordering.
var _ = sort.Slice
