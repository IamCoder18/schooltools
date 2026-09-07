package login

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/aarav/schooltools/internal/cookies"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/mfa"
	"github.com/aarav/schooltools/internal/retry"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

const (
	StageD2LToADFS = "d2l->adfs"
	StageADFSForms = "adfs-forms"
	StageSAMLPost  = "saml-post"

	HopsD2LToADFS = 3
	HopsADFSForms = 6
	HopsSAMLPost  = 8
)

var adfsErrorRegex = regexp.MustCompile(`(?i)problem|invalid|incorrect|failed|denied|locked|expired`)

var adfsErrorSelectors = []string{
	"#errorMessage",
	"#errorText",
	".error",
	"#content .pageTitle",
}

// Options mirrors the TS LoginOpts.
type Options struct {
	EnvFile     string
	Verbose     bool
	KMSI        bool
	CaptureSAML bool // capture the SAMLResponse that produced the session (default true from callers)
}

// Result is what callers receive on a successful login. Cookies are a snapshot
// of every cookie stored in the jar, ready for persistence. SAML, when
// non-nil, is the SAMLResponse that produced the session, captured during the
// final saml-post stage. Token, when non-nil, is the Brightspace OAuth access
// token used by api.brightspace.com endpoints.
type Result struct {
	Cookies []cookies.Record
	SAML    *session.SAMLRecord
	Token   *session.TokenRecord
}

