package authlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CookieSummary struct {
	Name    string     `json:"name"`
	Domain  string     `json:"domain"`
	Expires *time.Time `json:"expires,omitempty"`
}

var (
	mu       sync.Mutex
	disabled bool
)

// Path returns the canonical auth log path (~/.config/schooltools/auth.log).
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "schooltools", "auth.log")
}

func SetEnabled(v bool) {
	mu.Lock()
	disabled = !v
	mu.Unlock()
}

func Enabled() bool {
	if os.Getenv("SCHOOLTOOLS_NO_AUTH_LOG") == "1" {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return !disabled
}

func Log(event string, fields map[string]any) error {
	return logInternal(event, fields)
}

func LogCookies(event string, summaries []CookieSummary) error {
	return logInternal(event, map[string]any{"cookies": summaries})
}

func logInternal(event string, fields map[string]any) error {
	if !Enabled() {
		return nil
	}
	entry := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339),
		"event": event,
	}
	for k, v := range fields {
		entry[k] = v
	}
	line, err := json.Marshal(entry)
	if err != nil {
		warn(err)
		return err
	}
	line = append(line, '\n')

	dir := filepath.Dir(Path())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		warn(err)
		return err
	}
	f, err := os.OpenFile(Path(), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		warn(err)
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		warn(err)
		return err
	}
	return nil
}

func warn(err error) {
	fmt.Fprintln(os.Stderr, "authlog:", err)
}