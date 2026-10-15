package urlutil

import (
	"net/url"
	"testing"
)

func TestCleanPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already valid", "/content/enforced/x/notes.pdf", "/content/enforced/x/notes.pdf"},
		{"literal spaces", "/c/Acetic Acid, 5% (Vinegar).pdf", "/c/Acetic%20Acid,%205%25%20(Vinegar).pdf"},
		{"hash in filename", "/c/Quiz #3 Chemistry.pdf", "/c/Quiz%20%233%20Chemistry.pdf"},
		{"existing escapes preserved", "/c/KEY%20Practice.pdf", "/c/KEY%20Practice.pdf"},
		{"trailing bare percent", "/c/100%", "/c/100%25"},
		{"bare percent not followed by hex", "/c/50% off.pdf", "/c/50%25%20off.pdf"},
		{"question mark is a path character", "/c/What? Really.pdf?download=1", "/c/What%3F%20Really.pdf%3Fdownload=1"},
		{"non-ascii bytes", "/c/café menu.pdf", "/c/caf%C3%A9%20menu.pdf"},
	}
	for _, tc := range cases {
		if got := CleanPath(tc.in); got != tc.want {
			t.Errorf("%s: CleanPath(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestResolveD2L(t *testing.T) {
	base := "https://d2l.cbe.ab.ca"

	got, err := ResolveD2L(base, "/content/enforced/1522695-x/Acetic Acid, 5% (Vinegar).pdf")
	if err != nil {
		t.Fatalf("ResolveD2L: %v", err)
	}
	want := "https://d2l.cbe.ab.ca/content/enforced/1522695-x/Acetic%20Acid,%205%25%20(Vinegar).pdf"
	if got != want {
		t.Errorf("ResolveD2L = %q, want %q", got, want)
	}

	u, perr := url.Parse(got)
	if perr != nil {
		t.Fatalf("resolved url does not parse: %v", perr)
	}
	if u.Path != "/content/enforced/1522695-x/Acetic Acid, 5% (Vinegar).pdf" {
		t.Errorf("decoded path = %q, want the original filename", u.Path)
	}
	if u.Fragment != "" {
		t.Errorf("fragment = %q, want empty", u.Fragment)
	}
}

func TestResolveD2LHashFilename(t *testing.T) {
	got, err := ResolveD2L("https://d2l.cbe.ab.ca", "/content/enforced/1527886-x/Quiz #3 Chemistry.pdf")
	if err != nil {
		t.Fatalf("ResolveD2L: %v", err)
	}
	want := "https://d2l.cbe.ab.ca/content/enforced/1527886-x/Quiz%20%233%20Chemistry.pdf"
	if got != want {
		t.Errorf("ResolveD2L = %q, want %q", got, want)
	}
}

func TestResolveD2LRelativeRefWithColon(t *testing.T) {
	// url.Parse rejects a reference whose first segment contains a colon
	// ("Week 1: Intro/…" looks like a scheme). The './' prefix keeps the
	// colon inside a path segment.
	got, err := ResolveD2L("https://d2l.cbe.ab.ca/d2l/le/content/123/view", "Week 1: Intro/notes.pdf")
	if err != nil {
		t.Fatalf("ResolveD2L: %v", err)
	}
	want := "https://d2l.cbe.ab.ca/d2l/le/content/123/Week%201:%20Intro/notes.pdf"
	if got != want {
		t.Errorf("ResolveD2L = %q, want %q", got, want)
	}
}

func TestResolveD2LRelativeRefWithSpaceBeforeColon(t *testing.T) {
	got, err := ResolveD2L("https://d2l.cbe.ab.ca", "Unit 3: Ratios.pdf")
	if err != nil {
		t.Fatalf("ResolveD2L: %v", err)
	}
	want := "https://d2l.cbe.ab.ca/Unit%203:%20Ratios.pdf"
	if got != want {
		t.Errorf("ResolveD2L = %q, want %q", got, want)
	}
}

func TestResolveD2LAbsoluteURLIsCleaned(t *testing.T) {
	got, err := ResolveD2L("https://d2l.cbe.ab.ca", "https://files.example.com/a b#c.pdf")
	if err != nil {
		t.Fatalf("ResolveD2L: %v", err)
	}
	want := "https://files.example.com/a%20b%23c.pdf"
	if got != want {
		t.Errorf("ResolveD2L = %q, want %q", got, want)
	}
}

func TestResolveD2LBaseParseError(t *testing.T) {
	if _, err := ResolveD2L("ht tp://bad", "/content/x.pdf"); err == nil {
		t.Error("expected error for unparseable base")
	}
}
