package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Progress is a non-blocking spinner + progress-bar + counter HUD for long
// archive-style passes. On a TTY it renders a multi-line bubbletea view that
// animates a spinner, fills a gradient bar, and prints running counters; on
// a non-TTY it falls back to plain stderr lines so the same call sites work
// in cronjobs and CI.
//
// Progress is safe for one writer goroutine (the worker that performs the
// pass) to call Update* methods concurrently with the bubbletea render loop.
// Stop blocks until the bubbletea program has released the terminal.
type Progress struct {
	title string
	total int
	done  int
	msg   string
	stats Counters

	mu   sync.Mutex
	prog *tea.Program
	tty  bool
	once sync.Once
}

// Counters are the running totals rendered under the progress bar.
type Counters struct {
	Checked int
	Fetched int
	Stale   int
}

// ProgressOptions tunes the HUD.
type ProgressOptions struct {
	Title string // top-line title (e.g. "Archiving")
	Total int    // total work units for the bar; 0 means indeterminate
}

// NewProgress returns an unstarted HUD. Call Start to begin rendering.
func NewProgress(opts ProgressOptions) *Progress {
	return &Progress{
		title: opts.Title,
		total: opts.Total,
		tty:   term.IsTerminal(int(os.Stderr.Fd())),
	}
}

// Start launches the bubbletea program in the background (or begins
// non-TTY logging). Idempotent; subsequent calls are no-ops.
func (p *Progress) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.once.Do(func() {
		if !p.tty {
			return
		}
		m := newProgressModel(p.title, p.total)
		p.prog = tea.NewProgram(m, tea.WithOutput(os.Stderr))
		go p.prog.Run()
	})
}

// Stop finalises the HUD, prints the summary line, and (for TTY) waits for
// the bubbletea program to release the terminal.
func (p *Progress) Stop() {
	p.mu.Lock()
	if !p.tty {
		p.mu.Unlock()
		p.writeSummary(os.Stderr)
		return
	}
	prog := p.prog
	p.mu.Unlock()
	if prog != nil {
		prog.Send(progQuitMsg{})
		// Give bubbletea a moment to release the terminal before we write
		// the summary, otherwise our line collides with the rendered view.
		time.Sleep(50 * time.Millisecond)
	}
	p.writeSummary(os.Stderr)
}

// SetMessage updates the "current activity" line.
func (p *Progress) SetMessage(msg string) {
	p.mu.Lock()
	p.msg = msg
	p.mu.Unlock()
	p.send(progUpdateMsg{msg: msg})
}

// SetTotal updates the bar's denominator after Start. Useful when the
// course count is only known after the manageCourses listing.
func (p *Progress) SetTotal(total int) {
	p.mu.Lock()
	p.total = total
	p.mu.Unlock()
	p.send(setTotalMsg{total: total})
}

// Increment advances the bar by n units (clamped at total).
func (p *Progress) Increment(n int) {
	p.mu.Lock()
	p.done += n
	if p.total > 0 && p.done > p.total {
		p.done = p.total
	}
	d := p.done
	t := p.total
	p.mu.Unlock()
	p.send(setDoneMsg{done: d, total: t})
}

// SetCounters overwrites the running topic counters. Values are absolute;
// pass the totals seen so far, not deltas.
func (p *Progress) SetCounters(c Counters) {
	p.mu.Lock()
	p.stats = c
	stats := p.stats
	p.mu.Unlock()
	p.send(setStatsMsg{stats: stats})
}

// AddCounters bumps the topic counters by the given deltas. Most callers
// should prefer SetCounters, which is unambiguous.
func (p *Progress) AddCounters(c Counters) {
	p.mu.Lock()
	p.stats.Checked += c.Checked
	p.stats.Fetched += c.Fetched
	p.stats.Stale += c.Stale
	stats := p.stats
	p.mu.Unlock()
	p.send(setStatsMsg{stats: stats})
}

// send is the single entry point used by all public mutators.
func (p *Progress) send(msg tea.Msg) {
	p.mu.Lock()
	tty := p.tty
	prog := p.prog
	p.mu.Unlock()
	if !tty || prog == nil {
		return
	}
	prog.Send(msg)
}

// flush is a no-op kept for future use.
func (p *Progress) flush() {}

func (p *Progress) writeSummary(w io.Writer) {
	p.mu.Lock()
	stats := p.stats
	total := p.total
	done := p.done
	p.mu.Unlock()
	fmt.Fprintf(w, "%s done — %d/%d course%s, %d topics checked, %d fetched, %d unchanged\n",
		p.title, done, total, plural(done),
		stats.Checked, stats.Fetched, stats.Stale)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- bubbletea model --------------------------------------------------------

type progUpdateMsg struct{ msg string }
type setTotalMsg struct{ total int }
type setDoneMsg struct{ done, total int }
type setStatsMsg struct{ stats Counters }
type progQuitMsg struct{}

type progressModel struct {
	title    string
	spn      spinner.Model
	bar      progress.Model
	msg      string
	done     int
	total    int
	stats    Counters
	width    int
	style    lipgloss.Style
	quitting bool
}

func newProgressModel(title string, total int) *progressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	bar := progress.New(
		progress.WithGradient("#5A56E0", "#EE6FF8"),
		progress.WithoutPercentage(),
		progress.WithWidth(40),
	)

	return &progressModel{
		title:  title,
		spn:    s,
		bar:    bar,
		total:  total,
		width:  termWidth(),
		style:  lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	}
}

func termWidth() int {
	w, _, err := term.GetSize(int(os.Stderr.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	if w > 120 {
		w = 120
	}
	return w
}

func (m *progressModel) Init() tea.Cmd {
	return tea.Batch(m.spn.Tick, m.bar.SetPercent(0))
}

func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		if m.width > 120 {
			m.width = 120
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spn, cmd = m.spn.Update(v)
		return m, cmd
	case progUpdateMsg:
		m.msg = v.msg
		return m, nil
	case setTotalMsg:
		m.total = v.total
		return m, nil
	case setDoneMsg:
		m.done = v.done
		m.total = v.total
		return m, m.updateBar()
	case setStatsMsg:
		m.stats = v.stats
		return m, nil
	case progQuitMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *progressModel) updateBar() tea.Cmd {
	if m.total <= 0 {
		return m.bar.SetPercent(0)
	}
	pct := float64(m.done) / float64(m.total)
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	return m.bar.SetPercent(pct)
}

func (m *progressModel) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(m.style.Render(fmt.Sprintf("%s %s", m.spn.View(), m.title)))
	if m.msg != "" {
		sb.WriteString("\n  ")
		sb.WriteString(m.msg)
	}
	barView := m.bar.View()
	if m.total > 0 {
		sb.WriteString("\n  ")
		sb.WriteString(barView)
		fmt.Fprintf(&sb, "  %d/%d", m.done, m.total)
	} else {
		sb.WriteString("\n  ")
		sb.WriteString(barView)
	}
	fmt.Fprintf(&sb, "\n  topics: %d checked · %d fetched · %d unchanged",
		m.stats.Checked, m.stats.Fetched, m.stats.Stale)
	return sb.String()
}
