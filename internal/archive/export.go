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
		hits = exportResolveRefs(store, opts.Refs)
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
	for _, h := range hits {
		if h.CurrentBody == "" {
			res.Skipped++
			res.Files = append(res.Files, ExportFile{
				TopicID: h.TopicID, CourseID: h.CourseID, Title: h.Title,
				Status: "skipped-missing-body",
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
		dest := uniqueDest(dir, base, ext, used)

		source := BodyPath(opts.Root, h.CurrentBody)
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

func exportResolveRefs(store *Store, refs []string) []SearchHit {
	out := make([]SearchHit, 0, len(refs))
	for _, r := range refs {
		res, err := store.Resolve(r, "")
		if err != nil {
			continue
		}
		if res.Kind != "body" || res.Ref.Topic == nil {
			continue
		}
		out = append(out, SearchHit{
			CourseID:     res.Ref.CourseID,
			CourseName:   res.Ref.CourseName,
			CourseCode:   res.Ref.CourseCode,
			TopicID:      res.Ref.Topic.TopicID,
			Title:        res.Ref.Topic.Title,
			Type:         res.Ref.Topic.Type,
			URL:          res.Ref.Topic.URL,
			LastModified: res.Ref.Topic.LastModified,
			CurrentBody:  res.BlobID,
			HasBody:      true,
		})
	}
	return out
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

func uniqueDest(dir, base, ext string, used map[string]int) string {
	key := filepath.Join(dir, base+"."+ext)
	n := used[key]
	used[key] = n + 1
	if n == 0 {
		return key
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d).%s", base, n+1, ext))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func fileSize(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return info.Size()
}