// Run executes the ADFS SAML form-post hop machine. Mirrors src/commands/login.ts.
func Run(opts Options) (Result, error) {
	creds, err := readCreds()
	if err != nil {
		return Result{}, err
	}

	jar, err := httpclient.NewJar()
	if err != nil {
		return Result{}, err
	}
	origins := map[string]struct{}{originOf(ua.D2LBase): {}}

	verbose(opts, "[stage "+StageD2LToADFS+"] GET "+ua.LoginEndpoint)
	r1, err := fetchGet(ua.LoginEndpoint, jar, "", opts)
	if err != nil {
		return Result{}, err
	}
	origins[originOf(r1.Request.URL.String())] = struct{}{}
	if !httpclient.IsRedirect(r1.StatusCode) {
		return Result{}, stderrors.New("D2L_NO_REDIRECT",
			fmt.Sprintf("D2L login did not redirect (got %d)", r1.StatusCode))
	}
	loc1 := r1.Header.Get("Location")
	if loc1 == "" {
		return Result{}, stderrors.New("NO_LOCATION", "first redirect had no Location")
	}
	adfsURL := resolve(loc1, r1.Request.URL.String())
	verbose(opts, fmt.Sprintf("[stage %s] %d -> %s", StageD2LToADFS, r1.StatusCode, adfsURL))
	origins[originOf(adfsURL)] = struct{}{}

	r2, err := fetchGet(adfsURL, jar, r1.Request.URL.String(), opts)
	if err != nil {
		return Result{}, err
	}
	origins[originOf(r2.Request.URL.String())] = struct{}{}
	if r2.StatusCode != http.StatusOK {
		return Result{}, stderrors.New("ADFS_PAGE",
			fmt.Sprintf("ADFS login page returned %d", r2.StatusCode))
	}
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "[2] %d %s\n", r2.StatusCode, r2.Request.URL.String())
	}
	loginHTML, err := io.ReadAll(r2.Body)
	if err != nil {
		return Result{}, err
	}
	_ = r2.Body.Close()
	if opts.Verbose {
		_ = os.WriteFile("/tmp/kilo/schooltools-debug-adfs-login.html", loginHTML, 0o600)
	}
	loginDoc, err := goquery.NewDocumentFromReader(strings.NewReader(string(loginHTML)))
	if err != nil {
		return Result{}, err
	}
	loginForm := loginDoc.Find("#loginForm")
	if loginForm.Length() == 0 {
		p := debugPath("no-login-form")
		_ = os.WriteFile(p, loginHTML, 0o600)
		if m := mfa.Detect(loginDoc, r2.Request.URL.String()); m != nil {
			return Result{}, m
		}
		return Result{}, stderrors.New("NO_LOGIN_FORM",
			fmt.Sprintf("Could not find ADFS #loginForm. Saved page to %s. SAMLRequest may have expired; retry.", p))
	}
	action, _ := loginForm.Attr("action")
	if action == "" {
		return Result{}, stderrors.New("FORM_NO_ACTION", "ADFS login form has no action attribute")
	}
	loginPostURL := resolve(action, adfsURL)

	body := url.Values{
		"UserName":   {creds.email},
		"Password":   {creds.password},
		"Kmsi":       {kmsiString(opts.KMSI)},
		"AuthMethod": {"FormsAuthentication"},
	}.Encode()

	res, err := fetchPost(loginPostURL, body, adfsURL, jar)
	if err != nil {
		return Result{}, err
	}
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "[3] %d %s\n", res.StatusCode, res.Request.URL.String())
	}

	stage := StageADFSForms
	hopsInStage := 0
	hopsTotal := 0
	var pendingSAML *session.SAMLRecord

	for {
		budget := budgetFor(stage)
		if hopsInStage >= budget {
			return Result{}, &stderrors.SamlConsumedError{
				LastURL: res.Request.URL.String(),
				Cookies: collectSummaries(jar, origins),
			}
		}
		hopsInStage++
		hopsTotal++
verbose(opts, fmt.Sprintf("[hop %d] %s #%d: %d %s", hopsTotal, stage, hopsInStage, res.StatusCode, res.Request.URL.String()))
	if opts.Verbose {
		if loc := res.Header.Get("Location"); loc != "" {
			fmt.Fprintf(os.Stderr, "           location: %s\n", loc)
		}
		for _, sc := range res.Cookies() {
			fmt.Fprintf(os.Stderr, "           set-cookie: %s=%s domain=%q path=%q expires=%v secure=%v httponly=%v\n",
				sc.Name, truncateForLog(sc.Value), sc.Domain, sc.Path, sc.Expires, sc.Secure, sc.HttpOnly)
		}
	}
	origins[originOf(res.Request.URL.String())] = struct{}{}

		current := httpclient.CookiesForHost(jar, res.Request.URL.Host)
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "           jar cookies for %s: %d\n", res.Request.URL.Host, len(current))
			for _, c := range current {
				fmt.Fprintf(os.Stderr, "             - %s domain=%q expires=%v\n", c.Name, c.Domain, c.Expires)
			}
		}
		if session.HasValidSession(current) {
			verbose(opts, fmt.Sprintf("[hop %d] hasValidSession=true", hopsTotal))
			break
		}

		if strings.Contains(res.Request.URL.Host, "d2l.cbe.ab.ca") {
			return Result{}, &stderrors.SamlConsumedError{
				LastURL: res.Request.URL.String(),
				Cookies: collectSummaries(jar, origins),
			}
		}

		if httpclient.IsRedirect(res.StatusCode) {
			loc := res.Header.Get("Location")
			if loc == "" {
				return Result{}, stderrors.New("NO_LOCATION", fmt.Sprintf("Redirect %d with no Location", res.StatusCode))
			}
			nextURL := resolve(loc, res.Request.URL.String())
			verbose(opts, "           -> "+nextURL)
			newStage := stageFor(nextURL, stage)
			if newStage != stage {
stage = newStage
			hopsInStage = 0
		}
		_ = res.Body.Close()
		res, err = fetchGet(nextURL, jar, res.Request.URL.String(), opts)
			if err != nil {
				return Result{}, err
			}
			continue
		}

		ct := res.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			return Result{}, stderrors.New("UNEXPECTED_CT",
				fmt.Sprintf("Unexpected non-HTML response (%d, %s) at %s", res.StatusCode, ct, res.Request.URL.String()))
		}

		html, err := io.ReadAll(res.Body)
		if err != nil {
			return Result{}, err
		}
		_ = res.Body.Close()
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
		if err != nil {
			return Result{}, err
		}
		if m := mfa.Detect(doc, res.Request.URL.String()); m != nil {
			return Result{}, m
		}
		if msg := pickErrorText(doc, adfsErrorSelectors); msg != "" && adfsErrorRegex.MatchString(msg) {
			return Result{}, &stderrors.AdfsRejectedError{Reason: firstSentence(msg)}
		}

		form := pickForm(doc)
		if form.Length() == 0 {
			p := debugPath("no-form")
			_ = os.WriteFile(p, html, 0o600)
			return Result{}, stderrors.New("NO_FORM",
				fmt.Sprintf("ADFS returned HTML with no form at %s. Title: %s. Saved to %s.",
					res.Request.URL.String(), strings.TrimSpace(doc.Find("title").First().Text()), p))
		}
		if opts.Verbose {
			dumpForms(doc, opts)
			// Form-scoped debug save happens after inputs are populated.
		}
		formAction, _ := form.Attr("action")
		if formAction == "" {
			return Result{}, stderrors.New("FORM_NO_ACTION", "Form has no action")
		}
		inputs := map[string]string{}
		form.Find("input[name],textarea[name]").Each(func(_ int, s *goquery.Selection) {
			name, _ := s.Attr("name")
			if name == "" {
				return
			}
			val, _ := s.Attr("value")
			inputs[name] = val
		})
		if opts.Verbose && len(inputs) < 2 {
			p := debugPath("few-fields")
			_ = os.WriteFile(p, html, 0o600)
			fmt.Fprintf(os.Stderr, "           saved page with %d fields to %s\n", len(inputs), p)
		}

		postBody := url.Values{}
		for k, v := range inputs {
			postBody.Set(k, v)
		}
		postURL := resolve(formAction, res.Request.URL.String())
		verbose(opts, fmt.Sprintf("           POST %s (%d fields)", postURL, len(postBody)))

		lower := strings.ToLower(postURL)
		isSAMLPush := strings.Contains(lower, "/saml/") || strings.Contains(lower, "samllogin") || strings.Contains(lower, "samlresponse")
		if isSAMLPush {
			if samlResp := postBody.Get("SAMLResponse"); samlResp != "" && pendingSAML == nil {
				rec := session.NewSAMLRecord(samlResp, postBody.Get("RelayState"), time.Now().UTC())
				pendingSAML = &rec
				verbose(opts, fmt.Sprintf("           captured SAMLResponse (%d bytes)", len(samlResp)))
			}
			stage = StageSAMLPost
			hopsInStage = 0
		}

		res, err = fetchPost(postURL, postBody.Encode(), res.Request.URL.String(), jar)
		if err != nil {
			return Result{}, err
		}
	}

	all := collectRecords(jar, origins)
	if !session.HasValidSession(all) {
		return Result{}, &stderrors.SamlConsumedError{
			LastURL: res.Request.URL.String(),
			Cookies: collectSummaries(jar, origins),
		}
	}

	out := Result{Cookies: all}
	if opts.CaptureSAML && pendingSAML != nil {
		out.SAML = pendingSAML
	}
	// Mint the Brightspace OAuth token (used by api.brightspace.com) using
	// the now-authenticated cookie jar. Failure is non-fatal: the LE APIs
	// work fine on session cookies alone.
	if rec, err := mintBrightspaceToken(jar); err != nil {
		verbose(opts, fmt.Sprintf("[token] capture skipped: %v", err))
	} else if rec != nil {
		out.Token = rec
		verbose(opts, fmt.Sprintf("[token] captured Brightspace token (valid until %s)", rec.ExpiresAt.Format(time.RFC3339)))
	}
	return out, nil
}

