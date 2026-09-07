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

func LoadEnv(envFile string) error {
	path, err := filepath.Abs(envFile)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("env file not found: %s (create it with CBE_EMAIL and CBE_PASSWORD, or pass --env-file <path>)", envFile)
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