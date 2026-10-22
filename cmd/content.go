package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/archive"
	"github.com/aarav/schooltools/internal/content"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/render"
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
	contentTimeout     time.Duration
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
	contentCmd.Flags().DurationVar(&contentTimeout, "timeout", 60*time.Second, "Abort the live TOC fetch after this duration; --archive is unaffected")

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

	toc, err := fetchTocWithTimeout(ens.Jar, courseID, contentTimeout)
	if err != nil {
		if ua.IsLoginURL(err.Error()) || isLoginError(err) {
			_ = session.Clear()
			return fmt.Errorf("session expired (landed on login page); run 'schooltools login' again")
		}
		if isTimeoutErr(err) {
			fmt.Fprint(os.Stderr, render.Note("TOC fetch did not complete in %s; re-run with `schooltools content --course %s --archive` to read from the on-disk archive.", contentTimeout, courseID))
		}
		return err
	}
	flatRows := content.FlattenToc(toc.Modules)

	if contentDepth < 0 {
		return fmt.Errorf("--depth must be >= 0 (got %d)", contentDepth)
	}
	if contentDepth > 0 {
		flatRows = depthFilter(flatRows, contentDepth)
	}
	if contentJSON && contentTree {
		return fmt.Errorf("--tree and --json are mutually exclusive")
	}
	if contentJSON {
		list := render.NewList(flatRows)
		return render.PrintJSON(map[string]any{
			"courseId": courseID,
			"count":    list.Count,
			"items":    list.Items,
		})
	}

	treeRows := applyTreePrefixes(flatRows, toc.Modules)

	if contentTree {
		printTree(courseID, toc.Modules, treeRows, contentDepth)
		return nil
	}
	printHuman(courseID, treeRows)
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
	t, doc, err := fetchTopicDoc(ens.Jar, courseID, topicID)
	if err != nil {
		if isLoginError(err) {
			_ = session.Clear()
			return fmt.Errorf("session expired; run 'schooltools login' again")
		}
		return fmt.Errorf("fetch topic metadata: %w", err)
	}
	if contentJSON {
		return render.PrintJSON(doc)
	}
	fmt.Print(topicReport(courseID, t, doc))
	return nil
}

func fetchTopicDoc(jar *cookiejar.Jar, courseID string, topicID int) (content.Topic, map[string]any, error) {
	res, err := httpclient.FollowRedirects(content.TopicURL(courseID, topicID), jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return content.Topic{}, nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return content.Topic{}, nil, fmt.Errorf("D2L API returned %d %s for %s", res.StatusCode, res.Status, res.Request.URL)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return content.Topic{}, nil, err
	}
	var t content.Topic
	if err := json.Unmarshal(body, &t); err != nil {
		return content.Topic{}, nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return content.Topic{}, nil, err
	}
	return t, doc, nil
}

func topicReport(courseID string, t content.Topic, doc map[string]any) string {
	var b strings.Builder
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = fmt.Sprintf("Topic %d", t.TopicId)
	}
	b.WriteString(render.Title(title))

	rows := []render.Row{
		{Key: "ID", Value: strconv.Itoa(t.TopicId)},
		{Key: "Type", Value: topicTypeLabel(t)},
		{Key: "Start", Value: render.DateTime(docString(doc, "StartDate"))},
		{Key: "End", Value: render.DateTime(docString(doc, "EndDate"))},
		{Key: "Updated", Value: relOrDash(t.LastModifiedDate)},
		{Key: "URL", Value: topicURLValue(t)},
		{Key: "Hidden", Value: render.StatusWord(yesNo(t.IsHidden))},
		{Key: "Locked", Value: render.StatusWord(yesNo(t.IsLocked))},
		{Key: "Broken", Value: render.StatusWord(yesNo(t.IsBroken))},
	}
	if t.FileName != "" {
		rows = append(rows, render.Row{Key: "File", Value: t.FileName})
	}
	b.WriteString(render.KV(rows...))

	b.WriteString(render.Section("Next"))
	switch {
	case t.IsFile():
		b.WriteString(render.Hint("schooltools download --course %s %d", courseID, t.TopicId))
	case t.IsLink():
		if u := topicURLValue(t); u != "—" {
			b.WriteString(render.Hint("%s", u))
		} else {
			b.WriteString(render.Empty("No URL recorded for this link."))
		}
	default:
		b.WriteString(render.Empty("Nothing to download for this topic type."))
	}
	return b.String()
}

