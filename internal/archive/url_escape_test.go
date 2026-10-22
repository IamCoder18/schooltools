package archive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamCoder18/schooltools/internal/content"
)

func TestRunDownloadsFileBodyWithUnescapedURL(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("9999"), Name: "Chem", Code: "CHM", IsActive: true}}
	rawURL := "/content/enforced/9999-CHM/Acetic Acid, 5% (Vinegar).pdf"
	f.tocs["9999"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 9100, Title: "Acetic Acid", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &rawURL},
	}}}}
	f.topics[9100] = map[string]any{"TopicId": 9100, "Title": "Acetic Acid", "TypeIdentifier": "File", "Url": rawURL}

	var gotPath string
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("PDF-vinegar"))
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.BodiesFetched != 1 {
		t.Fatalf("BodiesFetched=%d, want 1", res.BodiesFetched)
	}
	wantPath := "/content/enforced/9999-CHM/Acetic Acid, 5% (Vinegar).pdf"
	if gotPath != wantPath {
		t.Errorf("server saw path %q, want %q (the filename must survive URL escaping)", gotPath, wantPath)
	}
}

func TestRunDownloadsFileBodyWithHashInURL(t *testing.T) {
	f := newFakeD2L(t)
	f.courses = []d2lCourse{{OrgUnitId: json.Number("9998"), Name: "Chem", Code: "CHM", IsActive: true}}
	rawURL := "/content/enforced/9998-CHM/Quiz #3 Chemistry.pdf"
	f.tocs["9998"] = content.TocResponse{Modules: []content.TocModule{{ModuleId: 1, Topics: []content.TocTopic{
		{TopicId: 9200, Title: "Quiz 3", LastModifiedDate: "2026-03-01T00:00:00Z", TypeIdentifier: "File", Url: &rawURL},
	}}}}
	f.topics[9200] = map[string]any{"TopicId": 9200, "Title": "Quiz 3", "TypeIdentifier": "File", "Url": rawURL}

	var gotPath string
	f.bodyHandler = func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("PDF-quiz"))
	}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	res, err := Run(Options{Root: t.TempDir(), Jar: newJar(t), BaseURL: srv.URL, DownloadFiles: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.BodiesFetched != 1 {
		t.Fatalf("BodiesFetched=%d, want 1", res.BodiesFetched)
	}
	wantPath := "/content/enforced/9998-CHM/Quiz #3 Chemistry.pdf"
	if gotPath != wantPath {
		t.Errorf("server saw path %q, want %q (a raw # must not truncate the path into a fragment)", gotPath, wantPath)
	}
}
