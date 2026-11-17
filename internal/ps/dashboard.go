package ps

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	stderrors "github.com/IamCoder18/schooltools/internal/errors"
	"github.com/IamCoder18/schooltools/internal/httpclient"
)

// sessionStaleError is the single user-facing error for "PowerSchool
// bounced us to the sign-in page". It carries a stable code so the auth
// log and JSON callers can distinguish it from a transport failure.
func sessionStaleError(detail string) error {
	return stderrors.New("PS_SESSION_STALE",
		fmt.Sprintf("PowerSchool redirected to the sign-in page (%s); the saved SIS session is likely expired — run 'schooltools sis login'", detail))
}

func isSigninRedirect(res *http.Response) bool {
	if res == nil || res.StatusCode < 300 || res.StatusCode >= 400 {
		return false
	}
	loc := res.Header.Get("Location")
	return strings.Contains(loc, "login") || strings.Contains(loc, "/public/") || strings.Contains(loc, "signin")
}

// GetDashboardHTML returns the body of the PowerSchool guardian
// dashboard at /guardian/home.html. Used by `sis whoami` to discover
// the student's DCID when SIS_DCID isn't set.
//
// The dashboard is a 59 KB Angular SPA shell — the SPA then issues
// the actual /ws/xte/* data calls. We don't need to wait for those;
// the student record is embedded inside an inline bootstrap script.
func (c *Client) GetDashboardHTML() (string, error) {
	rawURL := c.Base + "/guardian/home.html"
	res, err := c.fetch(http.MethodGet, rawURL, nil, htmlAccept, c.Base+"/public/")
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if isSigninRedirect(res) {
			return "", sessionStaleError(fmt.Sprintf("HTTP %d", res.StatusCode))
		}
		hint := errorHint(res)
		return "", fmt.Errorf("GET %s: HTTP %d %s%s", redactedURL(rawURL), res.StatusCode, res.Status, hint)
	}
	body, err := httpclient.BodyString(res)
	if err != nil {
		return "", err
	}
	_ = res.Body.Close()
	return body, nil
}

var (
	// Pattern: /guardian/credentials.html?frn=00<TABLE><DCID>
	// PowerSchool's FRN format is `<type_code><table_id><record_id>`.
	// The student record's table_id is a single digit on CBE, so the
	// DCID is the trailing 8 digits of the FRN suffix (post "00").
	//
	// We deliberately over-match (capture everything after "00")
	// and then truncate to the last 8 digits in code.
	dashboardStudentFRN = regexp.MustCompile(`/guardian/credentials\.html\?frn=00(\d+)`)
)

// ExtractDCIDFromDashboard walks the dashboard HTML looking for the
// student DCID. Returns 0 when nothing matches — caller should treat
// that as "DCID unknown" and fail loudly rather than silently
// guessing.
//
// PowerSchool's FRN is a packed format `<type_code><table_id><record_id>`.
// On CBE the student credentials link is `frn=00<1 digit><8-digit DCID>`,
// so the DCID is the trailing 8 digits of the FRN suffix.
func ExtractDCIDFromDashboard(html string) int64 {
	m := dashboardStudentFRN.FindStringSubmatch(html)
	if len(m) < 2 {
		return 0
	}
	frnSuffix := m[1]
	if len(frnSuffix) < 8 {
		return 0
	}
	// Take the trailing 8 digits. This handles the "00<1 digit><8 DCID>"
	// shape on CBE without needing to know the exact table_id length.
	d, err := strconv.ParseInt(frnSuffix[len(frnSuffix)-8:], 10, 64)
	if err != nil {
		return 0
	}
	return d
}