func topicURLValue(t content.Topic) string {
	if t.Url == nil || *t.Url == "" {
		return "—"
	}
	return *t.Url
}

func docString(doc map[string]any, key string) string {
	if doc == nil {
		return ""
	}
	s, _ := doc[key].(string)
	return s
}

func relOrDash(iso string) string {
	if r := render.Rel(iso); r != "" {
		return r
	}
	return "—"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
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

// fetchTocWithTimeout races the live TOC fetch against a deadline. On
// timeout the deadline propagates into the HTTP request via the
// context, so the in-flight call returns promptly with the timeout
// error instead of leaking the goroutine until the server replies.
// The archive path is unaffected — users get sub-second responses
// from `content --archive`.
func fetchTocWithTimeout(jar *cookiejar.Jar, courseID string, d time.Duration) (content.TocResponse, error) {
	if d <= 0 {
		return content.FetchToc(courseID, jar)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return content.FetchTocWithContext(ctx, courseID, jar)
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out")
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

func printTree(courseID string, modules []content.TocModule, rows []content.FlatRow, depth int) {
	if len(modules) == 0 {
		fmt.Print(render.Title(fmt.Sprintf("Course content · %s", courseID)))
		fmt.Print(render.Empty("No content found for this course."))
		return
	}
	modulesN, topicsN := countKinds(rows)
	fmt.Print(render.Title(fmt.Sprintf("Course content · %s", courseID)))

	pruned := modules
	if depth > 0 {
		pruned = pruneModulesByDepth(modules, depth)
	}
	out := tree.RenderTOC(pruned, tree.Options{
		ShowKind:  true,
		ShowType:  true,
		ShowDate:  true,
		ShowFlags: true,
		MaxDepth:  depth,
		TopicLabelHook: func(t content.TocTopic) string {
			if t.TypeIdentifier != "File" && topicTypeNumber(t) != 1 {
				return ""
			}
			if t.Url == nil {
				return ""
			}
			u := *t.Url
			parsed, err := url.Parse(u)
			if err != nil {
				return ""
			}
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(parsed.Path)), ".")
			if ext == "" || strings.ContainsAny(ext, "/\\") || len(ext) > 8 {
				return ""
			}
			return ext
		},
	})
	fmt.Print(out)
	if depth > 0 {
		fmt.Print(render.Note("limited to depth %d", depth))
	}
	fmt.Print(tocCountSummary(modulesN, topicsN))
}

func countKinds(rows []content.FlatRow) (modules, topics int) {
	for _, r := range rows {
		if r.Kind == "MODULE" {
			modules++
		} else {
			topics++
		}
	}
	return modules, topics
}

func tocCountSummary(modules, topics int) string {
	return render.KV(
		render.Row{Key: "Modules", Value: render.Count(modules, "module")},
		render.Row{Key: "Topics", Value: render.Count(topics, "topic")},
	)
}

// pruneModulesByDepth returns modules trimmed so no descendant is below
// `max`. Modules whose own depth equals max still render (they appear in
// the table as headers) but their child topics are dropped.
func pruneModulesByDepth(modules []content.TocModule, max int) []content.TocModule {
	if max <= 0 {
		return modules
	}
	var walk func(m content.TocModule, depth int) content.TocModule
	walk = func(m content.TocModule, depth int) content.TocModule {
		if depth >= max {
			return content.TocModule{ModuleId: m.ModuleId, Title: m.Title, LastModifiedDate: m.LastModifiedDate, IsHidden: m.IsHidden, IsLocked: m.IsLocked}
		}
		out := m
		out.Modules = nil
		for _, c := range m.Modules {
			if cm := walk(c, depth+1); cm.ModuleId != 0 || cm.Title != "" {
				out.Modules = append(out.Modules, cm)
			}
		}
		return out
	}
	out := make([]content.TocModule, 0, len(modules))
	for _, m := range modules {
		if cm := walk(m, 1); cm.ModuleId != 0 || cm.Title != "" {
			out = append(out, cm)
		}
	}
	return out
}

// topicTypeNumber mirrors the TopicType discriminator used by the TOC.
// 2 = Link, 1 = File (default when the type identifier is missing).
func topicTypeNumber(t content.TocTopic) int { return t.TopicType }

func printHuman(courseID string, rows []content.FlatRow) {
	fmt.Print(render.Title(fmt.Sprintf("Course content · %s", courseID)))
	if len(rows) == 0 {
		fmt.Print(render.Empty("No content found for this course."))
		return
	}
	modules, topics := countKinds(rows)
	widths := contentTableWidths()
	out := table.Render(table.Options{
		Headers: []string{"ID", "Kind", "Type", "Last Updated", "Flags", "Title"},
		Widths:  widths,
		Rows:    contentTableRows(rows, widths),
	})
	fmt.Print(out)
	fmt.Print(tocCountSummary(modules, topics))
}

func contentTableWidths() []int {
	return table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(10), table.Fixed(12), table.Fixed(22), table.Fixed(22), table.Flex(24)}, table.TerminalWidth())
}

