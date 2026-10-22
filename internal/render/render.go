package render

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const (
	minWidth = 60
	maxWidth = 200
	fallback = 100
)

func Width() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < minWidth {
		return fallback
	}
	if w > maxWidth {
		return maxWidth
	}
	return w
}

func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func color(n lipgloss.TerminalColor) lipgloss.Style {
	s := lipgloss.NewStyle()
	if !ColorEnabled() {
		return s.Foreground(lipgloss.Color(""))
	}
	return s.Foreground(n)
}

const (
	cAccent = lipgloss.Color("39")
	cKey    = lipgloss.Color("245")
	cDim    = lipgloss.Color("243")
	cOK     = lipgloss.Color("78")
	cWarn   = lipgloss.Color("214")
	cBad    = lipgloss.Color("203")
	cInfo   = lipgloss.Color("111")
	cTitle  = lipgloss.Color("81")
)

func Bold(s string) string   { return lipgloss.NewStyle().Bold(true).Render(s) }
func Muted(s string) string  { return color(cDim).Render(s) }
func Accent(s string) string { return color(cAccent).Bold(true).Render(s) }

func OK(s string) string   { return color(cOK).Render(s) }
func Warn(s string) string { return color(cWarn).Render(s) }
func Bad(s string) string  { return color(cBad).Render(s) }
func Info(s string) string { return color(cInfo).Render(s) }

func Title(s string) string {
	rule := strings.Repeat("─", maxInt(0, Width()-utf8.RuneCountInString(s)-3))
	out := color(cTitle).Bold(true).Render(s)
	if rule == "" {
		return out + "\n"
	}
	return out + "  " + color(cDim).Render(rule) + "\n"
}

func Section(s string) string {
	if s == "" {
		return "\n"
	}
	return "\n" + color(cAccent).Bold(true).Render(s) + "\n"
}

func Label(s string) string { return color(cKey).Render(s) }

type Row struct {
	Key   string
	Value string
}

func KV(rows ...Row) string {
	keyw := 0
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.Key); n > keyw {
			keyw = n
		}
	}
	gap := 2
	valw := Width() - keyw - gap
	if valw < 20 {
		valw = 20
	}
	var b strings.Builder
	for _, r := range rows {
		if r.Key == "" && r.Value == "" {
			b.WriteString("\n")
			continue
		}
		pad := strings.Repeat(" ", maxInt(0, keyw-utf8.RuneCountInString(r.Key)))
		vals := wrapRunes(r.Value, valw)
		for i, line := range vals {
			if i == 0 {
				b.WriteString(Label(r.Key) + pad + "  " + line + "\n")
			} else {
				b.WriteString(strings.Repeat(" ", keyw+gap) + line + "\n")
			}
		}
	}
	return b.String()
}

func Wrap(s string, width int) string {
	return strings.Join(wrapRunes(s, width), "\n")
}

