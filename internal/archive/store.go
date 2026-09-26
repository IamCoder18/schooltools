package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TopicRef struct {
	CourseID   string
	CourseName string
	CourseCode string
	Topic      *TopicIndex
}

type Resolved struct {
	Kind     string
	BlobID   string
	BlobPath string
	Ref      TopicRef
	TopicID  int
}

type SearchQuery struct {
	Terms         string
	Ext           string
	CourseIDs     []string
	Type          string
	WithBodies    bool
	MissingBodies bool
	Limit         int
}

type SearchHit struct {
	CourseID     string    `json:"courseId"`
	CourseName   string    `json:"courseName,omitempty"`
	CourseCode   string    `json:"courseCode,omitempty"`
	TopicID      int       `json:"topicId"`
	Title        string    `json:"title,omitempty"`
	Type         string    `json:"type,omitempty"`
	URL          string    `json:"url,omitempty"`
	LastModified time.Time `json:"lastModified,omitempty"`
	Current      string    `json:"current,omitempty"`
	CurrentBody  string    `json:"currentBody,omitempty"`
	HasBody      bool      `json:"hasBody"`
}

type topicEntry struct {
	ref TopicRef
	hay string
	ext string
}

type Store struct {
	Root    string
	Index   *Index
	Courses map[string]*CourseIndex
	byTopic map[int]*topicEntry
	byMeta  map[string]*topicEntry
	byBody  map[string]*topicEntry
	entries []*topicEntry
}

func OpenStore(root string) (*Store, error) {
	idx, err := LoadIndex(root)
	if err != nil {
		return nil, err
	}
	s := &Store{
		Root:    root,
		Index:   idx,
		Courses: make(map[string]*CourseIndex, len(idx.Courses)),
		byTopic: map[int]*topicEntry{},
		byMeta:  map[string]*topicEntry{},
		byBody:  map[string]*topicEntry{},
	}

	known := make(map[string]struct{}, len(idx.Courses))
	for cid := range idx.Courses {
		known[cid] = struct{}{}
	}
	dir := filepath.Join(root, coursesDirName)
	if entries, derr := os.ReadDir(dir); derr == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, ok := known[e.Name()]; ok {
				continue
			}
			idx.Courses[e.Name()] = &IndexEntry{OrgUnitId: e.Name()}
			known[e.Name()] = struct{}{}
		}
	}

	for _, cid := range SortedCourseIDs(idx) {
		ci, err := LoadCourseIndex(root, cid)
		if err != nil {
			return nil, err
		}
		s.Courses[cid] = ci
		var ids []int
		for id := range ci.Topics {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			t := ci.Topics[id]
			if t == nil {
				continue
			}
			entry := &topicEntry{
				ref: TopicRef{
					CourseID:   ci.CourseID,
					CourseName: ci.Name,
					CourseCode: ci.Code,
					Topic:      t,
				},
				hay: strings.ToLower(t.Title + " " + t.URL + " " + t.Type),
				ext: strings.ToLower(strings.TrimPrefix(filepath.Ext(t.URL), ".")),
			}
			s.entries = append(s.entries, entry)
			if _, exists := s.byTopic[id]; !exists {
				s.byTopic[id] = entry
			}
			for _, v := range t.Versions {
				if v.UUID == "" {
					continue
				}
				if _, exists := s.byMeta[v.UUID]; !exists {
					s.byMeta[v.UUID] = entry
				}
			}
			for _, b := range t.BodyVersions {
				if b.SHA256 == "" {
					continue
				}
				if _, exists := s.byBody[b.SHA256]; !exists {
					s.byBody[b.SHA256] = entry
				}
			}
		}
	}
	return s, nil
}

func (s *Store) ByTopicID(id int) (TopicRef, bool) {
	e, ok := s.byTopic[id]
	if !ok || e == nil {
		return TopicRef{}, false
	}
	return e.ref, true
}

