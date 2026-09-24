package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/content"
)

func TestPrunePlanWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cur, _, err := SaveBlob(dir, []byte(`"current"`))
	if err != nil {
		t.Fatalf("SaveBlob cur: %v", err)
	}
	old, _, err := SaveBlob(dir, []byte(`"old"`))
	if err != nil {
		t.Fatalf("SaveBlob old: %v", err)
	}

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {TopicID: 10, Current: cur, LastModified: time.Now().UTC(),
			Versions: []TopicVersion{
				{UUID: old, LastModified: time.Now().UTC(), Size: 4, SavedAt: time.Now().UTC()},
				{UUID: cur, LastModified: time.Now().UTC(), Size: 8, SavedAt: time.Now().UTC()},
			}},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := PrunePlan(dir)
	if err != nil {
		t.Fatalf("PrunePlan: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("plan Deleted=%d, want 1", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, old+blobExt)); err != nil {
		t.Errorf("plan removed the old blob: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, cur+blobExt)); err != nil {
		t.Errorf("plan removed the current blob: %v", err)
	}
	ci2, _ := LoadCourseIndex(dir, "1")
	if len(ci2.Topics[10].Versions) != 2 {
		t.Errorf("plan rewrote Versions; len=%d, want 2", len(ci2.Topics[10].Versions))
	}
}

func TestPruneKeepsCurrentBodyBlob(t *testing.T) {
	dir := t.TempDir()
	curMeta, _, err := SaveBlob(dir, []byte(`"meta"`))
	if err != nil {
		t.Fatalf("SaveBlob meta: %v", err)
	}
	bodyHash, _, err := SaveFileBody(dir, []byte("file body bytes"))
	if err != nil {
		t.Fatalf("SaveFileBody: %v", err)
	}
	orphanMeta, _, err := SaveBlob(dir, []byte(`"orphan"`))
	if err != nil {
		t.Fatalf("SaveBlob orphan: %v", err)
	}

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {
			TopicID: 10, Current: curMeta, CurrentBody: bodyHash,
			LastModified: time.Now().UTC(),
			Versions:     []TopicVersion{{UUID: curMeta, LastModified: time.Now().UTC(), Size: 6, SavedAt: time.Now().UTC()}},
			BodyVersions: []BodyVersion{{SHA256: bodyHash, Size: 15, DownloadedAt: time.Now().UTC()}},
		},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("Deleted=%d, want 1 (orphan meta only); orphans=%v", res.Deleted, res.Orphans)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, bodyHash+bodyExt)); err != nil {
		t.Errorf("Prune deleted the live body blob: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, orphanMeta+blobExt)); !os.IsNotExist(err) {
		t.Errorf("orphan meta still present: err=%v", err)
	}
}

func TestPruneTrimsDanglingRecords(t *testing.T) {
	dir := t.TempDir()
	cur, _, _ := SaveBlob(dir, []byte(`"current"`))
	bodyHash, _, _ := SaveFileBody(dir, []byte("body"))

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {
			TopicID: 10, Current: cur, CurrentBody: bodyHash,
			LastModified: time.Now().UTC(),
			Versions: []TopicVersion{
				{UUID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LastModified: time.Now().UTC(), Size: 1, SavedAt: time.Now().UTC()},
				{UUID: cur, LastModified: time.Now().UTC(), Size: 8, SavedAt: time.Now().UTC()},
			},
			BodyVersions: []BodyVersion{
				{SHA256: strings.Repeat("0", 64), Size: 1, DownloadedAt: time.Now().UTC()},
				{SHA256: bodyHash, Size: 4, DownloadedAt: time.Now().UTC()},
			},
		},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.TrimmedRecords != 2 {
		t.Errorf("TrimmedRecords=%d, want 2 (one dangling meta + one dangling body)", res.TrimmedRecords)
	}
	ci2, _ := LoadCourseIndex(dir, "1")
	if got := len(ci2.Topics[10].Versions); got != 1 {
		t.Errorf("Versions after Prune=%d, want 1 (only current)", got)
	}
	if got := len(ci2.Topics[10].BodyVersions); got != 1 {
		t.Errorf("BodyVersions after Prune=%d, want 1 (only current)", got)
	}
}

