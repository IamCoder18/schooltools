// Package archive snapshots a user's D2L courses into a directory tree rooted
// at a single global location (default: ~/.config/schooltools/archive/).
//
// # On-disk layout
//
//	~/.config/schooltools/archive/
//	  index.json                       # global manifest: courses + per-course counters
//	  blobs/<uuid>.json                # every metadata document, flat, named by UUID
//	  blobs/<sha256>.bin               # every file body (PDF/DOCX/HTML/etc.), flat, content-addressed
//	  courses/<courseId>/
//	    index.json                     # per-course: topicId -> { title, lastModified,
//	                                  #               current: <metadata uuid>,
//	                                  #               versions: [metadata versions],
//	                                  #               currentBody: <sha256>,
//	                                  #               bodyVersions: [body versions] }
//
// All blobs are stored in a single flat pool under blobs/. Metadata blobs
// are named by random UUIDs (32-char hex); file-body blobs are content-
// addressed by SHA-256 (64-char hex) so two topics pointing at the same
// underlying file (D2L reuses attachments across modules) automatically
// share a single copy on disk.
// blob via a UUID, and keeps an append-only history of every version we have
// ever saved for that topic.
//
// File bodies (the underlying PDFs / DOCX / HTML that D2L serves from the
// `/content/enforced/...` URL) are archived in parallel: each File
// topic's body is downloaded, hashed (SHA-256), and stored at
// `blobs/<sha256>.bin` keyed by hash. This deduplicates identically-
// referenced files across modules and runs; a new version is recorded
// only when the SHA differs from the one we already saved for the same
// topic. We can't avoid re-downloading for hash detection (D2L exposes
// no body-side LastModifiedDate), but the SHA step itself is cheap.
//
// # Incremental pass
//
// An archive pass has four stages, designed to minimise API requests on
// repeat runs:
//
//  1. Global index — index.json lists the courses we have seen, by id, with
//     their last TOC fetch timestamp. We rewrite this once at the start so
//     downstream tools can tell what's covered.
//  2. Per-course TOC — for each course we fetch the table-of-contents from
//     /d2l/api/le/<ver>/<id>/content/toc and compare every topic's
//     LastModifiedDate against the saved per-course index. Only topics whose
//     date moved forward (or are new) are queued for download.
//  3. Topic documents — for each queued topic we hit
//     /d2l/api/le/<ver>/<id>/content/topics/<topicId> and write the response
//     body verbatim into a brand-new blob under blobs/<uuid>.json. The old
//     blob is left on disk and the topic's Version history grows; nothing is
//     ever overwritten. Prune (see below) reclaims the old blobs.
//  4. File bodies (File topics only) — when `--files` is on, we additionally
//     download the underlying file from the `Url` recorded in the topic
//     metadata, compute the SHA-256, and either store a brand-new blob at
//     `blobs/<sha256>.bin` (new/changed) or skip it (unchanged). New versions
//     are recorded in `bodyVersions`; the latest one is exposed as
//     `currentBody`.
//
// # Prune
//
// Prune walks every per-course index.json, builds the set of "live" UUIDs
// (one per topic — the current pointer), and deletes every other blob. This
// drops the full version history while leaving each topic's latest document
// intact.
package archive

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aarav/schooltools/internal/content"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ua"
)

const (
	// SchemaVersion is bumped when the on-disk layout changes in a way that
	// would confuse older readers. v3 added bodies (blobs/<sha256>.bin) and
	// per-topic body-version tracking.
	SchemaVersion = 3
	// indexFileName is the global manifest.
	indexFileName = "index.json"
	// blobsDirName holds every document blob, named only by UUID.
	blobsDirName = "blobs"
	// coursesDirName holds one subdirectory per course.
	coursesDirName = "courses"
	// courseIndexFileName is the per-course diff driver + blob pointer map.
	courseIndexFileName = "index.json"
	// blobExt is the on-disk extension for metadata blobs. Files contain
	// raw or pretty-printed JSON from the D2L /content/topics/<id>
	// endpoint.
	blobExt = ".json"
	// bodyExt is the on-disk extension for file-body blobs. These are
	// the raw bytes returned by the URL embedded in a File topic's
	// metadata (typically /content/enforced/...). Named by SHA-256 so
	// deduplication is automatic.
	bodyExt = ".bin"
)

// Course represents a single D2L course. Mirrors the subset of fields we care
// about from the manageCourses API.
type Course struct {
	OrgUnitId string `json:"OrgUnitId"`
	Name      string `json:"Name"`
	Code      string `json:"Code"`
	IsActive  bool   `json:"IsActive"`
}

// IndexEntry records, for one course, when its TOC was last fetched. We only
// keep a tiny summary here — the canonical per-course state lives in
// courses/<id>/index.json.
type IndexEntry struct {
	OrgUnitId    string    `json:"OrgUnitId"`
	Name         string    `json:"Name"`
	Code         string    `json:"Code"`
	IsActive     bool      `json:"IsActive"`
	TocFetchedAt time.Time `json:"TocFetchedAt"`
	TopicsTotal  int       `json:"TopicsTotal"`
	TopicsNew    int       `json:"TopicsNew"`
	TopicsStale  int       `json:"TopicsStale"`
}