func (s *Store) Resolve(ref, kind string) (Resolved, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Resolved{}, fmt.Errorf("empty ref")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" && kind != "metadata" && kind != "body" {
		return Resolved{}, fmt.Errorf("unknown --kind %q (want metadata or body)", kind)
	}

	if isTopicID(ref) {
		id, _ := strconv.Atoi(ref)
		te, ok := s.byTopic[id]
		if !ok {
			return Resolved{}, fmt.Errorf("topic %d not found in archive at %s", id, s.Root)
		}
		return s.resolveFromEntry(te, kind), nil
	}

	if kind == "metadata" || kind == "" {
		if len(ref) == 32 && isHex(ref) {
			if te, ok := s.byMeta[ref]; ok {
				return Resolved{
					Kind: "metadata", BlobID: ref,
					BlobPath: MetadataBlobPath(s.Root, ref),
					Ref:      te.ref, TopicID: te.ref.Topic.TopicID,
				}, nil
			}
			if fileExists(MetadataBlobPath(s.Root, ref)) {
				return Resolved{
					Kind: "metadata", BlobID: ref,
					BlobPath: MetadataBlobPath(s.Root, ref),
				}, nil
			}
			return Resolved{}, fmt.Errorf("metadata blob %s not found (no topic in the index references it)", ref)
		}
	}

	if kind == "body" || kind == "" {
		if len(ref) == 64 && isHex(ref) {
			if te, ok := s.byBody[ref]; ok {
				return Resolved{
					Kind: "body", BlobID: ref,
					BlobPath: BodyPath(s.Root, ref),
					Ref:      te.ref, TopicID: te.ref.Topic.TopicID,
				}, nil
			}
			if fileExists(BodyPath(s.Root, ref)) {
				return Resolved{
					Kind: "body", BlobID: ref,
					BlobPath: BodyPath(s.Root, ref),
				}, nil
			}
			return Resolved{}, fmt.Errorf("body blob %s not found (no topic in the index references it)", ref)
		}
	}

	return Resolved{}, fmt.Errorf("unrecognised ref %q (want numeric topicId, 32-hex metadata uuid, or 64-hex body sha256)", ref)
}

func (s *Store) resolveFromEntry(te *topicEntry, kind string) Resolved {
	t := te.ref.Topic
	switch kind {
	case "metadata":
		return Resolved{
			Kind: "metadata", BlobID: t.Current,
			BlobPath: MetadataBlobPath(s.Root, t.Current),
			Ref:      te.ref, TopicID: t.TopicID,
		}
	case "body":
		return Resolved{
			Kind: "body", BlobID: t.CurrentBody,
			BlobPath: BodyPath(s.Root, t.CurrentBody),
			Ref:      te.ref, TopicID: t.TopicID,
		}
	}
	if t.Type == "File" && t.CurrentBody != "" {
		return Resolved{
			Kind: "body", BlobID: t.CurrentBody,
			BlobPath: BodyPath(s.Root, t.CurrentBody),
			Ref:      te.ref, TopicID: t.TopicID,
		}
	}
	return Resolved{
		Kind: "metadata", BlobID: t.Current,
		BlobPath: MetadataBlobPath(s.Root, t.Current),
		Ref:      te.ref, TopicID: t.TopicID,
	}
}

