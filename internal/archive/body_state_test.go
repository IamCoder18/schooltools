package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadStatusCountsBodiesMissingFromDisk(t *testing.T) {
	dir := t.TempDir()
	_, _, _ = SaveFileBody(dir, []byte("body"))
	presentHash := sha256.Sum256([]byte("body"))
	presentHex := hex.EncodeToString(presentHash[:])
	missingHash := sha256.Sum256([]byte("gone"))
	missingHex := hex.EncodeToString(missingHash[:])

	ci := &CourseIndex{CourseID: "1", Code: "SAF", Topics: map[int]*TopicIndex{
		1: {TopicID: 1, Title: "on disk", Type: "File", URL: "/x.pdf", CurrentBody: presentHex, LastModified: time.Now().UTC()},
		2: {TopicID: 2, Title: "pointer only", Type: "File", URL: "/y.pdf", CurrentBody: missingHex, LastModified: time.Now().UTC()},
		3: {TopicID: 3, Title: "no pointer", Type: "File", URL: "/z.pdf", CurrentBody: "", LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	st, err := LoadStatus(dir)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if st.FileTopics != 3 {
		t.Errorf("FileTopics=%d, want 3", st.FileTopics)
	}
	if st.BodiesPresent != 1 {
		t.Errorf("BodiesPresent=%d, want 1 (only the blob that exists on disk)", st.BodiesPresent)
	}
	if st.MissingBodies != 2 {
		t.Errorf("MissingBodies=%d, want 2 (pointer-without-blob plus empty pointer)", st.MissingBodies)
	}
}

// A stat failure that is not not-existence (here: the blobs directory is
// a regular file, so stat returns ENOTDIR) must not read as "missing":
// the status/search paths stay quiet, while `archive verify` reports it.
func TestStatErrorsAreNotMissingButVerifyReportsThem(t *testing.T) {
	dir := t.TempDir()
	if err := SaveIndex(dir, &Index{Version: SchemaVersion, Courses: map[string]*IndexEntry{
		"1": {OrgUnitId: "1", Code: "SAF"},
	}}); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	hash := sha256.Sum256([]byte("body"))
	hexSum := hex.EncodeToString(hash[:])
	ci := &CourseIndex{CourseID: "1", Code: "SAF", Topics: map[int]*TopicIndex{
		1: {TopicID: 1, Title: "unstatable", Type: "File", URL: "/a.pdf", CurrentBody: hexSum, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, blobsDirName)); err != nil {
		t.Fatalf("remove blobs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, blobsDirName), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace blobs dir with a file: %v", err)
	}

	st, err := LoadStatus(dir)
	if err != nil {
		t.Fatalf("LoadStatus: %v", err)
	}
	if st.MissingBodies != 0 || st.BodiesPresent != 1 {
		t.Errorf("BodiesPresent=%d MissingBodies=%d, want 1/0 (an unstattable blob is not a missing body)",
			st.BodiesPresent, st.MissingBodies)
	}
	topics, err := FileTopics(dir, "")
	if err != nil {
		t.Fatalf("FileTopics: %v", err)
	}
	if len(topics) != 1 || !topics[0].HasBody {
		t.Errorf("FileTopics HasBody = %+v, want true (stat error must not read as missing)", topics)
	}

	res, err := Verify(dir, false)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, is := range res.Issues {
		if is.Code == "missing-body-blob" {
			t.Errorf("verify reported a missing body for an unstattable blob: %+v", is)
		}
		if is.Code == "unreadable-body-blob" {
			found = true
		}
	}
	if !found {
		t.Errorf("verify did not report unreadable-body-blob; issues: %+v", res.Issues)
	}
	if res.OK {
		t.Error("verify must not pass a store whose blobs cannot be statted")
	}
}

func TestSearchBodyFiltersCheckDisk(t *testing.T) {
	dir := t.TempDir()
	_, _, _ = SaveFileBody(dir, []byte("body"))
	presentHash := sha256.Sum256([]byte("body"))
	presentHex := hex.EncodeToString(presentHash[:])
	missingHash := sha256.Sum256([]byte("gone"))
	missingHex := hex.EncodeToString(missingHash[:])

	ci := &CourseIndex{CourseID: "1", Code: "SAF", Topics: map[int]*TopicIndex{
		1: {TopicID: 1, Title: "present", Type: "File", URL: "/a.pdf", CurrentBody: presentHex, LastModified: time.Now().UTC()},
		2: {TopicID: 2, Title: "dangling", Type: "File", URL: "/b.pdf", CurrentBody: missingHex, LastModified: time.Now().UTC()},
		3: {TopicID: 3, Title: "empty", Type: "File", URL: "/c.pdf", CurrentBody: "", LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	hits := store.Search(SearchQuery{WithBodiesOnDisk: true})
	if len(hits) != 1 || hits[0].TopicID != 1 {
		t.Errorf("WithBodiesOnDisk: got %+v, want only topic 1", hits)
	}

	hits = store.Search(SearchQuery{WithBodies: true})
	if len(hits) != 2 {
		t.Errorf("WithBodies (pointer semantics): got %d hits, want 2 (pointer set)", len(hits))
	}

	hits = store.Search(SearchQuery{MissingBodies: true})
	if len(hits) != 2 || hits[0].TopicID != 2 || hits[1].TopicID != 3 {
		t.Errorf("MissingBodies: got %+v, want topics 2 and 3 (dangling pointer counts as missing)", hits)
	}

	if !store.Search(SearchQuery{Terms: "present"})[0].HasBody {
		t.Error("HasBody should be true when the blob is on disk")
	}
	if store.Search(SearchQuery{Terms: "dangling"})[0].HasBody {
		t.Error("HasBody should be false when the blob is missing from disk")
	}
}

func TestFileTopicsHasBodyChecksDisk(t *testing.T) {
	dir := t.TempDir()
	_, _, _ = SaveFileBody(dir, []byte("body"))
	presentHash := sha256.Sum256([]byte("body"))
	presentHex := hex.EncodeToString(presentHash[:])
	missingHash := sha256.Sum256([]byte("gone"))
	missingHex := hex.EncodeToString(missingHash[:])

	ci := &CourseIndex{CourseID: "1", Code: "SAF", Topics: map[int]*TopicIndex{
		1: {TopicID: 1, Title: "present", Type: "File", URL: "/a.pdf", CurrentBody: presentHex, LastModified: time.Now().UTC()},
		2: {TopicID: 2, Title: "dangling", Type: "File", URL: "/b.pdf", CurrentBody: missingHex, LastModified: time.Now().UTC()},
	}}
	if err := SaveCourseIndex(dir, "1", ci); err != nil {
		t.Fatalf("SaveCourseIndex: %v", err)
	}
	if err := SaveIndex(dir, &Index{Version: SchemaVersion, Courses: map[string]*IndexEntry{
		"1": {OrgUnitId: "1", Code: "SAF"},
	}}); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	topics, err := FileTopics(dir, "")
	if err != nil {
		t.Fatalf("FileTopics: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("FileTopics returned %d topics, want 2", len(topics))
	}
	for _, ft := range topics {
		want := ft.TopicID == 1
		if ft.HasBody != want {
			t.Errorf("topic %d: HasBody=%v, want %v", ft.TopicID, ft.HasBody, want)
		}
	}
}
