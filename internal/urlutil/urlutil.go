// Package urlutil repairs the D2L content URLs that the LMS emits with
// unescaped path characters (literal spaces, '#', stray '%', '?') before
// they are handed to net/http.
package urlutil

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveD2L resolves a D2L content URL against a base URL. Relative
// references and absolute URLs are both percent-encoded first, so that
// no character is silently reinterpreted as a URL fragment or query
// delimiter. A '?' is treated as a path character: D2L file URLs never
// carry a query string. Relative references that are not already
// path-absolute (no leading '/', './' or '../') are resolved against the
// base's directory with a './' prefix, which keeps url.Parse from
// mistaking a colon in the first segment ("Week 1: Intro/…") for a URL
// scheme.
func ResolveD2L(base, raw string) (string, error) {
	if isAbs(raw) {
		return cleanAbs(raw), nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base: %w", err)
	}
	p := CleanPath(raw)
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") {
		p = "./" + p
	}
	ref, err := url.Parse(p)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", raw, err)
	}
	return b.ResolveReference(ref).String(), nil
}

// CleanPath percent-encodes a URL path, preserving escape sequences that
// are already valid.
func CleanPath(raw string) string {
	return encodePath(raw)
}

// isAbs reports whether raw already carries an HTTP(S) scheme. Schemes
// are case-insensitive, so "HTTPS://host/x" is absolute too and must
// never be treated as a relative reference.
func isAbs(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func cleanAbs(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return CleanPath(raw)
	}
	rest := raw[i+3:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return raw
	}
	return raw[:i+3] + rest[:slash] + CleanPath(rest[slash:])
}

func encodePath(p string) string {
	var b strings.Builder
	b.Grow(len(p) + 8)
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '%' && i+2 < len(p) && isHex(p[i+1]) && isHex(p[i+2]):
			b.WriteString(p[i : i+3])
			i += 2
		case c == '%':
			b.WriteString("%25")
		case mustEscape(c):
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func mustEscape(c byte) bool {
	switch {
	case c <= 0x20:
		return true
	case c >= 0x7f:
		return true
	}
	switch c {
	case '"', '#', '<', '>', '?', '`', '{', '|', '}', '\\', '^':
		return true
	}
	return false
}

func isHex(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	}
	return false
}
