package httpclient

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchXSRFTokenFrom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><script>localStorage.setItem('XSRF.Token','tok-123');</script></html>"))
	}))
	defer srv.Close()

	tok, err := FetchXSRFTokenFrom(nil, srv.URL)
	if err != nil {
		t.Fatalf("FetchXSRFTokenFrom: %v", err)
	}
	if tok != "tok-123" {
		t.Errorf("token = %q, want %q", tok, "tok-123")
	}
}

func TestFetchXSRFTokenFromMissingScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>login page</html>"))
	}))
	defer srv.Close()

	if _, err := FetchXSRFTokenFrom(nil, srv.URL); err == nil {
		t.Fatal("expected an error when the XSRF script is absent")
	}
}
