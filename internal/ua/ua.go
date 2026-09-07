package ua

import "regexp"

const (
	// Chrome ~130 on Windows — picked because the version we shipped
	// (152.0.0.0) doesn't exist and ADFS/D2L can downgrade non-browser
	// sessions on UA mismatch. 130 is well within the cutoff.
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

	// SecChUa matches what Chrome 130 sends.
	SecChUa = `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="24"`

	D2LBase = "https://d2l.cbe.ab.ca"

	LoginEndpoint = D2LBase + "/d2l/lp/auth/saml/login"

	// LEAPIVersion is the hardcoded Learning Environment product version.
	// Per D2L_API_RESEARCH.md §3, version discovery currently fails on CBE
	// with our stored session. The post-1.0 version-discovery item from
	// the CLI-feature-parity plan will turn this into a runtime call.
	LEAPIVersion = "1.47"

	// LPAPIVersion is the hardcoded Learning Platform product version.
	LPAPIVersion = "1.47"

	// BASAPIVersion is the hardcoded Awards/Badges/Certificates product
	// version. Discovered via /d2l/api/bas/versions/.
	BASAPIVersion = "1.6"

	// EPAPIVersion is the hardcoded ePortfolio product version. The CBE
	// tenant disables eP for students (every /d2l/api/eP/ call returns
	// 403 Forbidden), so this constant is only used as the fallback when
	// discovery fails — ePortfolio itself is out of scope for now.
	EPAPIVersion = "2.5"
)

var loginPageRegex = regexp.MustCompile(`(?i)/(d2l/)?login(\.aspx)?(\?|$|/)`)
var signinRegex = regexp.MustCompile(`(?i)/signin`)

// IsLoginURL matches D2L/ADFS redirect targets that mean "the session expired".
func IsLoginURL(rawURL string) bool {
	return loginPageRegex.MatchString(rawURL) || signinRegex.MatchString(rawURL)
}
