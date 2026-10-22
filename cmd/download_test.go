package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/content"
)

func captureDownloadOutput(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	fnErr := fn()
	_ = w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	_ = r.Close()
	return string(b), fnErr
}

func TestDownloadResultKV(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	topic := content.Topic{TopicId: 5, Title: "Notes", TypeIdentifier: "File", FileName: "notes.pdf"}

	got := downloadResultKV("3000001", topic, "/home/x/notes.pdf", "https://d2l.cbe.ab.ca/content/enforced/3000001-x/notes.pdf", 1024)

	require.Contains(t, got, "/home/x/notes.pdf")
	require.Contains(t, got, "1.0 KiB")
	require.Contains(t, got, "3000001")
	require.Contains(t, got, "5 · Notes")
	require.Contains(t, got, "/content/enforced/3000001-x/notes.pdf")
}

func TestMetadataResultKVShowsLinkURL(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	u := "https://example.org/lesson"
	topic := content.Topic{TopicId: 9, Title: "Video", TypeIdentifier: "Link", Url: &u}

	got := metadataResultKV("3000001", topic, "/home/x/topic-3000001-9.json")

	require.Contains(t, got, "/home/x/topic-3000001-9.json")
	require.Contains(t, got, "Link")
	require.Contains(t, got, "https://example.org/lesson")
}

func TestWriteTopicMetadataSavesLinkReport(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	dir := t.TempDir()
	oldOut, oldName, oldNo := downloadOut, downloadName, downloadNoOverwrite
	downloadOut, downloadName, downloadNoOverwrite = dir, "", false
	defer func() { downloadOut, downloadName, downloadNoOverwrite = oldOut, oldName, oldNo }()

	u := "https://example.org/lesson"
	out, err := captureDownloadOutput(func() error {
		return writeTopicMetadata("3000001", content.Topic{TopicId: 9, Title: "Video", TypeIdentifier: "Link", Url: &u})
	})
	require.NoError(t, err)
	require.Contains(t, out, "https://example.org/lesson")
	require.Contains(t, out, "schooltools content get 9 --course 3000001")
	saved := filepath.Join(dir, "topic-3000001-9.json")
	require.Contains(t, out, saved)
	_, serr := os.Stat(saved)
	require.NoError(t, serr)
}

func TestWriteTopicMetadataNoClobber(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	dir := t.TempDir()
	path := filepath.Join(dir, "topic-3000001-9.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o644))

	oldOut, oldName, oldNo := downloadOut, downloadName, downloadNoOverwrite
	downloadOut, downloadName, downloadNoOverwrite = dir, "topic-3000001-9.json", true
	defer func() { downloadOut, downloadName, downloadNoOverwrite = oldOut, oldName, oldNo }()

	out, err := captureDownloadOutput(func() error {
		return writeTopicMetadata("3000001", content.Topic{TopicId: 9, Title: "Video", TypeIdentifier: "Link"})
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to overwrite")
	require.Contains(t, out, "already exists")
	body, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	require.Equal(t, "{}", string(body))
}

func TestSanitizeFilename(t *testing.T) {
	require.Equal(t, "a_b.pdf", sanitizeFilename("a/b.pdf"))
	require.Equal(t, "notes.pdf", sanitizeFilename(" notes.pdf "))
	require.Equal(t, "", sanitizeFilename("   "))
}

func TestFilenameFromContentDisposition(t *testing.T) {
	require.Equal(t, "report.pdf", filenameFromContentDisposition(`attachment; filename="report.pdf"`))
	require.Equal(t, "", filenameFromContentDisposition(""))
}

func TestResolveFileURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "relative enforced path with space and percent",
			in:   "/content/enforced/3000001-x/Acetic Acid, 5% (Vinegar).pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/3000001-x/Acetic%20Acid,%205%25%20(Vinegar).pdf",
		},
		{
			name: "hash in filename becomes an escape, not a fragment",
			in:   "/content/enforced/3000001-x/Quiz #3 Chemistry.pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/3000001-x/Quiz%20%233%20Chemistry.pdf",
		},
		{
			name: "existing escape preserved",
			in:   "/content/enforced/3000001-x/KEY%20Practice.pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/3000001-x/KEY%20Practice.pdf",
		},
		{
			name: "relative reference with a colon in the first segment",
			in:   "Week 1: Intro/notes.pdf",
			want: "https://d2l.cbe.ab.ca/Week%201:%20Intro/notes.pdf",
		},
		{
			name: "absolute URL is cleaned too",
			in:   "https://files.example.com/a b#c.pdf",
			want: "https://files.example.com/a%20b%23c.pdf",
		},
		{
			name:    "empty URL",
			in:      "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		got, err := resolveFileURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: resolveFileURL(%q) = %q, want an error", tc.name, tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: resolveFileURL(%q): %v", tc.name, tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: resolveFileURL(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
