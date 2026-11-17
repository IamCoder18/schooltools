package env

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Creds struct {
	Email    string
	Password string
}

// SISCreds holds PowerSchool MyCBE credentials. The account is a student
// number, not an email — the CBE portal uses the student ID for the
// parent/student login.
type SISCreds struct {
	Account  string
	Password string
}

func LoadEnv(envFile string) error {
	path, err := filepath.Abs(envFile)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("env file not found: %s (create it with CBE_EMAIL and CBE_PASSWORD, or pass --env-file <path>): %w", envFile, err)
		}
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`)
		_ = os.Setenv(key, val)
	}
	return scanner.Err()
}

func GetCreds() (Creds, error) {
	email := os.Getenv("CBE_EMAIL")
	password := os.Getenv("CBE_PASSWORD")
	if email == "" || password == "" {
		return Creds{}, fmt.Errorf("CBE_EMAIL and CBE_PASSWORD must be set in the env file; these are read from environment only and never logged")
	}
	return Creds{Email: email, Password: password}, nil
}

// GetSISCreds returns the PowerSchool SIS account + password.
//
// PowerSchool MyCBE uses the **student number**, not an email, so the
// obvious CBE_EMAIL fallback would silently try an email address as the
// username and fail. We prefer SIS_STUDENT_NUMBER, then
// CBE_STUDENT_NUMBER, then CBE_EMAIL — in that order — and we warn on
// stderr if the value still looks like an email.
func GetSISCreds() (SISCreds, error) {
	account := os.Getenv("SIS_STUDENT_NUMBER")
	if account == "" {
		account = os.Getenv("CBE_STUDENT_NUMBER")
	}
	if account == "" {
		account = os.Getenv("CBE_EMAIL")
	}
	password := os.Getenv("SIS_PASSWORD")
	if password == "" {
		password = os.Getenv("CBE_PASSWORD")
	}
	if account == "" || password == "" {
		return SISCreds{}, fmt.Errorf("PowerSchool credentials missing: set SIS_STUDENT_NUMBER and SIS_PASSWORD (or CBE_STUDENT_NUMBER + CBE_PASSWORD) in the env file")
	}
	if strings.Contains(account, "@") {
		fmt.Fprintf(os.Stderr, "Warning: SIS_STUDENT_NUMBER is unset, so '%s' from CBE_EMAIL was used as the PowerSchool username. MyCBE expects the student number and will reject an email address.\n", account)
	}
	return SISCreds{Account: account, Password: password}, nil
}
