package mfa_test

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	stderrors "github.com/aarav/schooltools/internal/errors"
	"github.com/aarav/schooltools/internal/mfa"
	"github.com/stretchr/testify/assert"
)

func mustDoc(t *testing.T, html string) *goquery.Document {
	t.Helper()
	d, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAdfsLoginFormNoMfa(t *testing.T) {
	html := `<html><body>
		<form id="loginForm" action="/adfs/ls/">
			<input name="UserName" />
			<input name="Password" type="password" />
			<input name="AuthMethod" value="FormsAuthentication" />
		</form>
	</body></html>`
	d := mustDoc(t, html)
	assert.Nil(t, mfa.Detect(d, "https://adfs/login"))
}

func TestDetectsDuoOtpInput(t *testing.T) {
	html := `<html><body>
		<form action="https://api-something.duosecurity.com/...">
			<input name="Duo_Code" />
		</form>
	</body></html>`
	d := mustDoc(t, html)
	got := mfa.Detect(d, "https://adfs/duo")
	assert.NotNil(t, got)
	assert.Equal(t, "MFA_REQUIRED", got.AuthCode())
	var target *stderrors.MfaRequiredError
	assert.ErrorAs(t, got, &target)
}

func TestDetectsMfaByTitle(t *testing.T) {
	html := `<html><head><title>Verify your identity</title></head><body>Loading...</body></html>`
	d := mustDoc(t, html)
	got := mfa.Detect(d, "https://adfs/mfa")
	assert.NotNil(t, got)
}

func TestDetectsMfaByOktaFormHost(t *testing.T) {
	html := `<html><body>
		<form action="https://company.okta.com/sso/saml">
			<input name="SAMLResponse" />
		</form>
	</body></html>`
	d := mustDoc(t, html)
	got := mfa.Detect(d, "https://adfs/mfa")
	assert.NotNil(t, got)
}