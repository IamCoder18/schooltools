package cmd

import (
	"testing"
)

func TestResolveFileURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "relative enforced path with space and percent",
			in:   "/content/enforced/1522695-x/Acetic Acid, 5% (Vinegar).pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/1522695-x/Acetic%20Acid,%205%25%20(Vinegar).pdf",
		},
		{
			name: "hash in filename becomes an escape, not a fragment",
			in:   "/content/enforced/1527886-x/Quiz #3 Chemistry.pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/1527886-x/Quiz%20%233%20Chemistry.pdf",
		},
		{
			name: "existing escape preserved",
			in:   "/content/enforced/1522695-x/KEY%20Practice.pdf",
			want: "https://d2l.cbe.ab.ca/content/enforced/1522695-x/KEY%20Practice.pdf",
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