func wrapRunes(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		para = strings.TrimRight(para, " ")
		if para == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			switch {
			case line == "":
				line = w
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func Truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if n <= 1 {
		if s != "" {
			return "…"
		}
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

func Bullets(items ...string) string {
	if len(items) == 0 {
		return ""
	}
	w := Width() - 4
	if w < 20 {
		w = 20
	}
	var b strings.Builder
	for _, it := range items {
		lines := wrapRunes(it, w)
		for i, line := range lines {
			if i == 0 {
				b.WriteString("  " + color(cDim).Render("·") + " " + line + "\n")
			} else {
				b.WriteString("    " + line + "\n")
			}
		}
	}
	return b.String()
}

func Empty(msg string) string {
	return "  " + color(cDim).Render(msg) + "\n"
}

func Hint(format string, args ...any) string {
	return "  " + color(cDim).Render("→ "+fmt.Sprintf(format, args...)) + "\n"
}

func Note(format string, args ...any) string {
	return "  " + color(cWarn).Render(fmt.Sprintf(format, args...)) + "\n"
}

func Count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %s", n, Plural(n, noun))
}

func Plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return pluralize(noun)
}

func pluralize(noun string) string {
	if noun == "" {
		return noun
	}
	lower := strings.ToLower(noun)
	switch {
	case strings.HasSuffix(lower, "quiz"):
		return noun + "zes"
	case strings.HasSuffix(lower, "s"),
		strings.HasSuffix(lower, "x"),
		strings.HasSuffix(lower, "z"),
		strings.HasSuffix(lower, "ch"),
		strings.HasSuffix(lower, "sh"):
		return noun + "es"
	case strings.HasSuffix(lower, "y") && len(noun) >= 2 && !isVowel(rune(noun[len(noun)-2])):
		return noun[:len(noun)-1] + "ies"
	case strings.HasSuffix(lower, "f"):
		return noun[:len(noun)-1] + "ves"
	case strings.HasSuffix(lower, "fe"):
		return noun[:len(noun)-2] + "ves"
	default:
		return noun + "s"
	}
}

func isVowel(r rune) bool {
	switch unicode.ToLower(r) {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type List struct {
	Count int             `json:"count"`
	Items json.RawMessage `json:"items"`
}

func NewList(items any) List {
	raw, err := json.Marshal(items)
	if err != nil {
		raw = []byte("null")
	}
	n := 0
	var probe []json.RawMessage
	if json.Unmarshal(raw, &probe) == nil {
		n = len(probe)
	}
	if string(raw) == "null" {
		raw = []byte("[]")
	}
	return List{Count: n, Items: raw}
}

func PrintJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
}

func Parse(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return time.Time{}, false
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func Rel(iso string) string {
	t, ok := Parse(iso)
	if !ok {
		return ""
	}
	return RelAt(t, time.Now())
}

func RelAt(t, now time.Time) string {
	d := now.Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	var phrase string
	switch {
	case d < time.Minute:
		phrase = "just now"
	case d < time.Hour:
		phrase = Count(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		phrase = Count(int(d.Hours()), "hour")
	case d < 7*24*time.Hour:
		phrase = Count(int(d.Hours()/24), "day")
	case d < 30*24*time.Hour:
		phrase = Count(int(d.Hours()/24/7), "week")
	case d < 365*24*time.Hour:
		phrase = Count(int(d.Hours()/24/30), "month")
	default:
		phrase = Count(int(d.Hours()/24/365), "year")
	}
	if phrase == "just now" {
		return phrase
	}
	if future {
		return "in " + phrase
	}
	return phrase + " ago"
}

func isDateOnly(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func displayTime(iso string) (time.Time, bool) {
	t, ok := Parse(iso)
	if !ok {
		return time.Time{}, false
	}
	if isDateOnly(iso) {
		return t, true
	}
	return t.Local(), true
}

func localPart(iso, layout string) string {
	t, ok := displayTime(iso)
	if !ok {
		return "—"
	}
	return t.Format(layout)
}

func Date(iso string) string {
	t, ok := displayTime(iso)
	if !ok {
		return "—"
	}
	return t.Format("Mon, Jan 2 2006")
}

func DateTime(iso string) string {
	t, ok := displayTime(iso)
	if !ok {
		return "—"
	}
	return t.Format("Mon, Jan 2 2006, 3:04 PM")
}

func Time(iso string) string {
	t, ok := displayTime(iso)
	if !ok {
		return "—"
	}
	return t.Format("3:04 PM")
}

func When(iso string) string {
	t, ok := displayTime(iso)
	if !ok {
		return "—"
	}
	rel := RelAt(t, time.Now())
	return t.Format("Mon, Jan 2 2006, 3:04 PM") + "  ·  " + rel
}

func Countdown(iso string) string {
	r := Rel(iso)
	if r == "" {
		return "—"
	}
	return r
}

func Window(startISO, endISO string) string {
	now := time.Now()
	start, hasStart := Parse(startISO)
	end, hasEnd := Parse(endISO)
	switch {
	case hasStart && hasEnd:
		state := "open"
		switch {
		case now.Before(start):
			state = Warn("opens " + RelAt(start, now))
		case now.After(end):
			state = Bad("closed " + RelAt(end, now))
		default:
			state = OK("closes " + RelAt(end, now))
		}
		return fmt.Sprintf("%s → %s  ·  %s", localPart(startISO, "Jan 2"), localPart(endISO, "Jan 2 3:04 PM"), state)
	case hasEnd:
		if now.After(end) {
			return localPart(endISO, "Mon, Jan 2 2006, 3:04 PM") + "  ·  " + Bad("closed "+RelAt(end, now))
		}
		return localPart(endISO, "Mon, Jan 2 2006, 3:04 PM") + "  ·  " + OK("due "+RelAt(end, now))
	case hasStart:
		return localPart(startISO, "Mon, Jan 2 2006, 3:04 PM") + "  ·  " + RelAt(start, now)
	default:
		return "—"
	}
}

func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days := int(d.Hours() / 24)
		h := int(d.Hours()) % 24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}

func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func StatusWord(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "graded", "complete", "completed", "submitted", "open", "active", "yes", "ok", "released", "published":
		return OK(s)
	case "due soon", "pending", "draft", "upcoming", "in progress", "partial", "warning":
		return Warn(s)
	case "missing", "overdue", "closed", "locked", "inactive", "failed", "error", "not submitted", "unavailable", "no":
		return Bad(s)
	default:
		return Muted(s)
	}
}
