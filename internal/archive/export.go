package archive

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ExportOptions struct {
	Root      string
	Out       string
	CourseIDs []string
	Ext       string
	Flat      bool
	DryRun    bool
	Refs      []string
}

type ExportFile struct {
	TopicID  int    `json:"topicId"`
	CourseID string `json:"courseId"`
	Title    string `json:"title,omitempty"`
	SHA256   string `json:"sha256"`
	Source   string `json:"source"`
	Dest     string `json:"dest"`
	Size     int64  `json:"size"`
	Status   string `json:"status"`
}

type ExportResult struct {
	Root       string       `json:"root"`
	Out        string       `json:"out"`
	Copied     int          `json:"copied"`
	Skipped    int          `json:"skipped"`
	BytesTotal int64        `json:"bytesTotal"`
	DryRun     bool         `json:"dryRun"`
	Files      []ExportFile `json:"files"`
}

func Export(opts ExportOptions) (ExportResult, error) {
	if opts.Root == "" {
		opts.Root = DefaultRoot()
	}
	if opts.Out == "" {
		return ExportResult{}, fmt.Errorf("export: --out is required")
	}
	res := ExportResult{Root: opts.Root, Out: opts.Out, DryRun: opts.DryRun}

	store, err := OpenStore(opts.Root)
	if err != nil {
		return res, err
	}

	var hits []SearchHit
	if len(opts.Refs) > 0 {
		hs, rerr := exportResolveRefs(store, opts.Refs)
		if rerr != nil {
			return res, rerr
		}
		hits = hs
	} else {
		hits = store.Search(SearchQuery{
			Ext:        opts.Ext,
			CourseIDs:  opts.CourseIDs,
			WithBodies: true,
		})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].CourseID != hits[j].CourseID {
			return hits[i].CourseID < hits[j].CourseID
		}
		return hits[i].TopicID < hits[j].TopicID
	})

	used := map[string]int{}
	reserved := map[string]struct{}{}
	for _, h := range hits {
		source := BodyPath(opts.Root, h.CurrentBody)
		if h.CurrentBody == "" || !fileExists(source) {
			res.Skipped++
			res.Files = append(res.Files, ExportFile{
				TopicID: h.TopicID, CourseID: h.CourseID, Title: h.Title,
				Source: source, Status: "skipped-missing-body",
			})
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(h.URL), "."))
		if ext == "" {
			ext = "bin"
		}
		base := sanitizeFilename(h.Title)
		if base == "" {
			base = fmt.Sprintf("topic-%d", h.TopicID)
		}
		var dir string
		if opts.Flat {
			dir = opts.Out
		} else {
			code := h.CourseCode
			if code == "" {
				code = h.CourseID
			}
			dir = filepath.Join(opts.Out, sanitizeFilename(code))
		}
		dest, err := uniqueDest(dir, base, ext, used, reserved)
		if err != nil {
			return res, err
		}

		size := fileSize(source)
		entry := ExportFile{
			TopicID: h.TopicID, CourseID: h.CourseID, Title: h.Title,
			SHA256: h.CurrentBody, Source: source, Dest: dest, Size: size,
		}
		if opts.DryRun {
			entry.Status = "would-copy"
			res.Files = append(res.Files, entry)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, err
		}
		if err := copyFile(source, dest); err != nil {
			return res, fmt.Errorf("copy %s -> %s: %w", source, dest, err)
		}
		entry.Status = "copied"
		res.Copied++
		res.BytesTotal += size
		res.Files = append(res.Files, entry)
	}
	return res, nil
}

func exportResolveRefs(store *Store, refs []string) ([]SearchHit, error) {
	out := make([]SearchHit, 0, len(refs))
	for _, r := range refs {
		res, err := store.Resolve(r, "")
		if err != nil {
			return nil, fmt.Errorf("export ref %q: %w", r, err)
		}
		t := res.Ref.Topic
		if t == nil {
			return nil, fmt.Errorf("export ref %q: no topic resolves to %s", r, res.BlobID)
		}
		if res.BlobID == "" {
			return nil, fmt.Errorf("export ref %q: resolved to a topic with no archived blob", r)
		}
		hasBody := res.Kind == "body" || t.CurrentBody != ""
		currentBody := t.CurrentBody
		if res.Kind == "body" {
			currentBody = res.BlobID
		}
		out = append(out, SearchHit{
			CourseID:     res.Ref.CourseID,
			CourseName:   res.Ref.CourseName,
			CourseCode:   res.Ref.CourseCode,
			TopicID:      t.TopicID,
			Title:        t.Title,
			Type:         t.Type,
			URL:          t.URL,
			LastModified: t.LastModified,
			CurrentBody:  currentBody,
			HasBody:      hasBody,
		})
	}
	return out, nil
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			runes = append(runes, '_')
		case r < 0x20:
			runes = append(runes, '_')
		default:
			runes = append(runes, r)
		}
	}
	out := strings.TrimSpace(string(runes))
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	if len([]rune(out)) > 120 {
		out = string([]rune(out)[:120])
	}
	out = strings.TrimLeft(out, ".")
	out = strings.TrimRight(out, ".")
	if out == "" || strings.Trim(out, ".") == "" {
		return ""
	}
	return out
}

// uniqueDest picks an unused destination path for an exported file.
// `used` records the next suffix index to try for each "<base>.<ext>"
// key (one entry per unsuffixed base). `reserved` records every
// destination path that has already been returned by a previous call
// in the same export run, including suffixed candidates. Both a
// `reserved` hit and a real filesystem hit bump the suffix; an
// unexpected Stat error (anything other than NotExist) is returned so
// the caller can fail loudly instead of silently trying the next
// suffix forever.
func uniqueDest(dir, base, ext string, used map[string]int, reserved map[string]struct{}) (string, error) {
	key := filepath.Join(dir, base+"."+ext)
	nextSuf := used[key] + 1
	for n := nextSuf; ; n++ {
		var candidate string
		if n == 1 {
			candidate = key
		} else {
			candidate = filepath.Join(dir, fmt.Sprintf("%s (%d).%s", base, n, ext))
		}
		if _, taken := reserved[candidate]; taken {
			continue
		}
		_, statErr := os.Stat(candidate)
		switch {
		case statErr == nil:
			// Exists on disk — try the next suffix.
		case os.IsNotExist(statErr):
			used[key] = n
			reserved[candidate] = struct{}{}
			return candidate, nil
		default:
			return "", fmt.Errorf("export: stat %s: %w", candidate, statErr)
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

func fileSize(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return info.Size()
}
