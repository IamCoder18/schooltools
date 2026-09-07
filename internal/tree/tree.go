// Package tree renders D2L course table-of-contents data as a hierarchical
// tree using github.com/xlab/treeprint. It is the multi-line alternative to
// the tabular output produced by cmd/content.
package tree

import (
	"strings"

	"github.com/xlab/treeprint"

	"github.com/aarav/schooltools/internal/content"
)

// Options controls what is rendered alongside each node label.
type Options struct {
	// ShowKind prefixes every node with its kind marker ("▣ Module" / "▢ Topic").
	ShowKind bool
	// ShowType appends the topic type ("File", "Link", "Dropbox", ...).
	// Ignored for modules.
	ShowType bool
	// ShowDate appends the last-modified timestamp (first 16 chars of the
	// ISO string, matching cmd/content.formatContentDate).
	ShowDate bool
	// ShowFlags appends a bracketed list of [hidden] [locked] [broken].
	ShowFlags bool
}

// RenderTOC walks modules → topics → nested modules (matching
// content.FlattenToc ordering) and returns a single multi-line tree string.
func RenderTOC(modules []content.TocModule, opts Options) string {
	root := treeprint.New()
	buildInto(root, modules, opts)
	if len(modules) == 0 {
		return ""
	}
	return root.String()
}

// Line is one rendered tree row, split into the tree-connector prefix and
// the bare title. Depth is the number of 4-char indent groups that precede
// the connector (0 for top-level children of the synthetic root).
type Line struct {
	Prefix string
	Title  string
	Depth  int
}

// Lines builds a treeprint tree whose values are the bare titles of each
// module and topic, renders it, and parses the output back into per-line
// prefix/title pairs.
//
// The returned lines are in depth-first order and match the order produced
// by content.FlattenToc, so callers can zip by index to attach the prefix
// onto their own rows without any string-based title matching.
func Lines(modules []content.TocModule) []Line {
	if len(modules) == 0 {
		return nil
	}
	root := treeprint.New()
	buildLinesInto(root, modules)
	return parseLines(root.String())
}

func buildLinesInto(parent treeprint.Tree, modules []content.TocModule) {
	for _, m := range modules {
		branch := parent.AddBranch(m.Title)
		for _, t := range m.Topics {
			branch.AddNode(t.Title)
		}
		if len(m.Modules) > 0 {
			buildLinesInto(branch, m.Modules)
		}
	}
}

// treeprint renders each line as a sequence of 4-char groups:
//   - "│   " (link) or "    " (no-link) for each ancestor level
//   - "├── " (mid) or "└── " (end) for the connector to this row
// followed by the value. We walk 4-rune chunks (NOT bytes — the box-drawing
// characters are 3 bytes each in UTF-8) until we hit a connector group;
// everything before is the prefix, everything after is the title.
func parseLines(s string) []Line {
	var out []Line
	for _, raw := range strings.Split(s, "\n") {
		if raw == "" || raw == "." {
			continue
		}
		runes := []rune(raw)
		offset, depth := 0, 0
		for offset+4 <= len(runes) {
			switch string(runes[offset : offset+4]) {
			case "├── ", "└── ":
				offset += 4
				goto found
			case "│   ", "    ":
				offset += 4
				depth++
				continue
			default:
				goto found
			}
		}
	found:
		out = append(out, Line{
			Prefix: string(runes[:offset]),
			Title:  string(runes[offset:]),
			Depth:  depth,
		})
	}
	return out
}

func buildInto(parent treeprint.Tree, modules []content.TocModule, opts Options) {
	for _, m := range modules {
		moduleBranch := parent.AddBranch(labelForModule(m, opts))

		for _, t := range m.Topics {
			moduleBranch.AddNode(labelForTopic(t, opts))
		}

		if len(m.Modules) > 0 {
			buildInto(moduleBranch, m.Modules, opts)
		}
	}
}

func labelForModule(m content.TocModule, opts Options) string {
	parts := make([]string, 0, 3)
	if opts.ShowKind {
		parts = append(parts, "▣ Module")
	}
	parts = append(parts, m.Title)
	if opts.ShowDate && m.LastModifiedDate != "" {
		parts = append(parts, "["+shortDate(m.LastModifiedDate)+"]")
	}
	if opts.ShowFlags {
		parts = append(parts, flagTags(m.IsHidden, m.IsLocked, false))
	}
	return strings.Join(parts, " ")
}

func labelForTopic(t content.TocTopic, opts Options) string {
	parts := make([]string, 0, 5)
	if opts.ShowKind {
		parts = append(parts, "▢ Topic")
	}
	parts = append(parts, t.Title)
	if opts.ShowType {
		parts = append(parts, "["+topicType(t)+"]")
	}
	if opts.ShowDate && t.LastModifiedDate != "" {
		parts = append(parts, "["+shortDate(t.LastModifiedDate)+"]")
	}
	if opts.ShowFlags {
		parts = append(parts, flagTags(t.IsHidden, t.IsLocked, t.IsBroken))
	}
	return strings.Join(parts, " ")
}

func topicType(t content.TocTopic) string {
	if t.TypeIdentifier != "" {
		return t.TypeIdentifier
	}
	if t.TopicType == 2 {
		return "Link"
	}
	return "File"
}

func shortDate(iso string) string {
	if len(iso) >= 16 {
		return iso[:16]
	}
	return iso
}

func flagTags(hidden, locked, broken bool) string {
	var tags []string
	if hidden {
		tags = append(tags, "hidden")
	}
	if locked {
		tags = append(tags, "locked")
	}
	if broken {
		tags = append(tags, "broken")
	}
	if len(tags) == 0 {
		return ""
	}
	return "[" + strings.Join(tags, ",") + "]"
}
