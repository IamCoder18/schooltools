package browser

import (
	"net/url"
	"strings"

	"github.com/aarav/schooltools/internal/ua"
)

// RequestContext carries everything browser-faithful header generation needs
// to know about a single request. Referer should be the URL the user "came
// from" (the previous request URL); pass "" for the very first navigation.
type RequestContext struct {
	URL     string
	Method  string // "GET" or "POST"; default treated as GET
	Referer string
}

// Headers produces a header set that matches what Chrome ~130 sends for the
// given request type. Per-request shape matters: ADFS and D2L use Sec-Fetch-*,
// Accept, Sec-Fetch-User and Upgrade-Insecure-Requests to decide whether the
// caller is a real browser, and that decision controls whether persistent
// (KMSI) cookies are issued. Sending a flat "default" set on every call — as
// we used to — gets us session-only cookies.
func Headers(ctx RequestContext, extra map[string]string) map[string]string {
	method := strings.ToUpper(strings.TrimSpace(ctx.Method))
	if method == "" {
		method = "GET"
	}
	isFormPost := method == "POST"

	u, err := url.Parse(ctx.URL)
	if err != nil {
		return merge(defaultHeaders(isFormPost), extra)
	}

	h := defaultHeaders(isFormPost)

	// Referer: caller-supplied, with a sane fallback for same-origin GETs that
	// forgot to set one. Browsers omit Referer for top-level cross-site
	// navigations (and for Referrer-Policy: no-referrer-when-downgrade); we
	// follow that — if the caller passed "" we leave it out.
	if ctx.Referer != "" {
		h["Referer"] = ctx.Referer
	} else if u.Host == urlMustHost(ua.D2LBase) && method == "GET" {
		h["Referer"] = ua.D2LBase + "/d2l/home"
	}

	// Origin: only meaningful on POST (and on cross-origin requests).
	if isFormPost {
		origin := u.Scheme + "://" + u.Host
		h["Origin"] = origin
	}

	// Sec-Fetch-* triplet — the server's main "are you a browser?" signal.
	h["Sec-Fetch-Dest"] = "document"
	h["Sec-Fetch-Mode"] = "navigate"
	h["Sec-Fetch-Site"] = fetchSite(ctx.Referer, u)
	// Browsers send Sec-Fetch-User: ?1 only on user-initiated navigations.
	// We always set it because every request in the login flow is
	// user-initiated from our point of view.
	h["Sec-Fetch-User"] = "?1"

	// Top-level navigations advertise upgrade support.
	h["Upgrade-Insecure-Requests"] = "1"

	return merge(h, extra)
}

// fetchSite mirrors Chrome's classification of the initiator relative to the
// request URL. "" means "no Referer / direct navigation", which Chrome sends
// as Sec-Fetch-Site: none.
func fetchSite(referer string, u *url.URL) string {
	if referer == "" {
		return "none"
	}
	ru, err := url.Parse(referer)
	if err != nil {
		return "none"
	}
	if ru.Scheme == u.Scheme && ru.Hostname() == u.Hostname() {
		return "same-origin"
	}
	// We don't have PSL access here; conservatively call it cross-site.
	// Same-site would require publicsuffix comparison which the httpclient
	// already pulls in — we accept "cross-site" as a safe default that
	// Chrome would also produce for genuinely different registrable domains.
	if eTLDPlusOne(ru.Hostname()) == eTLDPlusOne(u.Hostname()) {
		return "same-site"
	}
	return "cross-site"
}

// eTLDPlusOne is a minimal PSL stand-in for the two hosts we touch. We only
// classify correctly when both hosts share the same eTLD+1 as one of these
// well-known suffixes; anything else is "different site". Good enough for the
// ADFS/D2L boundary; the real PSL lookup isn't worth a new dependency here.
func eTLDPlusOne(host string) string {
	host = strings.ToLower(host)
	switch {
	case strings.HasSuffix(host, ".cbe.ab.ca"):
		return "cbe.ab.ca"
	case strings.HasSuffix(host, ".google.com"):
		return "google.com"
	default:
		return host
	}
}

// Accept values Chrome ~130 sends on a top-level navigation.
const navAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"

func defaultHeaders(isFormPost bool) map[string]string {
	h := map[string]string{
		"Accept":             navAccept,
		"Accept-Language":    "en-US,en;q=0.9",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Cache-Control":      "no-cache",
		"Pragma":             "no-cache",
		"sec-ch-ua":          ua.SecChUa,
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"Windows"`,
	}
	if !isFormPost {
		h["Cache-Control"] = "max-age=0"
	}
	return h
}

func merge(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func urlMustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