// Index is the global archive manifest.
type Index struct {
	Version   int                    `json:"version"`
	UpdatedAt time.Time              `json:"updatedAt"`
	Courses   map[string]*IndexEntry `json:"courses"`
}

// TopicVersion is one snapshot of a topic's document. Versions are
// append-only: when D2L reports an updated LastModifiedDate we save a new
// blob with a fresh UUID and append an entry here. Old blobs are kept until
// Prune removes them.
// TopicVersion records a saved copy of the topic metadata document.
// The blob lives at blobs/<uuid>.json so the actual D2L JSON is
// preserved verbatim — re-decoding it after a year from now still works
// because the response shape is stable.
type TopicVersion struct {
	UUID         string    `json:"uuid"`
	LastModified time.Time `json:"lastModified"`
	Size         int64     `json:"size"`
	SavedAt      time.Time `json:"savedAt"`
}

// BodyVersion records one saved copy of the underlying file bytes for
// a File topic. The blob lives at blobs/<sha256>.bin so two topics
// pointing at the same file (D2L reuses attachments across modules)
// share a single copy on disk.
//
// We hash to dedupe because D2L doesn't expose a body-side
// LastModifiedDate. Detecting a body change requires re-downloading;
// the SHA is then computed locally and compared against the stored
// one to decide whether to record a new version.
//
// DownloadedAt is when the file was fetched from D2L. ContentType is
// the HTTP header D2L sent; empty when missing.
type BodyVersion struct {
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"contentType,omitempty"`
	DownloadedAt time.Time `json:"downloadedAt"`
	// LastModified is the topic's metadata LastModifiedDate at the
	// moment this body was downloaded — useful when triaging archive
	// changes (does the body we have match the metadata revision we
	// archived?).
	LastModified time.Time `json:"lastModified,omitempty"`
}

// TopicIndex is the per-topic record kept in CourseIndex.Topics. It maps a
// D2L TopicId to the latest known document blob (Current) and the full
// history of versions we have ever saved for it. For File topics,
// CurrentBody + BodyVersions record the underlying file bytes (PDF /
// DOCX / HTML) under a content-addressed SHA-256 blob.
type TopicIndex struct {
	TopicID      int            `json:"topicId"`
	Title        string         `json:"title,omitempty"`
	Type         string         `json:"type,omitempty"`
	URL          string         `json:"url,omitempty"`
	LastModified time.Time      `json:"lastModified"` // matches the latest Versions entry
	Current      string         `json:"current"`      // UUID of the latest metadata blob
	Versions     []TopicVersion `json:"versions"`

	// File-body tracking. Empty for non-File topics.
	CurrentBody  string        `json:"currentBody,omitempty"`  // SHA-256 of latest body
	BodyVersions []BodyVersion `json:"bodyVersions,omitempty"` // body-version history (newest at end)
}

// CourseIndex is the per-course diff driver + blob pointer map. It is the
// single source of truth for what we have archived from a course: every
// topic's Latest D2L LastModifiedDate lives here, and every Version points
// at an immutable blob in blobs/.
type CourseIndex struct {
	CourseID  string              `json:"courseId"`
	Name      string              `json:"name,omitempty"`
	Code      string              `json:"code,omitempty"`
	IsActive  bool                `json:"isActive"`
	UpdatedAt time.Time           `json:"updatedAt"`
	Topics    map[int]*TopicIndex `json:"topics"`
}

// Result summarises one archive pass for human display.
type Result struct {
	Root            string
	CoursesScanned  int
	CoursesSkipped  int
	TopicsChecked   int
	TopicsFetched   int
	TopicsStale     int
	BodiesFetched   int // File topics whose body blob was newly saved (SHA changed or first-ever)
	BodiesReused    int // File topics whose body matched the saved hash — copy skipped
	BodiesFailed    int // File topics whose body download errored (metadata still saved)
}

// DiffResult is the structured output of a Diff pass — what would a Run
// pass do, without actually fetching any topic bodies or writing any state.
type DiffResult struct {
	Root            string
	CoursesScanned  int
	TopicsChecked   int
	TopicsWouldFetch int
	TopicsStale     int
	PerCourse       []CourseDiff
}

// CourseDiff summarises a single course's planned work.
type CourseDiff struct {
	CourseID        string
	CourseName      string
	CourseCode      string
	TopicsTotal     int
	TopicsNew       int
	TopicsModified  int
	TopicsUnchanged int
	WouldFetchIDs   []int
}

// PruneResult summarises a Prune pass.
type PruneResult struct {
	Root         string
	BlobsBefore  int
	BlobsAfter   int
	Deleted      int
	BytesReclaim int64
	OrphanUUIDs  []string // blobs not referenced by any course index
}

// ProgressFunc lets Run stream activity updates to a UI without coupling to
// any particular rendering library. Implementations should be cheap and
// non-blocking; they may be called from the goroutine that Run is on.
type ProgressFunc func(event ProgressEvent)

// ProgressEvent describes a single tick of activity from Run.
type ProgressEvent struct {
	Phase                                  string
	CourseID, CourseName                   string
	TopicID                                int
	Reason                                 string
	Checked, Fetched, Stale                int
	Done, Total                            int
}

// Options bundles inputs to Run.
type Options struct {
	Root          string         // base directory; created if missing
	Jar           *cookiejar.Jar // authenticated session
	BaseURL       string         // D2L base URL; defaults to ua.D2LBase. Tests override.
	Verbose       bool           // print per-course progress to stderr
	Quiet         bool           // suppress the human summary on stdout
	Progress      ProgressFunc   // optional; receives ProgressEvents
	CourseIDs     []string       // when non-empty, restrict to these course ids
	Now           func() time.Time
	DownloadFiles bool           // when true, also download each File topic's underlying bytes and hash-store them in blobs/<sha256>.bin (default: false for backwards-compat)
}

// baseURL returns the effective D2L base, defaulting to ua.D2LBase.
func (o Options) baseURL() string {
	if o.BaseURL != "" {
		return o.BaseURL
	}
	return ua.D2LBase
}

// coursesAPIURLFor builds the manageCourses URL against the given base.
func coursesAPIURLFor(base string) string {
	return base +
		"/d2l/le/manageCourses/api/mycourses" +
		"?pageSize=20&sort=current&autoPinCourses=false" +
		"&orgUnitTypeId=3&promotePins=true&embedDepth=0&widgetId=287426"
}

// tocURLFor builds the TOC URL for a course against the given base.
func tocURLFor(base, courseID string) string {
	return base + fmt.Sprintf("/d2l/api/le/%s/%s/content/toc", content.LEVersion(), courseID)
}

// topicURLFor builds the per-topic URL against the given base.
func topicURLFor(base, courseID string, topicID int) string {
	return base + fmt.Sprintf("/d2l/api/le/%s/%s/content/topics/%d", content.LEVersion(), courseID, topicID)
}

// DefaultRoot returns the canonical archive directory:
// ~/.config/schooltools/archive.
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "schooltools", "archive")
}

// ---- public storage API ---------------------------------------------------

// LoadIndex reads the global index.json from root, returning an empty Index
// when the file is missing.
func LoadIndex(root string) (*Index, error) {
	path := filepath.Join(root, indexFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{Version: SchemaVersion, Courses: map[string]*IndexEntry{}}, nil
		}
		return nil, fmt.Errorf("archive: read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("archive: parse index: %w", err)
	}
	if idx.Courses == nil {
		idx.Courses = map[string]*IndexEntry{}
	}
	return &idx, nil
}

// SaveIndex writes index.json atomically (write to temp, rename).
func SaveIndex(root string, idx *Index) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("archive: mkdir root: %w", err)
	}
	return writeJSONAtomic(filepath.Join(root, indexFileName), idx)
}

// LoadCourseIndex reads courses/<id>/index.json. Returns a fresh empty
// CourseIndex (with Topics map initialised) when the file is missing.
func LoadCourseIndex(root, courseID string) (*CourseIndex, error) {
	path := filepath.Join(root, coursesDirName, safeID(courseID), courseIndexFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &CourseIndex{
				CourseID: courseID,
				Topics:   map[int]*TopicIndex{},
			}, nil
		}
		return nil, fmt.Errorf("archive: read course index: %w", err)
	}
	var ci CourseIndex
	if err := json.Unmarshal(data, &ci); err != nil {
		return nil, fmt.Errorf("archive: parse course index: %w", err)
	}
	if ci.Topics == nil {
		ci.Topics = map[int]*TopicIndex{}
	}
	ci.CourseID = courseID
	return &ci, nil
}

// SaveCourseIndex writes the per-course index atomically.
func SaveCourseIndex(root, courseID string, ci *CourseIndex) error {
	dir := filepath.Join(root, coursesDirName, safeID(courseID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("archive: mkdir course: %w", err)
	}
	return writeJSONAtomic(filepath.Join(dir, courseIndexFileName), ci)
}

// SaveBlob writes data into blobs/<uuid><ext>. Returns the uuid, on-disk
// size, and any error. The destination directory is created if missing.
func SaveBlob(root string, data []byte) (string, int64, error) {
	uuid, err := NewUUID()
	if err != nil {
		return "", 0, fmt.Errorf("archive: uuid: %w", err)
	}
	dir := filepath.Join(root, blobsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("archive: mkdir blobs: %w", err)
	}
	path := filepath.Join(dir, uuid+blobExt)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", 0, fmt.Errorf("archive: write blob: %w", err)
	}
	return uuid, int64(len(data)), nil
}

// LoadBlob reads blobs/<uuid><ext>. Returns (nil, nil) when missing.
func LoadBlob(root, uuid string) ([]byte, error) {
	if uuid == "" {
		return nil, nil
	}
	path := filepath.Join(root, blobsDirName, uuid+blobExt)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// BodyPath returns the absolute path of a saved file body keyed by its
// SHA-256 hash. Returns "" when the hash is empty. The path may or may
// not exist on disk — callers should stat it if they need to.
func BodyPath(root, sha256 string) string {
	if sha256 == "" {
		return ""
	}
	return filepath.Join(root, blobsDirName, sha256+bodyExt)
}

// MetadataBlobPath returns the absolute path of a saved metadata blob
// keyed by its UUID. Symmetrical with BodyPath for body blobs.
func MetadataBlobPath(root, uuid string) string {
	if uuid == "" {
		return ""
	}
	return filepath.Join(root, blobsDirName, uuid+blobExt)
}

// LoadFileBody reads a saved file body by its content-addressed SHA-256
// hash. Returns (nil, nil) when missing — same convention as LoadBlob
// so callers can tell a missing body from a present-but-empty one only
// via stat.
func LoadFileBody(root, sha256 string) ([]byte, error) {
	if sha256 == "" {
		return nil, nil
	}
	data, err := os.ReadFile(BodyPath(root, sha256))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// SaveFileBody writes data into blobs/<sha256>.bin atomically (temp +
// rename) and returns the absolute path it wrote to. Callers should
// hash data with sha256.Sum256 first and reject accidental duplicates
// (or accept them — the temp file get overwritten on rename).
//
// We use this instead of SaveBlob because file bodies are 100%
// content-addressed: same bytes = same SHA = same path. Two topics
// pointing at the same file share a single on-disk copy, automatically
// deduplicated by the rename.
func SaveFileBody(root string, data []byte) (sha256Hex string, absPath string, err error) {
	sum := sha256.Sum256(data)
	sha256Hex = hex.EncodeToString(sum[:])
	dir := filepath.Join(root, blobsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("archive: mkdir blobs: %w", err)
	}
	final := BodyPath(root, sha256Hex)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", "", fmt.Errorf("archive: write body tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", "", fmt.Errorf("archive: rename body tmp: %w", err)
	}
	return sha256Hex, final, nil
}

// NewUUID returns a fresh random 128-bit identifier encoded as 32 hex chars.
// Cryptographically random, comparable to RFC 4122 v4 minus the variant
// bits — we don't need interoperability, just uniqueness for filenames.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ---- Prune -----------------------------------------------------------------

// Prune deletes every blob that is not the "current" version of some topic
// in any per-course index. The current UUIDs are collected by walking
// courses/<id>/index.json under root; every other file in blobs/ is removed.
//
// Prune never touches a per-course index file or any blob that is still
// referenced. If a blob is not referenced by any index but its name happens
// to collide with an actively-referenced UUID it is preserved (current set
// is authoritative).
func Prune(root string) (PruneResult, error) {
	res := PruneResult{Root: root}
	if _, err := os.Stat(filepath.Join(root, blobsDirName)); err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}

	live, err := collectLiveUUIDs(root)
	if err != nil {
		return res, err
	}

	entries, err := os.ReadDir(filepath.Join(root, blobsDirName))
	if err != nil {
		return res, fmt.Errorf("archive: read blobs dir: %w", err)
	}
	res.BlobsBefore = len(entries)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		uuid := strings.TrimSuffix(name, blobExt)
		if _, kept := live[uuid]; kept {
			continue
		}
		path := filepath.Join(root, blobsDirName, name)
		info, _ := e.Info()
		if err := os.Remove(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return res, fmt.Errorf("archive: prune %s: %w", name, err)
		}
		res.Deleted++
		if info != nil {
			res.BytesReclaim += info.Size()
		}
		res.OrphanUUIDs = append(res.OrphanUUIDs, uuid)
	}
	res.BlobsAfter = res.BlobsBefore - res.Deleted
	return res, nil
}

// collectLiveUUIDs walks every courses/<id>/index.json under root and
// returns the set of UUIDs currently pointed at by any TopicIndex.Current.
// Historical Version entries are intentionally NOT included — the whole
// point of Prune is to drop old versions, leaving only the current blob.
func collectLiveUUIDs(root string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	dir := filepath.Join(root, coursesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return live, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ci, err := LoadCourseIndex(root, e.Name())
		if err != nil {
			return nil, err
		}
		for _, t := range ci.Topics {
			if t.Current == "" {
				continue
			}
			live[t.Current] = struct{}{}
		}
	}
	return live, nil
}

// ---- Run -------------------------------------------------------------------

// Run executes a full archive pass: list courses (unless CourseIDs is set),
// fetch each TOC, fetch only changed topics. Safe to re-run; unchanged data
// is left alone on disk.
func Run(opts Options) (Result, error) {
	if opts.Root == "" {
		opts.Root = DefaultRoot()
	}
	if opts.Jar == nil {
		return Result{}, fmt.Errorf("archive: nil cookie jar")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	if err := os.MkdirAll(opts.Root, 0o700); err != nil {
		return Result{}, fmt.Errorf("archive: mkdir root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.Root, blobsDirName), 0o700); err != nil {
		return Result{}, fmt.Errorf("archive: mkdir blobs: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(opts.Root, coursesDirName), 0o700); err != nil {
		return Result{}, fmt.Errorf("archive: mkdir courses: %w", err)
	}

	idx, err := LoadIndex(opts.Root)
	if err != nil {
		return Result{}, err
	}

	var courses []Course
	if len(opts.CourseIDs) > 0 {
		courses = make([]Course, 0, len(opts.CourseIDs))
		for _, id := range opts.CourseIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			c := Course{OrgUnitId: id}
			if e, ok := idx.Courses[id]; ok {
				c.Name = e.Name
				c.Code = e.Code
				c.IsActive = e.IsActive
			}
			courses = append(courses, c)
		}
		emit(opts.Progress, ProgressEvent{Phase: "list", Total: len(courses), Done: 0})
	} else {
		if opts.Progress != nil {
			opts.Progress(ProgressEvent{Phase: "list"})
		}
		listed, err := listAllCoursesAt(opts.Jar, opts.baseURL())
		if err != nil {
			return Result{}, fmt.Errorf("archive: list courses: %w", err)
		}
		courses = listed
		emit(opts.Progress, ProgressEvent{Phase: "list", Total: len(courses), Done: 0})
	}

	result := Result{Root: opts.Root, CoursesScanned: len(courses)}

	for i, c := range courses {
		entry, ok := idx.Courses[c.OrgUnitId]
		if !ok {
			entry = &IndexEntry{}
			idx.Courses[c.OrgUnitId] = entry
		}
		entry.OrgUnitId = c.OrgUnitId
		entry.Name = c.Name
		entry.Code = c.Code
		entry.IsActive = c.IsActive

		emit(opts.Progress, ProgressEvent{
			Phase:      "toc",
			CourseID:   c.OrgUnitId,
			CourseName: c.Name,
			Done:       i,
			Total:      len(courses),
			Fetched:    result.TopicsFetched,
			Stale:      result.TopicsStale,
			Checked:    result.TopicsChecked,
		})

		stale, err := archiveCourse(opts, c, now)
		if err != nil {
			return result, fmt.Errorf("archive: course %s: %w", c.OrgUnitId, err)
		}
		result.TopicsChecked += stale.checked
		result.TopicsFetched += stale.fetched
		result.TopicsStale += stale.stale
		result.BodiesFetched += stale.bodiesNew
		result.BodiesReused += stale.bodiesReused
		result.BodiesFailed += stale.bodiesErr

		emit(opts.Progress, ProgressEvent{
			Phase:      "course-done",
			CourseID:   c.OrgUnitId,
			CourseName: c.Name,
			Done:       i + 1,
			Total:      len(courses),
			Fetched:    result.TopicsFetched,
			Stale:      result.TopicsStale,
			Checked:    result.TopicsChecked,
		})

		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "[archive] %s (%s): %d topics, %d fetched, %d stale, +%d bodies, %d reused, %d errors\n",
				c.OrgUnitId, c.Code, stale.checked, stale.fetched, stale.stale,
				stale.bodiesNew, stale.bodiesReused, stale.bodiesErr)
		}
	}

	idx.UpdatedAt = now()
	if err := SaveIndex(opts.Root, idx); err != nil {
		return result, err
	}
	return result, nil
}

// Diff walks the same course list as Run, fetches each TOC, and reports which
// topics would be fetched — but never fetches topic bodies and never writes
// to disk. Safe to run repeatedly.
func Diff(opts Options) (DiffResult, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	result := DiffResult{Root: opts.Root}

	idx, err := LoadIndex(opts.Root)
	if err != nil {
		return result, err
	}

	var courses []Course
	if len(opts.CourseIDs) > 0 {
		courses = make([]Course, 0, len(opts.CourseIDs))
		for _, id := range opts.CourseIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			c := Course{OrgUnitId: id}
			if e, ok := idx.Courses[id]; ok {
				c.Name = e.Name
				c.Code = e.Code
				c.IsActive = e.IsActive
			}
			courses = append(courses, c)
		}
		emit(opts.Progress, ProgressEvent{Phase: "list", Total: len(courses), Done: 0})
	} else {
		if opts.Progress != nil {
			opts.Progress(ProgressEvent{Phase: "list"})
		}
		listed, err := listAllCoursesAt(opts.Jar, opts.baseURL())
		if err != nil {
			return result, fmt.Errorf("diff: list courses: %w", err)
		}
		courses = listed
		emit(opts.Progress, ProgressEvent{Phase: "list", Total: len(courses), Done: 0})
	}

	for i, c := range courses {
		emit(opts.Progress, ProgressEvent{
			Phase:      "toc",
			CourseID:   c.OrgUnitId,
			CourseName: c.Name,
			Done:       i,
			Total:      len(courses),
		})

		cd, err := diffCourse(opts, c)
		if err != nil {
			return result, fmt.Errorf("diff: course %s: %w", c.OrgUnitId, err)
		}
		result.CoursesScanned++
		result.TopicsChecked += cd.TopicsTotal
		result.TopicsWouldFetch += cd.TopicsNew + cd.TopicsModified
		result.TopicsStale += cd.TopicsUnchanged
		result.PerCourse = append(result.PerCourse, cd)

		emit(opts.Progress, ProgressEvent{
			Phase:      "course-done",
			CourseID:   c.OrgUnitId,
			CourseName: c.Name,
			Done:       i + 1,
			Total:      len(courses),
			Checked:    result.TopicsChecked,
			Fetched:    result.TopicsWouldFetch,
			Stale:      result.TopicsStale,
		})

		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "[diff] %s (%s): %d topics, %d would fetch, %d unchanged\n",
				c.OrgUnitId, c.Code, cd.TopicsTotal, cd.TopicsNew+cd.TopicsModified, cd.TopicsUnchanged)
		}
	}
	return result, nil
}

// diffCourse is the per-course counterpart of archiveCourse: load the saved
// index, fetch the TOC, classify every topic, return the counts. No body
// fetches, no index writes.
func diffCourse(opts Options, c Course) (CourseDiff, error) {
	toc, err := fetchTocFor(opts.baseURL(), c.OrgUnitId, opts.Jar)
	if err != nil {
		return CourseDiff{}, err
	}
	ci, err := LoadCourseIndex(opts.Root, c.OrgUnitId)
	if err != nil {
		return CourseDiff{}, err
	}
	cd := CourseDiff{
		CourseID:   c.OrgUnitId,
		CourseName: c.Name,
		CourseCode: c.Code,
	}
	for _, t := range flattenTopics(toc.Modules) {
		cd.TopicsTotal++
		newMod := parseD2LTime(t.LastModifiedDate)
		ti, existed := ci.Topics[t.TopicId]
		switch {
		case !existed:
			cd.TopicsNew++
			cd.WouldFetchIDs = append(cd.WouldFetchIDs, t.TopicId)
		case !ti.LastModified.Equal(newMod):
			cd.TopicsModified++
			cd.WouldFetchIDs = append(cd.WouldFetchIDs, t.TopicId)
		default:
			cd.TopicsUnchanged++
		}
	}
	return cd, nil
}

// archiveCourse handles the per-course leg: fetch TOC, diff against the
// per-course index, queue and fetch stale topics, persist new blobs and the
// updated index. Old blobs are never touched.
// courseStats accumulates counts from one archiveCourseV2 pass so Run
// can roll them into the global Result. Body counts are only meaningful
// when DownloadFiles is true — otherwise all three stay zero and the
// summary line simply omits the bodies section.
type courseStats struct {
	stale, checked, fetched           int
	bodiesNew, bodiesReused, bodiesErr int
}

// archiveCourse runs a single course's archive pass and returns the
// counts that go into Result. Body downloads happen for File topics
// when opts.DownloadFiles is true.
func archiveCourse(opts Options, c Course, now func() time.Time) (courseStats, error) {
	return archiveCourseV2(opts, c, now)
}

// archiveCourseV2 is the working implementation.
func archiveCourseV2(opts Options, c Course, now func() time.Time) (courseStats, error) {
	var s courseStats
	toc, err := fetchTocFor(opts.baseURL(), c.OrgUnitId, opts.Jar)
	if err != nil {
		return s, err
	}

	ci, err := LoadCourseIndex(opts.Root, c.OrgUnitId)
	if err != nil {
		return s, err
	}
	ci.Name = c.Name
	ci.Code = c.Code
	ci.IsActive = c.IsActive

	type queued struct {
		topicID  int
		topic    content.TocTopic
		reason   string
		newMod   time.Time
		isFile   bool // File topic — eligible for body download when --files is on
	}
	var work []queued
	for _, t := range flattenTopics(toc.Modules) {
		s.checked++
		newMod := parseD2LTime(t.LastModifiedDate)
		ti, existed := ci.Topics[t.TopicId]
		// File topics are eligible for body download: anything that the
		// D2L TOC calls "File" (and has a real URL to the underlying
		// document) gets its bytes downloaded + hashed in the body-
		// download stage below. We piggyback on topicType() — the
		// existing discriminator that maps the TOC's TypeIdentifier /
		// TopicType fields to the canonical "File" / "Link" /
		// <empty-other> strings.
		isFile := topicType(t) == "File" && t.Url != nil && *t.Url != ""
		// Body-version detection requires re-downloading the file (D2L
		// doesn't expose a body hash), so we only run the body stage
		// on topics whose metadata changed. The trade-off: an editor
		// that re-uploads a file without touching LastModifiedDate
		// will not be detected. Users who want exhaustive coverage
		// can `prune` and re-archive.
		switch {
		case !existed:
			work = append(work, queued{topicID: t.TopicId, topic: t, reason: "new", newMod: newMod, isFile: isFile})
		case !ti.LastModified.Equal(newMod):
			work = append(work, queued{topicID: t.TopicId, topic: t, reason: "modified", newMod: newMod, isFile: isFile})
		default:
			s.stale++
		}
	}

for _, q := range work {
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "[archive]   topic %d: %s\n", q.topicID, q.reason)
		}
		if q.reason == "new" || q.reason == "modified" {
			emit(opts.Progress, ProgressEvent{
				Phase:    "topic",
				CourseID: c.OrgUnitId,
				TopicID:  q.topicID,
				Reason:   q.reason,
				Stale:    s.stale,
			})
			body, ferr := fetchTopicFor(opts.baseURL(), opts.Jar, c.OrgUnitId, q.topicID)
			if ferr != nil {
				return s, fmt.Errorf("fetch topic %d: %w", q.topicID, ferr)
			}
			uuid, size, werr := SaveBlob(opts.Root, body)
			if werr != nil {
				return s, werr
			}
			ti := ci.Topics[q.topicID]
			if ti == nil {
				ti = &TopicIndex{TopicID: q.topicID}
				ci.Topics[q.topicID] = ti
			}
			ti.Title = q.topic.Title
			ti.Type = topicType(q.topic)
			if u := topicURL(q.topic); u != "" {
				ti.URL = u
			}
			ti.LastModified = q.newMod
			ti.Current = uuid
			ti.Versions = append(ti.Versions, TopicVersion{
				UUID:         uuid,
				LastModified: q.newMod,
				Size:         size,
				SavedAt:      now(),
			})
			s.fetched++
		}

		// File bodies: download when this topic was queued as new/modified.
		// We do NOT re-download bodies when only the existing index has a
		// matching SHA — that would force a 246-topic walk every run.
		if !opts.DownloadFiles || !q.isFile || (q.reason != "new" && q.reason != "modified") {
			continue
		}
		ti, tiOk := ci.Topics[q.topicID]
		if !tiOk || ti == nil {
			continue
		}
		if ti.URL == "" {
			continue
		}
		bodyBytes, contentType, berr := downloadFileBody(opts.Jar, ti.URL)
		if berr != nil {
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "[archive]   body %d: %v (continuing)\n", q.topicID, berr)
			}
			s.bodiesErr++
			continue
		}
		sha := sha256.Sum256(bodyBytes)
		shaHex := hex.EncodeToString(sha[:])
		if shaHex == ti.CurrentBody {
			s.bodiesReused++
			continue
		}
		var _, _, sErr = SaveFileBody(opts.Root, bodyBytes)
		if sErr != nil {
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "[archive]   body %d save: %v (continuing)\n", q.topicID, sErr)
			}
			s.bodiesErr++
			continue
		}
		nowt := now()
		ti.CurrentBody = shaHex
		ti.BodyVersions = append(ti.BodyVersions, BodyVersion{
			SHA256:       shaHex,
			Size:         int64(len(bodyBytes)),
			ContentType:  contentType,
			DownloadedAt: nowt,
			LastModified: q.newMod,
		})
		s.bodiesNew++
	}

	ci.UpdatedAt = now()
	if err := SaveCourseIndex(opts.Root, c.OrgUnitId, ci); err != nil {
		return s, err
	}

	return s, nil
}

// downloadFileBody fetches the underlying file bytes from a Topic URL
// (e.g. /content/enforced/<org>-<slug>/<file>). Relative URLs are
// resolved against ua.D2LBase (the public D2L base URL is fine — the
// session cookie auth applies to the same host).
//
// Returns the bytes and the D2L-provided Content-Type header (empty
// when missing). The returned body is *not* hashed — call sites
// should use the SHA they computed to decide whether to save it as a
// new blob.
func downloadFileBody(jar *cookiejar.Jar, rawURL string) ([]byte, string, error) {
	if rawURL == "" {
		return nil, "", fmt.Errorf("empty URL")
	}
	// Resolve relative URLs against the D2L base. The metadata
	// endpoint returns bare paths like "/content/enforced/..." which
	// D2L never gives us as absolute URLs to copy.
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		base, err := url.Parse(ua.D2LBase)
		if err != nil {
			return nil, "", fmt.Errorf("resolve base: %w", err)
		}
		rel, err := url.Parse(rawURL)
		if err != nil {
			return nil, "", fmt.Errorf("parse %s: %w", rawURL, err)
		}
		rawURL = base.ResolveReference(rel).String()
	}
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "*/*"},
	})
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(res.Body)
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return nil, "", fmt.Errorf("download %d %s for %s: %s", res.StatusCode, res.Status, rawURL, msg)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	return body, res.Header.Get("Content-Type"), nil
}

// parseD2LTime parses D2L's ISO 8601 LastModifiedDate. Returns a zero
// time.Time on empty input; comparisons against zero are stable.
func parseD2LTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func topicType(t content.TocTopic) string {
	if t.TypeIdentifier != "" {
		return t.TypeIdentifier
	}
	if t.TopicType == 2 {
		return "Link"
	}
	return "File"
}

func topicURL(t content.TocTopic) string {
	if t.Url == nil {
		return ""
	}
	return *t.Url
}

// fetchTocFor fetches the TOC using the given base URL.
func fetchTocFor(base, orgUnitID string, jar *cookiejar.Jar) (content.TocResponse, error) {
	res, err := httpclient.FollowRedirects(tocURLFor(base, orgUnitID), jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return content.TocResponse{}, err
	}
	if res.StatusCode != 200 {
		return content.TocResponse{}, fmt.Errorf("D2L API returned %d %s for %s", res.StatusCode, res.Status, res.Request.URL)
	}
	var toc content.TocResponse
	if err := json.NewDecoder(res.Body).Decode(&toc); err != nil {
		return content.TocResponse{}, err
	}
	return toc, nil
}

// fetchTopicFor retrieves the canonical document payload for a single topic
// against the given base URL.
func fetchTopicFor(base string, jar *cookiejar.Jar, orgUnitID string, topicID int) ([]byte, error) {
	res, err := httpclient.FollowRedirects(topicURLFor(base, orgUnitID, topicID), jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("D2L API returned %d %s for topic %d", res.StatusCode, res.Status, topicID)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return nil, err
	}
	out, err := prettyJSON(body)
	if err != nil {
		// Fall back to raw bytes; the on-disk file is still useful.
		return body, nil
	}
	return out, nil
}

// flattenTopics walks the TOC depth-first and returns every topic.
func flattenTopics(modules []content.TocModule) []content.TocTopic {
	var out []content.TocTopic
	for _, m := range modules {
		out = append(out, m.Topics...)
		if len(m.Modules) > 0 {
			out = append(out, flattenTopics(m.Modules)...)
		}
	}
	return out
}

// safeID scrubs an OrgUnitId for use as a directory name. D2L ids are numeric
// today, but we guard against path separators just in case.
func safeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "_"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_")
	return r.Replace(id)
}

// SortedCourseIDs returns the course IDs from an Index in stable order, for
// deterministic output in CLI listings.
func SortedCourseIDs(idx *Index) []string {
	out := make([]string, 0, len(idx.Courses))
	for id := range idx.Courses {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := strconv.Atoi(out[i])
		aj, _ := strconv.Atoi(out[j])
		if ai != 0 && aj != 0 {
			return ai < aj
		}
		return out[i] < out[j]
	})
	return out
}

// writeJSONAtomic marshals v (pretty) and writes it to path via a temp file.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("archive: marshal: %w", err)
	}
	return writeTempRename(path, append(data, '\n'), 0o600)
}

// writeTempRename writes data to path+".tmp" then renames over path. Parent
// directory must already exist.
func writeTempRename(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("archive: write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("archive: rename %s: %w", path, err)
	}
	return nil
}

// prettyJSON returns a pretty-printed version of data; returns an error if
// data is not valid JSON.
func prettyJSON(data []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, "", "  ")
}

// emit is a nil-safe wrapper around an optional ProgressFunc.
func emit(p ProgressFunc, ev ProgressEvent) {
	if p == nil {
		return
	}
	p(ev)
}

// ---- courses API client (duplicated from cmd/courses.go) -----------------

// d2lCourse mirrors the fields we care about from the manageCourses API.
type d2lCourse struct {
	OrgUnitId json.Number `json:"OrgUnitId"`
	Name      string      `json:"Name"`
	Code      string      `json:"Code"`
	IsActive  bool        `json:"IsActive"`
}

type d2lCoursesResponse struct {
	Courses []d2lCourse `json:"Courses"`
}

func listAllCoursesAt(jar *cookiejar.Jar, base string) ([]Course, error) {
	var all []Course
	next := coursesAPIURLFor(base)
	pageNum := 0
	for next != "" {
		pageNum++
		data, err := fetchCoursesPage(jar, next)
		if err != nil {
			return all, err
		}
		for _, c := range data.Courses {
			all = append(all, Course{
				OrgUnitId: c.OrgUnitId.String(),
				Name:      c.Name,
				Code:      c.Code,
				IsActive:  c.IsActive,
			})
		}
		if len(data.Courses) < 20 {
			break
		}
		next = advancePage(next, pageNum+1)
	}
	return all, nil
}

func fetchCoursesPage(jar *cookiejar.Jar, rawURL string) (d2lCoursesResponse, error) {
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{})
	if err != nil {
		return d2lCoursesResponse{}, err
	}
	if res.StatusCode != 200 {
		return d2lCoursesResponse{}, fmt.Errorf("D2L my-courses API returned %d %s", res.StatusCode, res.Status)
	}
	ct := res.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/json") {
		return d2lCoursesResponse{}, fmt.Errorf("expected JSON, got %s", ct)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return d2lCoursesResponse{}, err
	}
	var out d2lCoursesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return d2lCoursesResponse{}, err
	}
	return out, nil
}

func advancePage(rawURL string, page int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}
