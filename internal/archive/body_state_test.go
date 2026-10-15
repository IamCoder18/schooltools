package archive

import (
	"crypto/sha256"
	"encoding/hex"
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

	topics, err := FileTopics(dir, "")
	if err != nil {
		t.Fatalf("FileTopics: %v", err)
	}
	for _, ft := range topics {
		want := ft.TopicID == 1
		if ft.HasBody != want {
			t.Errorf("topic %d: HasBody=%v, want %v", ft.TopicID, ft.HasBody, want)
		}
	}
}
