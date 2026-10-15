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
// url.Parse cannot fail on them and no character is silently
// reinterpreted as a URL fragment or query delimiter. A '?' is treated
// as a path character: D2L file URLs never carry a query string.
func ResolveD2L(base, raw string) (string, error) {
	if isAbs(raw) {
		return cleanAbs(raw), nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base: %w", err)
	}
	ref, err := url.Parse(CleanPath(raw))
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

func isAbs(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
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