func readCreds() (struct{ email, password string }, error) {
	email := os.Getenv("CBE_EMAIL")
	password := os.Getenv("CBE_PASSWORD")
	if email == "" || password == "" {
		return struct{ email, password string }{}, fmt.Errorf("CBE_EMAIL and CBE_PASSWORD must be set in the env file")
	}
	return struct{ email, password string }{email: email, password: password}, nil
}

func kmsiString(on bool) string {
	if on {
		return "true"
	}
	return "false"
}

func budgetFor(stage string) int {
	switch stage {
	case StageD2LToADFS:
		return HopsD2LToADFS
	case StageADFSForms:
		return HopsADFSForms
	case StageSAMLPost:
		return HopsSAMLPost
	default:
		return 5
	}
}

func stageFor(rawURL, fallback string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fallback
	}
	if strings.Contains(u.Host, "d2l.cbe.ab.ca") {
		return StageSAMLPost
	}
	if strings.Contains(rawURL, "/adfs/") {
		return StageADFSForms
	}
	return fallback
}

// collectRecords gathers every cookie in the jar across the origins we've
// visited, deduplicating by domain+path+name, and converts them to Record so
// the caller can persist them. The request hostname (port stripped) is passed
// so host-only cookies keep their origin in the persisted record.
func collectRecords(jar *cookiejar.Jar, origins map[string]struct{}) []cookies.Record {
	seen := map[string]struct{}{}
	for origin := range origins {
		u, err := url.Parse(origin)
		if err != nil {
			continue
		}
		for _, c := range jar.Cookies(u) {
			key := c.Domain + "|" + c.Path + "|" + c.Name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
	}
	records := make([]cookies.Record, 0, len(seen))
	for origin := range origins {
		u, err := url.Parse(origin)
		if err != nil {
			continue
		}
		for _, c := range jar.Cookies(u) {
			key := c.Domain + "|" + c.Path + "|" + c.Name
			if _, ok := seen[key]; !ok {
				continue
			}
			delete(seen, key)
			records = append(records, cookies.FromHTTP(c, u.Hostname()))
		}
	}
	return records
}

// collectSummaries returns the cookie name/domain pairs (no values) for error
// reporting.
func collectSummaries(jar *cookiejar.Jar, origins map[string]struct{}) []stderrors.SamlCookieSummary {
	records := collectRecords(jar, origins)
	out := make([]stderrors.SamlCookieSummary, len(records))
	for i, r := range records {
		out[i] = stderrors.SamlCookieSummary{Key: r.Name, Domain: r.Domain}
	}
	return out
}

func pickErrorText(doc *goquery.Document, selectors []string) string {
	for _, sel := range selectors {
		t := strings.TrimSpace(doc.Find(sel).First().Text())
		if t != "" {
			return t
		}
	}
	return ""
}

// pickForm chooses the form most likely to be the one we want to submit.
// Preference order:
//  1. Cross-origin forms (action host differs from the page) that carry at
//     least one non-button named field — i.e. real hidden inputs, not just a
//     styled submit button.
//  2. Any cross-origin form.
//  3. Same-origin form with real hidden fields.
//  4. The first form on the page.
func pickForm(doc *goquery.Document) *goquery.Selection {
	var first, crossOrigin *goquery.Selection
	doc.Find("form").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if first == nil {
			first = s
		}
		action, _ := s.Attr("action")
		if action == "" {
			return true
		}
		u, err := url.Parse(action)
		if err != nil || !u.IsAbs() || u.Host == "" {
			return true
		}
		if hasRealFields(s) {
			crossOrigin = s
			return false
		}
		if crossOrigin == nil {
			crossOrigin = s
		}
		return true
	})
	if crossOrigin != nil && hasRealFields(crossOrigin) {
		return crossOrigin
	}
	if crossOrigin != nil {
		return crossOrigin
	}
	if first != nil && hasRealFields(first) {
		return first
	}
	return first
}

