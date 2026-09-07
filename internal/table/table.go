package table

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"golang.org/x/term"
)

type ColumnSpec struct {
	Fixed  int
	IsFlex bool
	Flex   int
}

func Fixed(n int) ColumnSpec { return ColumnSpec{Fixed: n} }
func Flex(min int) ColumnSpec { return ColumnSpec{Flex: min, IsFlex: true} }

// Widths computes column widths matching the old cli-table3 semantics:
// fixed columns reserve content width, flex columns fill the remaining
// space never smaller than their minimum. cli-table3 reserves 2 chars
// of padding per cell + 1 char per vertical border, so we add `pad` on
// top of content widths.
func Widths(cols []ColumnSpec, totalWidth int) []int {
	const pad = 2
	fixedContentTotal := 0
	flexMinTotal := 0
	flexCount := 0
	for _, c := range cols {
		if c.IsFlex {
			flexMinTotal += c.Flex
			flexCount++
		} else {
			fixedContentTotal += c.Fixed
		}
	}
	borderOverhead := len(cols) + 1
	paddingOverhead := len(cols) * pad
	flexContentSpace := totalWidth - borderOverhead - paddingOverhead - fixedContentTotal

	out := make([]int, len(cols))
	for i, c := range cols {
		if !c.IsFlex {
			out[i] = c.Fixed + pad
			continue
		}
		share := flexContentSpace
		if flexCount > 1 {
			share = (flexContentSpace * c.Flex) / flexMinTotal
		}
		w := c.Flex
		if share > w {
			w = share
		}
		out[i] = w + pad
	}
	return out
}

// TerminalWidth returns the actual terminal width in cells, clamped to
// [60, 200].
func TerminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 60 {
		return 100
	}
	if w > 200 {
		return 200
	}
	return w
}

func shouldColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// Options configures Render. Widths come from Widths(...).
type Options struct {
	Headers []string
	Rows    [][]string
	Widths  []int
	Style   string // "dark" (default for TTY), "light", "notty" (default for pipes)
}

// Render produces a styled table using lipgloss/table directly. This avoids
// the markdown-table issues with leading-whitespace tree connectors and gives
// us per-column widths.
func Render(opts Options) string {
	if len(opts.Headers) == 0 && len(opts.Rows) == 0 {
		return ""
	}

	color := shouldColor()
	styleName := opts.Style
	if styleName == "" {
		if color {
			styleName = "dark"
		} else {
			styleName = "notty"
		}
	}

	border := lipgloss.NormalBorder()
	headerStyle := lipgloss.NewStyle().Bold(true)
	borderStyle := lipgloss.NewStyle()
	switch styleName {
	case "light":
		headerStyle = headerStyle.Foreground(lipgloss.Color("27")) // blue
		borderStyle = borderStyle.Foreground(lipgloss.Color("250"))
	case "dark":
		headerStyle = headerStyle.Foreground(lipgloss.Color("39")) // cyan
		borderStyle = borderStyle.Foreground(lipgloss.Color("245"))
	default: // notty / ascii
		headerStyle = headerStyle.Foreground(lipgloss.Color(""))
		borderStyle = borderStyle.Foreground(lipgloss.Color(""))
	}

	t := table.New().
		Border(border).
		BorderStyle(borderStyle).
		Headers(opts.Headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})

	for _, row := range opts.Rows {
		t.Row(row...)
	}

	if len(opts.Widths) == 0 {
		opts.Widths = make([]int, len(opts.Headers))
		for i := range opts.Widths {
			opts.Widths[i] = 20
		}
	}
	if total := sumWidths(opts.Widths); total > 0 {
		t.Width(total)
	}
	return t.Render() + "\n"
}

func sumWidths(w []int) int {
	s := 0
	for _, v := range w {
		s += v
	}
	return s
}

// Truncate is kept for API compatibility.
func Truncate(s string, width int) string {
	if width <= 1 {
		if len(s) > 0 {
			return "…"
		}
		return ""
	}
	if len(s) <= width {
		return s
	}
	return s[:width-1] + "…"
}