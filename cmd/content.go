package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/archive"
	"github.com/aarav/schooltools/internal/content"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/tree"
	"github.com/aarav/schooltools/internal/ua"
)

var (
	contentEnvFile     string
	contentAutoRefresh bool
	contentJSON        bool
	contentTree        bool
	contentDepth       int
	contentArchive     bool
	contentArchiveDir  string
)

// contentCmd is the new (0.2.0) flag-based form: `content --course <id>`.
// When a positional courseId is given, we print a one-line deprecation
// notice and dispatch — this preserves the legacy 0.1.0 form.
var contentCmd = &cobra.Command{
	Use:   "content",
	Short: "List a course's modules + topics via the table-of-contents API",
	Long: "Browse the table of contents for a single D2L course. The course\n" +
		"is selected by `--course <orgUnitId>` (formerly a positional arg).\n\n" +
		"With no subcommand, the full TOC is printed as a table. `content get\n" +
		"<topicId>` shows a single topic's metadata document.",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Legacy form: `content <courseId>`. If a positional arg was given
		// and --course wasn't, treat the arg as the course id and warn.
		if len(args) > 0 {
			if contentCourse == "" {
				fmt.Fprintln(os.Stderr, "Deprecated: 'schooltools content <id>' is now 'schooltools content --course <id>'.")
				fmt.Fprintln(os.Stderr, "This alias will be removed in a future release.")
				contentCourse = args[0]
			}
		}
		courseID, err := requireContentCourse()
		if err != nil {
			return err
		}
		return runContentList(courseID)
	},
}

var contentGetCmd = &cobra.Command{
	Use:   "get <topicId>",
	Short: "Show a single topic's metadata document",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		courseID, err := requireContentCourse()
		if err != nil {
			return err
		}
		tid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid topicId %q: %w", args[0], err)
		}
		return runContentGet(courseID, tid)
	},
}

