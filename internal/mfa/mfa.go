package mfa

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/aarav/schooltools/internal/errors"
)

var mfaInputNames = regexp.MustCompile(`(?i)^(Duo_Code|Duo_Session_Id|Otp_Code|OTPCode|verificationCode|passcode|otpCode|totp|code)$`)

var mfaTitle = regexp.MustCompile(`(?i)verify (your identity|with)|two[- ]step|multi[- ]factor|\bmfa\b|one[- ]time password|\botp\b`)

var mfaHost = regexp.MustCompile(`(?i)(duosecurity|duo\.com|okta\.com|pingone|pingidentity|azure\.com|adfs.*/mfa)`)

// Detect inspects the HTML doc and final URL for any of three MFA signals:
// input name, page title, or form action host. Returns an MfaRequiredError or
// nil. Mirrors the three-signal TS implementation verbatim.
func Detect(doc *goquery.Document, finalURL string) *errors.MfaRequiredError {
	// Input name match.
	found := false
	doc.Find("input[name]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		name, _ := s.Attr("name")
		if mfaInputNames.MatchString(name) {
			found = true
			return false
		}
		return true
	})
	if found {
		return &errors.MfaRequiredError{URL: finalURL}
	}
	// Title match.
	if title := strings.TrimSpace(doc.Find("title").First().Text()); title != "" {
		if mfaTitle.MatchString(title) {
			return &errors.MfaRequiredError{URL: finalURL}
		}
	}
	// Form action host match.
	actionHit := false
	doc.Find("form[action]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		action, _ := s.Attr("action")
		if mfaHost.MatchString(action) {
			actionHit = true
			return false
		}
		return true
	})
	if actionHit {
		return &errors.MfaRequiredError{URL: finalURL}
	}
	return nil
}