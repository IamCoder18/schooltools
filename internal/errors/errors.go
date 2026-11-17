package errors

type AuthError struct {
	Code    string
	Message string
}

func (e *AuthError) Error() string { return e.Message }

// AuthCode lets callers use the same `interface{ AuthCode() string }`
// assertion against every auth failure type, so the machine-readable
// code always reaches the auth log instead of a per-call-site fallback.
func (e *AuthError) AuthCode() string { return e.Code }

func New(code, message string) *AuthError {
	return &AuthError{Code: code, Message: message}
}

type SamlCookieSummary struct {
	Key    string
	Domain string
}

type SamlConsumedError struct {
	LastURL string
	Cookies []SamlCookieSummary
}

func (e *SamlConsumedError) Error() string {
	return "SAML request appears to have been consumed without producing a session: " + e.LastURL
}

func (e *SamlConsumedError) AuthCode() string { return "SAML_CONSUMED" }

type MfaRequiredError struct {
	URL string
}

func (e *MfaRequiredError) Error() string {
	return "MFA / second-factor required at " + e.URL +
		". schooltools cannot complete ADFS MFA challenges; sign in interactively once to clear this."
}

func (e *MfaRequiredError) AuthCode() string { return "MFA_REQUIRED" }

type AdfsRejectedError struct {
	Reason string
}

func (e *AdfsRejectedError) Error() string {
	return "ADFS rejected the login: " + e.Reason
}

func (e *AdfsRejectedError) AuthCode() string { return "ADFS_REJECTED" }