func (s *Store) Search(q SearchQuery) []SearchHit {
	wantExt := strings.ToLower(strings.TrimPrefix(q.Ext, "."))
	tokens := tokenize(q.Terms)
	courseFilter := make(map[string]struct{}, len(q.CourseIDs))
	for _, c := range q.CourseIDs {
		c = strings.TrimSpace(c)
		if c != "" {
			courseFilter[c] = struct{}{}
		}
	}
	out := make([]SearchHit, 0)
	for _, te := range s.entries {
		t := te.ref.Topic
		if t == nil {
			continue
		}
		if wantExt != "" && te.ext != wantExt {
			continue
		}
		if q.Type != "" && t.Type != q.Type {
			continue
		}
		if len(courseFilter) > 0 {
			if _, ok := courseFilter[te.ref.CourseID]; !ok {
				continue
			}
		}
		if q.WithBodies && t.CurrentBody == "" {
			continue
		}
		if q.MissingBodies {
			isFileCompatible := t.Type == "" || t.Type == "File"
			if !isFileCompatible || t.CurrentBody != "" {
				continue
			}
		}
		if len(tokens) > 0 && !te.hayMatches(tokens) {
			continue
		}
		out = append(out, SearchHit{
			CourseID:     te.ref.CourseID,
			CourseName:   te.ref.CourseName,
			CourseCode:   te.ref.CourseCode,
			TopicID:      t.TopicID,
			Title:        t.Title,
			Type:         t.Type,
			URL:          t.URL,
			LastModified: t.LastModified,
			Current:      t.Current,
			CurrentBody:  t.CurrentBody,
			HasBody:      t.CurrentBody != "",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CourseID != out[j].CourseID {
			return out[i].CourseID < out[j].CourseID
		}
		return out[i].TopicID < out[j].TopicID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

func (te *topicEntry) hayMatches(tokens []string) bool {
	for _, tok := range tokens {
		if !strings.Contains(te.hay, tok) {
			return false
		}
	}
	return true
}

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	fields := strings.Fields(s)
	return fields
}

func isTopicID(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

type Status struct {
	Root          string    `json:"root"`
	Exists        bool      `json:"exists"`
	SchemaVersion int       `json:"schemaVersion"`
	StoreVersion  int       `json:"storeVersion"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Courses       int       `json:"courses"`
	Topics        int       `json:"topics"`
	FileTopics    int       `json:"fileTopics"`
	BodiesPresent int       `json:"bodiesPresent"`
	MissingBodies int       `json:"missingBodies"`
	MetaBlobs     int       `json:"metaBlobs"`
	BodyBlobs     int       `json:"bodyBlobs"`
	MetaBytes     int64     `json:"metaBytes"`
	BodyBytes     int64     `json:"bodyBytes"`
	LockHeld      bool      `json:"lockHeld"`
}

func LoadStatus(root string) (Status, error) {
	st := Status{Root: root, SchemaVersion: SchemaVersion}
	_, idxErr := os.Stat(filepath.Join(root, indexFileName))
	indexExists := idxErr == nil
	st.Exists = indexExists
	var idx *Index
	if indexExists {
		var err error
		idx, err = LoadIndex(root)
		if err != nil {
			return st, err
		}
		st.StoreVersion = idx.Version
		st.UpdatedAt = idx.UpdatedAt
	} else {
		idx = &Index{Version: SchemaVersion, Courses: map[string]*IndexEntry{}}
	}

	courseIDs := SortedCourseIDs(idx)
	coursesDir := filepath.Join(root, coursesDirName)
	if entries, err := os.ReadDir(coursesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			found := false
			for _, c := range courseIDs {
				if c == e.Name() {
					found = true
					break
				}
			}
			if !found {
				courseIDs = append(courseIDs, e.Name())
			}
		}
	}
	st.Courses = len(courseIDs)

	for _, cid := range courseIDs {
		ci, err := LoadCourseIndex(root, cid)
		if err != nil {
			return st, err
		}
		st.Topics += len(ci.Topics)
		for _, t := range ci.Topics {
			if t == nil {
				continue
			}
			if t.Type == "" || t.Type == "File" {
				st.FileTopics++
			}
			if t.CurrentBody != "" {
				st.BodiesPresent++
			}
		}
	}
	st.MissingBodies = st.FileTopics - st.BodiesPresent

	blobsDir := filepath.Join(root, blobsDirName)
	if entries, err := os.ReadDir(blobsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			name := e.Name()
			switch {
			case strings.HasSuffix(name, blobExt):
				st.MetaBlobs++
				st.MetaBytes += info.Size()
			case strings.HasSuffix(name, bodyExt):
				st.BodyBlobs++
				st.BodyBytes += info.Size()
			}
		}
	}

	st.LockHeld = lockHeldAt(root)
	return st, nil
}

func lockHeldAt(root string) bool {
	return lockHeldByOther(root)
}