func contentTableRows(rows []content.FlatRow, widths []int) [][]string {
	titleW := 24
	if len(widths) > 5 {
		titleW = widths[5] - 2
	}
	tableRows := make([][]string, len(rows))
	for i, r := range rows {
		tableRows[i] = []string{
			fmt.Sprintf("%d", r.Id),
			kindLabel(r.Kind),
			r.Type,
			formatContentDate(r.LastModified),
			flagsFor(r),
			render.Truncate(r.Title, titleW),
		}
	}
	return tableRows
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
	if r := render.Rel(iso); r != "" {
		return r
	}
	return "—"
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
		Id       int
		Title    string
		URL      string
		Type     string
		Modified string
		UUID     string
		Versions int
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

	if contentJSON {
		items := make([]map[string]any, len(rows))
		for i, r := range rows {
			items[i] = map[string]any{
				"topicId":      r.Id,
				"title":        r.Title,
				"type":         r.Type,
				"url":          r.URL,
				"lastModified": r.Modified,
				"current":      r.UUID,
				"versions":     r.Versions,
				"courseId":     idx.CourseID,
				"courseName":   idx.Name,
				"courseCode":   idx.Code,
			}
		}
		list := render.NewList(items)
		return render.PrintJSON(map[string]any{
			"courseId":   courseID,
			"source":     "archive",
			"count":      list.Count,
			"courseName": idx.Name,
			"courseCode": idx.Code,
			"indexedAt":  idx.UpdatedAt.UTC().Format(time.RFC3339),
			"items":      list.Items,
		})
	}

	fmt.Print(render.Title(fmt.Sprintf("Archived content · %s", courseID)))
	if len(rows) == 0 {
		fmt.Print(render.Empty(fmt.Sprintf("No archived topics for course %s.", courseID)))
		return nil
	}
	widths := contentTableWidths()
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
			render.Truncate(r.Title, widths[5]-2),
		}
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Kind", "Type", "Last Updated", "Flags", "Title"},
		Widths:  widths,
		Rows:    tableRows,
	})
	fmt.Print(out)
	fmt.Print(render.KV(
		render.Row{Key: "Topics", Value: render.Count(len(rows), "topic")},
		render.Row{Key: "Course", Value: archiveCourseLabel(idx, courseID)},
		render.Row{Key: "Indexed", Value: render.When(idx.UpdatedAt.UTC().Format(time.RFC3339))},
	))
	return nil
}

func archiveCourseLabel(idx *archive.CourseIndex, courseID string) string {
	name := strings.TrimSpace(idx.Name)
	code := strings.TrimSpace(idx.Code)
	switch {
	case name != "" && code != "":
		return fmt.Sprintf("%s  ·  %s", name, code)
	case name != "":
		return name
	case code != "":
		return code
	default:
		return courseID
	}
}

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

	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		fmt.Println(string(blob))
		return nil
	}
	var topic content.Topic
	if err := json.Unmarshal(blob, &topic); err != nil {
		topic = content.Topic{TopicId: topicID}
	}
	if contentJSON {
		return render.PrintJSON(doc)
	}
	fmt.Print(topicReport(courseID, topic, doc))
	return nil
}

// suppress unused-warning for filepath import — used by future
// `--archive-subtree` filters we may add; keep it for symmetry with
// archive package usage elsewhere.
var _ = filepath.Clean
