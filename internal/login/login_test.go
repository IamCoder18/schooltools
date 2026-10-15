package login

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/aarav/schooltools/internal/httpclient"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	data, _ := io.ReadAll(r)
	return string(data)
}

func TestFetchPostDumpsOnlyWhenVerbose(t *testing.T) {
	secret := "supersecretvalue-but-very-long-cookie-tail"
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.SetCookie(w, &http.Cookie{Name: "d2lSessionVal", Value: secret, Path: "/"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	jar, err := httpclient.NewJar()
	if err != nil {
		t.Fatalf("NewJar: %v", err)
	}

	out := captureStderr(t, func() {
		res, perr := fetchPost(srv.URL, "UserName=x", "", jar, Options{})
		if perr != nil {
			t.Errorf("non-verbose fetchPost failed: %v", perr)
		}
		if res == nil || res.StatusCode != http.StatusOK {
			t.Error("non-verbose fetchPost did not complete the request")
		}
	})
	if out != "" {
		t.Errorf("fetchPost without --verbose wrote to stderr: %q", out)
	}
	if requests != 1 {
		t.Errorf("server saw %d requests, want 1 (the non-verbose POST must still be sent)", requests)
	}

	out = captureStderr(t, func() {
		if _, perr := fetchPost(srv.URL, "UserName=x", "", jar, Options{Verbose: true}); perr != nil {
			t.Errorf("verbose fetchPost failed: %v", perr)
		}
	})
	if !strings.Contains(out, "Set-Cookie") {
		t.Errorf("verbose fetchPost did not dump Set-Cookie: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("verbose fetchPost leaked the full cookie value: %q", out)
	}
	if requests != 2 {
		t.Errorf("server saw %d requests, want 2", requests)
	}
}

func TestDumpReqHeadersRedactsSAML(t *testing.T) {
	assertion := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol">secret-assertion</samlp:Response>`
	target := "https://adfsfba.cbe.ab.ca/adfs/ls/?SAMLRequest=" + url.QueryEscape(assertion)
	req := httptest.NewRequest(http.MethodPost, target, nil)

	out := captureStderr(t, func() { dumpReqHeaders(req) })
	if strings.Contains(out, assertion) {
		t.Errorf("dumpReqHeaders leaked the SAML assertion: %q", out)
	}
	if !strings.Contains(out, "SAMLRequest") {
		t.Errorf("dump should still name the parameter: %q", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("dump should mark the value redacted: %q", out)
	}
}

func TestDumpReqHeadersRedactsReferer(t *testing.T) {
	assertion := `<samlp:AuthnRequest xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol">secret</samlp:AuthnRequest>`
	referer := "https://adfsfba.cbe.ab.ca/adfs/ls/?SAMLRequest=" + url.QueryEscape(assertion)
	req := httptest.NewRequest(http.MethodPost, "https://d2l.cbe.ab.ca/d2l/lp/auth/login/samlLogin.d2l", nil)
	req.Header.Set("Referer", referer)

	out := captureStderr(t, func() { dumpReqHeaders(req) })
	if strings.Contains(out, assertion) {
		t.Errorf("dumpReqHeaders leaked the SAML assertion via Referer: %q", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("Referer value should be redacted: %q", out)
	}
}

func TestDumpRespSetCookiesRedactsValue(t *testing.T) {
	res := &http.Response{Header: http.Header{}}
	res.Header.Add("Set-Cookie", "d2lSessionVal=abcdef0123456789abcdef0123456789; Path=/; Secure; HttpOnly")

	out := captureStderr(t, func() { dumpRespSetCookies(res) })
	if strings.Contains(out, "abcdef0123456789") {
		t.Errorf("dumpRespSetCookies leaked the cookie value: %q", out)
	}
	if !strings.Contains(out, "d2lSessionVal=<redacted 32 bytes>") {
		t.Errorf("expected a redacted marker with the value length: %q", out)
	}
	if !strings.Contains(out, "Path=/") {
		t.Errorf("cookie attributes should survive for debugging: %q", out)
	}
}

func TestDumpReqHeadersNonSAMLURLUnchanged(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://d2l.cbe.ab.ca/d2l/api/le/1.97/123/content/toc", nil)
	out := captureStderr(t, func() { dumpReqHeaders(req) })
	if !strings.Contains(out, "/content/toc") {
		t.Errorf("non-SAML URL should be printed verbatim: %q", out)
	}
	if strings.Contains(out, "redacted") {
		t.Errorf("non-SAML URL should not be redacted: %q", out)
	}
}

func TestRedactSAMLUnparseableURL(t *testing.T) {
	if got := redactSAML("ht tp://bad url"); got != "<unparseable url redacted>" {
		t.Errorf("redactSAML = %q, want the full URL redacted", got)
	}
}
