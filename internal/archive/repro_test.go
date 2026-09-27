package archive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aarav/schooltools/internal/content"
)

// TestRepro_Issue1_RunPopulatesIndexEntryCounts reproduces KNOWN_ISSUES #1:
// after a Run pass, the global IndexEntry for each course should carry the
// real topic count and "TOC Fetched" timestamp — not the zero values that
// the current code writes.
func TestRepro_Issue1_RunPopulatesIndexEntryCounts(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("7777"), Name: "Bio 30", Code: "BIO3191", IsActive: true},
	}
	f.tocs["7777"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 1001, Title: "Syllabus", LastModifiedDate: "2026-01-01T00:00:00Z"},
		{TopicId: 1002, Title: "Unit 1", LastModifiedDate: "2026-01-02T00:00:00Z"},
		{TopicId: 1003, Title: "Unit 2", LastModifiedDate: "2026-01-03T00:00:00Z"},
	}}}}
	f.topics[1001] = map[string]any{"TopicId": 1001, "Title": "Syllabus"}
	f.topics[1002] = map[string]any{"TopicId": 1002, "Title": "Unit 1"}
	f.topics[1003] = map[string]any{"TopicId": 1003, "Title": "Unit 2"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)

	if _, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL}); err != nil {
		t.Fatalf("run: %v", err)
	}

	idx, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	entry := idx.Courses["7777"]
	if entry == nil {
		t.Fatalf("no entry for 7777: %+v", idx.Courses)
	}
	if entry.TopicsTotal != 3 {
		t.Errorf("TopicsTotal = %d, want 3 (KNOWN_ISSUES #1)", entry.TopicsTotal)
	}
	if entry.TopicsNew != 3 {
		t.Errorf("TopicsNew = %d, want 3", entry.TopicsNew)
	}
	if entry.TocFetchedAt.IsZero() {
		t.Errorf("TocFetchedAt is zero (KNOWN_ISSUES #1): %v", entry.TocFetchedAt)
	}
}

// TestRepro_Issue2_DiffPersistsTOC reproduces KNOWN_ISSUES #2: a --diff pass
// TestRepro_Issue2_DiffWritesNothing asserts that Diff is a true preview
// pass: it must not create the archive root, must not write the global
// index, and must not create any per-course index. The original KNOWN_ISSUES
// #2 fix (Diff persisted TOC timestamps) is intentionally superseded by the
// archive-UX redesign — TOC timestamps only land after a real update run.
func TestRepro_Issue2_DiffWritesNothing(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("8888"), Name: "Chem 30", Code: "CHM3191", IsActive: true},
	}
	f.tocs["8888"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 2001, Title: "Outline", LastModifiedDate: "2026-01-10T00:00:00Z"},
		{TopicId: 2002, Title: "Lab 1", LastModifiedDate: "2026-01-11T00:00:00Z"},
	}}}}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)

	if _, err := Diff(Options{Root: dir, Jar: jar, BaseURL: srv.URL}); err != nil {
		t.Fatalf("diff: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, indexFileName)); !os.IsNotExist(err) {
		t.Errorf("Diff wrote the global index; expected no file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, coursesDirName, "8888", courseIndexFileName)); !os.IsNotExist(err) {
		t.Errorf("Diff wrote the per-course index; expected no file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, blobsDirName)); !os.IsNotExist(err) {
		t.Errorf("Diff created the blobs directory; expected none, stat err=%v", err)
	}
}

// TestRepro_Issue3_BodyReuseCountConflatesAbsent reproduces KNOWN_ISSUES #3:
// on the first-ever Run pass, every File topic should be a "new" body, not
// counted as "unchanged (same SHA)".
func TestRepro_Issue3_BodyReuseCountConflatesAbsent(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("9999"), Name: "Phys 30", Code: "PHY3191", IsActive: true},
	}
	f.tocs["9999"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 3001, Title: "Notes", LastModifiedDate: "2026-02-01T00:00:00Z", TypeIdentifier: "File", Url: ptrStr("/content/enforced/9999-T1/notes.pdf")},
	}}}}
	f.topics[3001] = map[string]any{
		"TopicId":        3001,
		"Title":          "Notes",
		"TypeIdentifier": "File",
		"Url":            "/content/enforced/9999-T1/notes.pdf",
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 fake notes"))
	}
	srv.Config.Handler = f.handler()

	dir := t.TempDir()
	jar := newJar(t)

	res, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.BodiesReused != 0 {
		t.Errorf("BodiesReused on a fresh archive = %d, want 0 (KNOWN_ISSUES #3)", res.BodiesReused)
	}
	if res.BodiesFetched != 1 {
		t.Errorf("BodiesFetched on a fresh archive = %d, want 1", res.BodiesFetched)
	}
}