func init() {
	contentCmd.PersistentFlags().StringVar(&contentCourse, "course", "", "Course OrgUnitId to scope the command to (required)")
	contentCmd.PersistentFlags().StringVarP(&contentEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	contentCmd.PersistentFlags().BoolVar(&contentAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")

	contentCmd.Flags().BoolVar(&contentJSON, "json", false, "Emit raw JSON instead of a table")
	contentCmd.Flags().BoolVar(&contentTree, "tree", false, "Render as a multi-line tree instead of a table (uses github.com/xlab/treeprint)")
	contentCmd.Flags().IntVar(&contentDepth, "depth", 0, "Limit tree depth (0 = unlimited)")
	contentCmd.Flags().BoolVar(&contentArchive, "archive", false, "Read from the on-disk archive (set by `archive`) instead of the live API")
	contentCmd.Flags().StringVar(&contentArchiveDir, "archive-dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")

	contentGetCmd.Flags().BoolVar(&contentJSON, "json", false, "Emit raw JSON instead of pretty-printed")
	contentGetCmd.Flags().BoolVar(&contentArchive, "archive", false, "Read from the on-disk archive instead of the live API")
	contentGetCmd.Flags().StringVar(&contentArchiveDir, "archive-dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")

	contentCmd.AddCommand(contentGetCmd)
	rootCmd.AddCommand(contentCmd)
}

var contentCourse string

func requireContentCourse() (string, error) {
	if contentCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return contentCourse, nil
}

func runContentList(courseID string) error {
	if contentArchive {
		return runContentListFromArchive(courseID)
	}
	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     contentEnvFile,
		AutoRefresh: contentAutoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}
	toc, err := content.FetchToc(courseID, ens.Jar)
	if err != nil {
		if ua.IsLoginURL(err.Error()) || isLoginError(err) {
			_ = session.Clear()
			return fmt.Errorf("session expired (landed on login page); run 'schooltools login' again")
		}
		return err
	}
	flatRows := content.FlattenToc(toc.Modules)

	if contentDepth > 0 {
		flatRows = depthFilter(flatRows, contentDepth)
	}

	if contentJSON && contentTree {
		return fmt.Errorf("--tree and --json are mutually exclusive")
	}
	if contentJSON {
		out, _ := json.MarshalIndent(flatRows, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	treeRows := applyTreePrefixes(flatRows, toc.Modules)

	if contentTree {
		printTree(toc.Modules, treeRows)
		return nil
	}
	printHuman(treeRows)
	return nil
}

func runContentGet(courseID string, topicID int) error {
	if contentArchive {
		return runContentGetFromArchive(courseID, topicID)
	}
	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     contentEnvFile,
		AutoRefresh: contentAutoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}
	t, err := content.FetchTopic(courseID, topicID, ens.Jar)
	if err != nil {
		if isLoginError(err) {
			_ = session.Clear()
			return fmt.Errorf("session expired; run 'schooltools login' again")
		}
		return fmt.Errorf("fetch topic metadata: %w", err)
	}
	if contentJSON {
		out, _ := json.MarshalIndent(t, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	out, _ := json.MarshalIndent(t, "", "  ")
	fmt.Println(string(out))
	return nil
}

// depthFilter keeps only rows with Depth <= max.
func depthFilter(rows []content.FlatRow, max int) []content.FlatRow {
	out := make([]content.FlatRow, 0, len(rows))
	for _, r := range rows {
		if r.Depth <= max {
			out = append(out, r)
		}
	}
	return out
}

func isLoginError(err error) bool {
	if err == nil {
		return false
	}
	return ua.IsLoginURL(err.Error())
}

// applyTreePrefixes asks internal/tree (which delegates to github.com/xlab/
// treeprint) for the per-line Unicode tree connectors, then zips them onto
// flatRows by index. tree.Lines walks the same DFS order as FlattenToc, so
// index-based zipping is unambiguous even when titles repeat.
func applyTreePrefixes(flatRows []content.FlatRow, modules []content.TocModule) []content.FlatRow {
	lines := tree.Lines(modules)
	out := make([]content.FlatRow, len(flatRows))
	for i, r := range flatRows {
		if i < len(lines) {
			r.Title = lines[i].Prefix + r.Title
			r.Depth = lines[i].Depth
		}
		out[i] = r
	}
	return out
}

func printTree(modules []content.TocModule, rows []content.FlatRow) {
	if len(modules) == 0 {
		fmt.Println("No content found for this course.")
		return
	}
	modulesN, topicsN := 0, 0
	for _, r := range rows {
		if r.Kind == "MODULE" {
			modulesN++
		} else {
			topicsN++
		}
	}
	fmt.Printf("%d items — %d module%s, %d topic%s\n",
		len(rows), modulesN, plural(modulesN), topicsN, plural(topicsN))

	out := tree.RenderTOC(modules, tree.Options{
		ShowKind:  true,
		ShowType:  true,
		ShowDate:  true,
		ShowFlags: true,
	})
	fmt.Print(out)
}

func printHuman(rows []content.FlatRow) {
	if len(rows) == 0 {
		fmt.Println("No content found for this course.")
		return
	}
	modules, topics := 0, 0
	for _, r := range rows {
		if r.Kind == "MODULE" {
			modules++
		} else {
			topics++
		}
	}
	fmt.Printf("%d items — %d module%s, %d topic%s\n",
		len(rows), modules, plural(modules), topics, plural(topics))

	tableRows := make([][]string, len(rows))
	for i, r := range rows {
		flags := flagsFor(r)
		tableRows[i] = []string{
			fmt.Sprintf("%d", r.Id),
			kindLabel(r.Kind),
			r.Type,
			formatContentDate(r.LastModified),
			flags,
			r.Title,
		}
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Kind", "Type", "Last Updated", "Flags", "Title"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(10), table.Fixed(12), table.Fixed(22), table.Fixed(22), table.Flex(24)}, table.TerminalWidth()),
		Rows:    tableRows,
	})
	fmt.Print(out)
}

func kindLabel(k string) string {
	if k == "MODULE" {
		return "▣ Module"
	}
	return "▢ Topic"
}

func flagsFor(r content.FlatRow) string {
	out := []string{}
	if r.IsHidden {
		out = append(out, "hidden")
	}
	if r.IsLocked {
		out = append(out, "locked")
	}
	if r.IsBroken {
		out = append(out, "broken")
	}
	if len(out) == 0 {
		return "—"
	}
	return stringsJoinComma(out)
}

func stringsJoinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func formatContentDate(iso string) string {
	if iso == "" {
		return "—"
	}
	if len(iso) >= 16 {
		return "[" + iso[:16] + "]"
	}
	return "[" + iso + "]"
}

// runContentListFromArchive renders a TOC table from the on-disk
// archive at archive.DefaultRoot() / courses/<courseId> / index.json. The
// shape of the output matches the live `content` command — same
// columns, same flags. Topics with no recorded archive entry simply
// do not appear; the per-course index is the single source of truth.
func runContentListFromArchive(courseID string) error {
	idx, err := archive.LoadCourseIndex(contentArchiveDir, courseID)
	if err != nil {
		return fmt.Errorf("load archived TOC for course %s: %w", courseID, err)
	}
	if idx == nil {
		return fmt.Errorf("no archive at %s yet — run `schooltools archive` to seed it",
			contentArchiveDir)
	}

	type flatRow struct {
		Id        int
		Title     string
		URL       string
		Type      string
		Modified  string
		UUID      string
		Versions  int
	}
	var rows []flatRow
	for id, t := range idx.Topics {
		if t == nil {
			continue
		}
		modified := ""
		if !t.LastModified.IsZero() {
			modified = t.LastModified.UTC().Format("2006-01-02T15:04:05Z")
		}
		rows = append(rows, flatRow{
			Id: id, Title: t.Title, URL: t.URL, Type: t.Type,
			Modified: modified, UUID: t.Current, Versions: len(t.Versions),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })

	// For the table view, mirror `printHuman`: "ID", "Kind", "Type",
	// "Last Updated", "Flags", "Title". Since the archive doesn't store
	// the module/topic tree shape, we render a flat list — kind is
	// always "Topic" here, and Flags shows "[N saved]" when there are
	// multiple versions.
	tableRows := make([][]string, len(rows))
	for i, r := range rows {
		flags := "—"
		if r.Versions > 1 {
			flags = fmt.Sprintf("[%d versions]", r.Versions)
		}
		tableRows[i] = []string{
			fmt.Sprintf("%d", r.Id),
			"Topic",
			r.Type,
			formatContentDate(r.Modified),
			flags,
			r.Title,
		}
	}

	if contentJSON {
		out := make([]map[string]any, len(rows))
		for i, r := range rows {
			out[i] = map[string]any{
				"topicId":     r.Id,
				"title":       r.Title,
				"type":        r.Type,
				"url":         r.URL,
				"lastModified": r.Modified,
				"current":     r.UUID,
				"versions":    r.Versions,
				"courseId":    idx.CourseID,
				"courseName":  idx.Name,
				"courseCode":  idx.Code,
			}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	if len(rows) == 0 {
		fmt.Printf("No archived topics for course %s.\n", courseID)
		return nil
	}
	fmt.Printf("%d archived topic%s in course %s (course=%q, code=%q, index updated %s)\n",
		len(rows), plural(len(rows)), courseID, idx.Name, idx.Code,
		idx.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	out := table.Render(table.Options{
		Headers: []string{"ID", "Kind", "Type", "Last Updated", "Flags", "Title"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(10), table.Fixed(12), table.Fixed(22), table.Fixed(22), table.Flex(24)}, table.TerminalWidth()),
		Rows:    tableRows,
	})
	fmt.Print(out)
	return nil
}

// runContentGetFromArchive reads a single archived topic's metadata JSON
// (the blob content) and prints it the same way as the live `content
// get` does — pretty-printed by default, unindented with --json.
func runContentGetFromArchive(courseID string, topicID int) error {
	idx, err := archive.LoadCourseIndex(contentArchiveDir, courseID)
	if err != nil {
		return fmt.Errorf("load archived course index for %s: %w", courseID, err)
	}
	if idx == nil {
		return fmt.Errorf("no archive at %s yet", contentArchiveDir)
	}
	t, ok := idx.Topics[topicID]
	if !ok || t == nil {
		return fmt.Errorf("topic %d not found in archived course %s", topicID, courseID)
	}
	if t.Current == "" {
		return fmt.Errorf("topic %d has no current blob in the archive", topicID)
	}
	blob, err := archive.LoadBlob(contentArchiveDir, t.Current)
	if err != nil {
		return fmt.Errorf("load blob %s: %w", t.Current, err)
	}

	// Re-decode so we can pretty-print, same behaviour as the live
	// `content get`.
	var parsed map[string]any
	if err := json.Unmarshal(blob, &parsed); err != nil {
		// blob wasn't JSON; print bytes verbatim.
		if contentJSON {
			fmt.Println(string(blob))
		} else {
			fmt.Println(string(blob))
		}
		return nil
	}
	if contentJSON {
		b, _ := json.Marshal(parsed)
		fmt.Println(string(b))
		return nil
	}
	b, _ := json.MarshalIndent(parsed, "", "  ")
	fmt.Println(string(b))
	return nil
}

// suppress unused-warning for filepath import — used by future
// `--archive-subtree` filters we may add; keep it for symmetry with
// archive package usage elsewhere.
var _ = filepath.Clean
