package ui

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// WithSpinner runs work while displaying a bubbletea spinner if stderr is a
// TTY. The progress callback can update the spinner message mid-flight. On
// non-TTY or when the spinner can't start, work runs synchronously and writes
// progress to stderr directly. Returns the error from work.
func WithSpinner(prefix string, work func(progress func(string)) error) error {
	if !isStderrTTY() {
		return work(func(msg string) {
			fmt.Fprintf(os.Stderr, "%s%s\n", prefix, msg)
		})
	}

	m := newModel(prefix)
	p := tea.NewProgram(m)

	errCh := make(chan error, 1)
	go func() {
		errCh <- work(func(msg string) {
			p.Send(updateMsg{msg: msg})
		})
	}()

	finalModel, err := p.Run()
	if err != nil {
		<-errCh
		return err
	}
	workErr := <-errCh
	if fm, ok := finalModel.(*model); ok {
		_ = fm
	}
	return workErr
}

type updateMsg struct{ msg string }

type quitMsg struct{ err error }

type model struct {
	prefix  string
	spinner spinner.Model
	msg     string
	done    bool
	err     error
}

func newModel(prefix string) *model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	return &model{prefix: prefix, spinner: s}
}

func (m *model) Init() tea.Cmd { return m.spinner.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		if v.Type == tea.KeyCtrlC {
			m.done = true
			m.err = fmt.Errorf("interrupted")
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(v)
		return m, cmd
	case updateMsg:
		m.msg = v.msg
		return m, nil
	case quitMsg:
		m.err = v.err
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) View() string {
	if m.done {
		return ""
	}
	return fmt.Sprintf("%s%s %s", m.prefix, m.spinner.View(), m.msg)
}

func isStderrTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// PlainStderr writes a progress line directly to stderr. Used as a fallback
// when WithSpinner chooses the non-TTY path.
func PlainStderr(prefix, msg string) {
	fmt.Fprintf(os.Stderr, "%s%s\n", prefix, msg)
}

var _ = io.Discard
var _ = time.Second