// hasRealFields reports whether the form has at least one named field that
// looks like data (input[type=hidden], input[type=text], textarea[name])
// rather than just a submit button.
func hasRealFields(form *goquery.Selection) bool {
	found := false
	form.Find("input[name],textarea[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		tag := goquery.NodeName(s)
		if tag == "textarea" {
			found = true
			return false
		}
		t, _ := s.Attr("type")
		if t == "" || t == "hidden" || t == "text" || t == "email" || t == "password" {
			found = true
			return false
		}
		return true
	})
	return found
}

// dumpForms logs each form's action and the count of named inputs/textareas.
func dumpForms(doc *goquery.Document, opts Options) {
	i := 0
	doc.Find("form").Each(func(_ int, s *goquery.Selection) {
		i++
		action, _ := s.Attr("action")
		n := 0
		s.Find("input[name],textarea[name]").Each(func(_ int, _ *goquery.Selection) { n++ })
		fmt.Fprintf(os.Stderr, "           form[%d] action=%s fields=%d\n", i, action, n)
	})
}

func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '.' {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func verbose(opts Options, msg string) {
	if opts.Verbose {
		fmt.Fprintln(os.Stderr, msg)
	}
}

func fetchGet(rawURL string, jar *cookiejar.Jar, referer string, opts Options) (*http.Response, error) {
	out, err := retry.WithRetry(
		func() (*http.Response, error) { return httpclient.Fetch(rawURL, jar, httpclient.FetchOptions{Referer: referer}) },
		retry.Options{},
		func(err error) bool { return !isMFA(err) },
	)
	if err != nil {
		verbose(opts, fmt.Sprintf("[net] GET %s failed: %v", rawURL, err))
	} else if opts.Verbose && out != nil && out.Request != nil {
		dumpReqHeaders(out.Request)
		dumpRespSetCookies(out)
	}
	return out, err
}

func fetchPost(rawURL, body, referer string, jar *cookiejar.Jar) (*http.Response, error) {
	res, err := httpclient.Fetch(rawURL, jar, httpclient.FetchOptions{
		Method:  http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    body,
		Referer: referer,
	})
	if res != nil && res.Request != nil {
		dumpReqHeaders(res.Request)
		dumpRespSetCookies(res)
	}
	return res, err
}

// dumpRespSetCookies prints raw Set-Cookie header values verbatim — useful
// for confirming whether the server actually sent Expires / Max-Age that our
// cookie parser might be losing.
func dumpRespSetCookies(res *http.Response) {
	for _, sc := range res.Header.Values("Set-Cookie") {
		fmt.Fprintf(os.Stderr, "             << Set-Cookie: %s\n", sc)
	}
}

// dumpReqHeaders prints the request headers in a compact form so verbose
// mode shows what we actually sent to the server. Body is intentionally
// omitted from the dump.
func dumpReqHeaders(r *http.Request) {
	if r == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "           > %s %s\n", r.Method, r.URL.String())
	keys := []string{"User-Agent", "Accept", "Accept-Language", "Accept-Encoding",
		"Referer", "Origin", "Content-Type", "Cache-Control",
		"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
		"Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site", "Sec-Fetch-User",
		"Upgrade-Insecure-Requests", "Pragma"}
	for _, k := range keys {
		if v := r.Header.Get(k); v != "" {
			fmt.Fprintf(os.Stderr, "             %s: %s\n", k, v)
		}
	}
}

func isMFA(err error) bool {
	_, ok := err.(*stderrors.MfaRequiredError)
	return ok
}

func resolve(ref, base string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		return u.String()
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Scheme + "://" + u.Host
}

func debugPath(label string) string {
	ts := time.Now().UTC().Format("2006-01-02T15-04-05.000")
	ts = strings.ReplaceAll(ts, ".", "-")
	ts = strings.ReplaceAll(ts, ":", "-")
	return filepath.Join("/tmp/kilo", fmt.Sprintf("schooltools-debug-%s-%s.html", label, ts))
}

func truncateForLog(s string) string {
	if len(s) > 20 {
		return s[:20] + "..."
	}
	return s
}