func TestPruneKeepsSharedBodyBlob(t *testing.T) {
	dir := t.TempDir()
	bodyHash, _, _ := SaveFileBody(dir, []byte("shared bytes"))
	curA, _, _ := SaveBlob(dir, []byte(`"A"`))
	curB, _, _ := SaveBlob(dir, []byte(`"B"`))

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {TopicID: 10, Current: curA, CurrentBody: bodyHash, LastModified: time.Now().UTC()},
		11: {TopicID: 11, Current: curB, CurrentBody: bodyHash, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Prune(dir)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("Deleted=%d, want 0 (shared body is live via both topics)", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName, bodyHash+bodyExt)); err != nil {
		t.Errorf("shared body removed: %v", err)
	}
}

func TestStoreResolveRefGrammar(t *testing.T) {
	dir := t.TempDir()
	cur, _, _ := SaveBlob(dir, []byte(`{"TopicId":42}`))
	bodyHash := sha256.Sum256([]byte("body-bytes"))
	bodyHex := hex.EncodeToString(bodyHash[:])
	if _, _, err := SaveFileBody(dir, []byte("body-bytes")); err != nil {
		t.Fatalf("SaveFileBody: %v", err)
	}

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		42: {
			TopicID: 42, Title: "Outline", Type: "File", URL: "/content/enforced/1/notes.pdf",
			Current: cur, CurrentBody: bodyHex,
			LastModified: time.Now().UTC(),
			Versions:     []TopicVersion{{UUID: cur, LastModified: time.Now().UTC(), Size: 16, SavedAt: time.Now().UTC()}},
			BodyVersions: []BodyVersion{{SHA256: bodyHex, Size: 10, DownloadedAt: time.Now().UTC()}},
		},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	res, err := store.Resolve("42", "")
	if err != nil {
		t.Fatalf("resolve topicId: %v", err)
	}
	if res.Kind != "body" || res.BlobID != bodyHex {
		t.Errorf("resolve 42: kind=%s id=%s, want body/%s", res.Kind, res.BlobID, bodyHex)
	}

	res, err = store.Resolve(cur, "")
	if err != nil {
		t.Fatalf("resolve uuid: %v", err)
	}
	if res.Kind != "metadata" || res.BlobID != cur {
		t.Errorf("resolve uuid: kind=%s id=%s", res.Kind, res.BlobID)
	}

	res, err = store.Resolve(bodyHex, "")
	if err != nil {
		t.Fatalf("resolve body sha: %v", err)
	}
	if res.Kind != "body" || res.BlobID != bodyHex {
		t.Errorf("resolve sha: kind=%s id=%s", res.Kind, res.BlobID)
	}

	if _, err := store.Resolve("9999999", ""); err == nil {
		t.Errorf("resolve unknown topicId: expected error")
	}
}