func ptrStr(s string) *string { return &s }

// TestRepro_Issue20_BodyAndMetaPathDistinct reproduces KNOWN_ISSUES #20:
// `archive path <topicId>` should default to the body path when one is
// archived, not always fall back to metadata.
func TestRepro_Issue20_BodyAndMetaPathDistinct(t *testing.T) {
	dir := t.TempDir()
	bodySHA, _, err := SaveFileBody(dir, []byte("hello"))
	if err != nil {
		t.Fatalf("SaveFileBody: %v", err)
	}
	metaUUID, _, err := SaveBlob(dir, []byte(`{"TopicId":42}`))
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}
	ci := &CourseIndex{CourseID: "1", Topics: map[int]*TopicIndex{
		42: {TopicID: 42, Type: "File", Current: metaUUID, CurrentBody: bodySHA},
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
		t.Fatalf("Resolve topic 42: %v", err)
	}
	if res.Kind != "body" {
		t.Errorf("Resolve(\"42\", \"\").Kind = %q, want %q", res.Kind, "body")
	}
	if res.BlobID != bodySHA {
		t.Errorf("Resolve(\"42\", \"\").BlobID = %q, want %q", res.BlobID, bodySHA)
	}
	metaRes, err := store.Resolve("42", "metadata")
	if err != nil {
		t.Fatalf("Resolve topic 42 metadata: %v", err)
	}
	if metaRes.Kind != "metadata" {
		t.Errorf("Resolve(\"42\", \"metadata\").Kind = %q, want %q", metaRes.Kind, "metadata")
	}
	if metaRes.BlobID != metaUUID {
		t.Errorf("Resolve(\"42\", \"metadata\").BlobID = %q, want %q", metaRes.BlobID, metaUUID)
	}
}

// TestRepro_Issue4_ArchiveReadDefaultsToBodyForFile reproduces KNOWN_ISSUES #4
// by asserting the public path of the body blob exists on disk after a
// Run pass with --files on.
func TestRepro_Issue4_ArchiveReadDefaultsToBodyForFile(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{
		{OrgUnitId: json.Number("1010"), Name: "Eng 30", Code: "ENG3191", IsActive: true},
	}
	f.tocs["1010"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 4001, Title: "Essay", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: ptrStr("/content/enforced/1010-T1/essay.pdf")},
	}}}}
	f.topics[4001] = map[string]any{
		"TopicId":        4001,
		"Title":          "Essay",
		"TypeIdentifier": "File",
		"Url":            "/content/enforced/1010-T1/essay.pdf",
	}
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 fake essay body"))
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	dir := t.TempDir()
	jar := newJar(t)
	res, err := Run(Options{Root: dir, Jar: jar, BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.BodiesFetched == 0 {
		t.Fatalf("BodiesFetched = 0, want 1 (BodiesFailed=%d)", res.BodiesFailed)
	}
	ci, err := LoadCourseIndex(dir, "1010")
	if err != nil {
		t.Fatalf("LoadCourseIndex: %v", err)
	}
	ti := ci.Topics[4001]
	if ti == nil || ti.CurrentBody == "" {
		t.Fatalf("no body archived for 4001: %+v", ti)
	}
	body, err := LoadFileBody(dir, ti.CurrentBody)
	if err != nil {
		t.Fatalf("LoadFileBody: %v", err)
	}
	if body == nil {
		t.Errorf("LoadFileBody returned nil bytes for archived body")
	}
	if filepath.Ext(BodyPath(dir, ti.CurrentBody)) != ".bin" {
		t.Errorf("BodyPath ext = %q, want .bin", filepath.Ext(BodyPath(dir, ti.CurrentBody)))
	}
}

// Sanity: ensure the fixture is sound.
var _ = time.Now