func TestStoreSearchSingleLoad(t *testing.T) {
	dir := t.TempDir()
	bodyHash := sha256.Sum256([]byte("body"))
	bodyHex := hex.EncodeToString(bodyHash[:])
	_, _, _ = SaveFileBody(dir, []byte("body"))

	ci := &CourseIndex{CourseID: "1", Name: "Safety Training", Code: "SAF101", IsActive: true, Topics: map[int]*TopicIndex{
		101: {TopicID: 101, Title: "Safety Contract", Type: "File", URL: "/x.pdf", CurrentBody: bodyHex, LastModified: time.Now().UTC()},
		102: {TopicID: 102, Title: "Lab Safety Notes", Type: "File", URL: "/y.docx", CurrentBody: bodyHex, LastModified: time.Now().UTC()},
		103: {TopicID: 103, Title: "Course Outline", Type: "File", URL: "/z.pdf", CurrentBody: "", LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if got := len(store.entries); got != 3 {
		t.Fatalf("store.entries=%d, want 3", got)
	}
	hits := store.Search(SearchQuery{Terms: "safety"})
	if len(hits) != 2 {
		t.Errorf("search 'safety': %d hits, want 2", len(hits))
	}
	hits = store.Search(SearchQuery{Terms: "safety", Ext: "pdf"})
	if len(hits) != 1 || hits[0].TopicID != 101 {
		t.Errorf("search 'safety' ext=pdf: hits=%+v, want topic 101", hits)
	}
	hits = store.Search(SearchQuery{Terms: "", MissingBodies: true})
	if len(hits) != 1 || hits[0].TopicID != 103 {
		t.Errorf("search missing-bodies: hits=%+v, want topic 103", hits)
	}
	hits = store.Search(SearchQuery{Terms: "outline", WithBodies: true})
	if len(hits) != 0 {
		t.Errorf("search 'outline' with-bodies: %d hits, want 0 (outline has no body)", len(hits))
	}
	hits = store.Search(SearchQuery{Terms: "outline", MissingBodies: true})
	if len(hits) != 1 {
		t.Errorf("search 'outline' missing-bodies: %d hits, want 1", len(hits))
	}
}

func TestVerifyDetectsDangling(t *testing.T) {
	dir := t.TempDir()
	cur, _, _ := SaveBlob(dir, []byte(`"cur"`))
	_, _, _ = SaveBlob(dir, []byte(`"old-orphan"`))

	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		10: {
			TopicID: 10, Current: cur,
			Versions:     []TopicVersion{{UUID: "deadbeefdeadbeefdeadbeefdeadbeef", LastModified: time.Now().UTC(), Size: 1, SavedAt: time.Now().UTC()}},
			LastModified: time.Now().UTC(),
		},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	res, err := Verify(dir, false)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.OK {
		t.Errorf("Verify OK=true, expected false (dangling version + orphan blob)")
	}
	if len(res.Orphans) != 1 {
		t.Errorf("Orphans=%d, want 1", len(res.Orphans))
	}
	foundDangling := false
	for _, iss := range res.Issues {
		if iss.Code == "dangling-metadata-version" {
			foundDangling = true
		}
	}
	if !foundDangling {
		t.Errorf("Verify did not flag dangling-metadata-version: %+v", res.Issues)
	}
}

func TestExportCopiesBodies(t *testing.T) {
	dir := t.TempDir()
	bodyData := []byte("body-content")
	_, _, err := SaveFileBody(dir, bodyData)
	if err != nil {
		t.Fatalf("SaveFileBody: %v", err)
	}
	bodyHash := sha256.Sum256(bodyData)
	bodyHex := hex.EncodeToString(bodyHash[:])

	ci := &CourseIndex{CourseID: "1", Name: "Safety", Code: "SAF101", IsActive: true, Topics: map[int]*TopicIndex{
		42: {TopicID: 42, Title: "Safety Contract", Type: "File", URL: "/x.pdf", CurrentBody: bodyHex, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	out := t.TempDir()
	res, err := Export(ExportOptions{Root: dir, Out: out})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Copied != 1 {
		t.Errorf("Copied=%d, want 1", res.Copied)
	}
	want := filepath.Join(out, "SAF101", "Safety Contract.pdf")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if string(got) != string(bodyData) {
		t.Errorf("exported content mismatch: %q", got)
	}
}

func TestExportDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	bodyData := []byte("body-content")
	_, _, _ = SaveFileBody(dir, bodyData)
	bodyHash := sha256.Sum256(bodyData)
	bodyHex := hex.EncodeToString(bodyHash[:])

	ci := &CourseIndex{CourseID: "1", Code: "SAF101", Topics: map[int]*TopicIndex{
		42: {TopicID: 42, Title: "Safety Contract", Type: "File", URL: "/x.pdf", CurrentBody: bodyHex, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	out := t.TempDir()
	res, err := Export(ExportOptions{Root: dir, Out: out, DryRun: true})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Copied != 0 {
		t.Errorf("Dry-run Copied=%d, want 0", res.Copied)
	}
	if _, err := os.Stat(filepath.Join(out, "SAF101")); !os.IsNotExist(err) {
		t.Errorf("dry-run created the course folder: err=%v", err)
	}
}

func TestLoadStatusReportsMissingBodies(t *testing.T) {
	dir := t.TempDir()
	_, _, _ = SaveFileBody(dir, []byte("body"))
	bodyHash := sha256.Sum256([]byte("body"))
	bodyHex := hex.EncodeToString(bodyHash[:])

	ci := &CourseIndex{CourseID: "1", Code: "SAF", Topics: map[int]*TopicIndex{
		1: {TopicID: 1, Title: "a", Type: "File", URL: "/x.pdf", CurrentBody: bodyHex},
		2: {TopicID: 2, Title: "b", Type: "File", URL: "/y.pdf", CurrentBody: ""},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	st, err := LoadStatus(dir)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if st.FileTopics != 2 {
		t.Errorf("FileTopics=%d, want 2", st.FileTopics)
	}
	if st.BodiesPresent != 1 {
		t.Errorf("BodiesPresent=%d, want 1", st.BodiesPresent)
	}
	if st.MissingBodies != 1 {
		t.Errorf("MissingBodies=%d, want 1", st.MissingBodies)
	}
}

func TestUpdateBodyConvergenceFillsMissingBody(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("7777"), Name: "Lab", Code: "LAB", IsActive: true}}
	urlStr := "/content/enforced/7777-T1/notes.pdf"
	f.tocs["7777"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 9001, Title: "Notes", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &urlStr},
	}}}}
	f.topics[9001] = map[string]any{
		"TopicId":        9001,
		"Title":          "Notes",
		"TypeIdentifier": "File",
		"Url":            urlStr,
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)

	bodyData := []byte("PDF-body")
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(bodyData)
	}
	srv.Config.Handler = f.handler()

	if _, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: false}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	ci, _ := LoadCourseIndex(dir, "7777")
	if ci.Topics[9001].CurrentBody != "" {
		t.Fatalf("expected no body on first run (DownloadFiles=false), got %s", ci.Topics[9001].CurrentBody)
	}

	res, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.BodiesFetched != 1 {
		t.Errorf("BodiesFetched on second run = %d, want 1 (convergence should fill the missing body even though metadata is unchanged)", res.BodiesFetched)
	}
	ci2, _ := LoadCourseIndex(dir, "7777")
	if ci2.Topics[9001].CurrentBody == "" {
		t.Errorf("body not filled after convergence pass")
	}
}

func TestUpdateBodyConvergenceRefetchesMissingBlob(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("6666"), Name: "Lab", Code: "LAB", IsActive: true}}
	urlStr := "/content/enforced/6666-T1/notes.pdf"
	f.tocs["6666"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 8001, Title: "Notes", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &urlStr},
	}}}}
	f.topics[8001] = map[string]any{"TopicId": 8001, "Title": "Notes", "TypeIdentifier": "File", "Url": urlStr}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)

	bodyData := []byte("body1")
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(bodyData)
	}
	srv.Config.Handler = f.handler()

	if _, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: true}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	ci, _ := LoadCourseIndex(dir, "6666")
	savedSHA := ci.Topics[8001].CurrentBody
	if savedSHA == "" {
		t.Fatalf("first run did not save body")
	}
	if err := os.Remove(BodyPath(dir, savedSHA)); err != nil {
		t.Fatalf("simulate missing blob: %v", err)
	}

	res, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.BodiesFetched != 1 {
		t.Errorf("BodiesFetched after simulated blob loss = %d, want 1 (convergence should re-fetch the missing body)", res.BodiesFetched)
	}
	ci2, _ := LoadCourseIndex(dir, "6666")
	if ci2.Topics[8001].CurrentBody != savedSHA {
		t.Errorf("body SHA changed after re-fetch: %s -> %s", savedSHA, ci2.Topics[8001].CurrentBody)
	}
	if _, err := os.Stat(BodyPath(dir, savedSHA)); err != nil {
		t.Errorf("body blob not restored: %v", err)
	}
}
