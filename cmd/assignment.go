// Package assignment (or "dropbox" — D2L's internal term and the URL prefix)
// exposes the per-course assignment / dropbox surface.
//
// The plan (.kilo/plans/cli-feature-parity-redesign.md §5.5) lists six
// subcommands:
//
//   assignment list --course <id>                                       list folders
//   assignment get <folderId> --course <id>                             one folder
//   assignment submissions <folderId> --course <id>                     my submissions
//   assignment feedback <folderId> [user|group|me [entityId]] --course <id>   feedback
//   assignment submission <folderId> <submissionId> download <fileId> --course <id>
//   assignment feedback <folderId> <entityId> download <fileId> --course <id>
//
// `dropbox` is a thin alias root with the same subcommands.
package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

// apiError is a local copy of dupdata.apiError so we can return
// non-200 errors with the response body attached. The dupdata version
// is unexported; this duplicate keeps the call sites symmetric.
func apiError(res *http.Response, rawURL string) error {
	body, _ := httpclient.BodyBytes(res)
	msg := string(body)
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Errorf("D2L API returned %d %s for %s: %s", res.StatusCode, res.Status, rawURL, msg)
}

var (
	asgCourse  string
	asgEnvFile string
	asgAutoRef bool
	asgJSON    bool
	asgOut     string
	asgName    string
)

// AssignmentFolder matches a single dropbox folder.
type AssignmentFolder struct {
	Id              int    `json:"Id"`
	Name            string `json:"Name"`
	Description     string `json:"Description"`
	IsHidden        bool   `json:"IsHidden"`
	IsLocked        bool   `json:"IsLocked"`
	DueDate         string `json:"DueDate"`
	StartDate       string `json:"StartDate"`
	EndDate         string `json:"EndDate"`
	DisplayGradeType string `json:"DisplayGradeType"`
	CategoryId      int    `json:"CategoryId"`
}

// Submission matches one of the caller's submissions.
type Submission struct {
	Id            int    `json:"Id"`
	FolderId      int    `json:"FolderId"`
	EntityType    string `json:"EntityType"`
	EntityId      int    `json:"EntityId"`
	SubmittedDate string `json:"SubmittedDate"`
	SubmittedText string `json:"SubmittedText"`
}

// FeedbackText is D2L's {Text, Html} rich-text composite, which the API
// uses inline for comment strings.
type FeedbackText struct {
	Text string `json:"Text"`
	Html string `json:"Html"`
}

// FeedbackCriteriaOutcome is one rubric criterion's grade for the entity.
type FeedbackCriteriaOutcome struct {
	CriterionId int          `json:"CriterionId"`
	LevelId     int          `json:"LevelId"`
	Score       json.Number  `json:"Score"`
	Feedback    FeedbackText `json:"Feedback"`
}

// FeedbackRubricAssessment is one rubric's overall assessment for the entity.
type FeedbackRubricAssessment struct {
	RubricId        int                       `json:"RubricId"`
	OverallScore    json.Number               `json:"OverallScore"`
	OverallFeedback FeedbackText              `json:"OverallFeedback"`
	CriteriaOutcome []FeedbackCriteriaOutcome `json:"CriteriaOutcome"`
}

// FeedbackFile matches an attachment D2L attached to the feedback entry.
type FeedbackFile struct {
	FileId   int    `json:"FileId"`
	FileName string `json:"FileName"`
	Size     int64  `json:"Size"`
}

// FeedbackLink matches a media/link attachment on the feedback entry.
type FeedbackLink struct {
	Type     string `json:"Type"`
	LinkId   int    `json:"LinkId"`
	LinkName string `json:"LinkName"`
	Href     string `json:"Href"`
}

// Feedback matches the DropboxFeedbackOut envelope returned for
// /feedback/<type>/<id>.
type Feedback struct {
	Score             json.Number                `json:"Score"`
	GradedSymbol      string                     `json:"GradedSymbol"`
	IsGraded          bool                       `json:"IsGraded"`
	Feedback          FeedbackText               `json:"Feedback"`
	RubricAssessments []FeedbackRubricAssessment `json:"RubricAssessments"`
	Files             []FeedbackFile             `json:"Files"`
	Links             []FeedbackLink             `json:"Links"`
}

func assignmentCmdTree() (*cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command) {
	root := &cobra.Command{
		Use:   "assignment",
		Short: "List assignment folders, view my submissions, and download files (alias: dropbox)",
		Long: "Browse a course's assignment folders, view your submissions, and\n" +
			"download submission / feedback files.\n\n" +
			"`dropbox` is an alias for `assignment` (D2L's internal term and the\n" +
			"`/dropbox/` URL prefix). Both names are interchangeable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List assignment folders in a course",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentList() },
	}
	get := &cobra.Command{
		Use:   "get <folderId>",
		Short: "Show one assignment folder's metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentGet(args[0]) },
	}
	subs := &cobra.Command{
		Use:   "submissions <folderId>",
		Short: "List my submissions for one assignment folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissions(args[0]) },
	}
	fb := &cobra.Command{
		Use:   "feedback <folderId> [user|group|me [entityId]]",
		Short: "Show feedback on a submission (default: yours)",
		Long: "Fetch the feedback entry for one entity in a dropbox folder.\n\n" +
			"With no extra arguments this shows your own feedback; `me` as the\n" +
			"second argument resolves the same way. Pass `user <entityId>` or\n" +
			"`group <entityId>` to view another entity's entry.",
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentFeedback(args) },
	}
	subDl := &cobra.Command{
		Use:   "submission <folderId> <submissionId> download <fileId>",
		Short: "Download a file from one of my submissions",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissionDownload(args) },
	}
	fbDl := &cobra.Command{
		Use:   "feedback <folderId> [entityId|me] download <fileId>",
		Short: "Download a feedback attachment (default: your own)",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentFeedbackDownload(args) },
	}

	addAsgFlags := func(cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.Flags().StringVar(&asgCourse, "course", "", "Course OrgUnitId (required)")
			c.Flags().StringVarP(&asgEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
			c.Flags().BoolVar(&asgAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
			c.Flags().BoolVar(&asgJSON, "json", false, "Emit raw JSON instead of a table")
		}
	}
	addAsgFlags(list, get, subs, fb, subDl, fbDl)
	subDl.Flags().StringVar(&asgOut, "out", defaultHome(), "Directory to write the downloaded file into (default: $HOME)")
	subDl.Flags().StringVar(&asgName, "name", "", "Override the output filename")
	fbDl.Flags().StringVar(&asgOut, "out", defaultHome(), "Directory to write the downloaded file into (default: $HOME)")
	fbDl.Flags().StringVar(&asgName, "name", "", "Override the output filename")

	root.AddCommand(list, get, subs, fb, subDl, fbDl)
	return root, list, get, subs, fb, subDl, fbDl
}

func init() {
	root, _, _, _, _, _, _ := assignmentCmdTree()
	rootCmd.AddCommand(root)
	// dropbox alias — separate command tree that re-uses the same shared
	// run* functions. Flag state is shared via the package-level asg* vars.
	dropbox := &cobra.Command{
		Use:   "dropbox",
		Short: "Alias for `assignment` (D2L's internal term for the /dropbox/ URL prefix)",
		Long: "Alias for the `assignment` command. Both names are interchangeable;\n" +
			"`dropbox` matches D2L's internal URL prefix and the existing internal\n" +
			"package name; `assignment` is what a student calls it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	list := &cobra.Command{
		Use: "list", Short: "List assignment folders in a course", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentList() },
	}
	get := &cobra.Command{
		Use: "get <folderId>", Short: "Show one assignment folder's metadata", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentGet(args[0]) },
	}
	subs := &cobra.Command{
		Use: "submissions <folderId>", Short: "List my submissions for one assignment folder", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissions(args[0]) },
	}
	fb := &cobra.Command{
		Use: "feedback <folderId> [user [me]]", Short: "Show feedback on a submission (default: yours)", Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentFeedback(args) },
	}
	subDl := &cobra.Command{
		Use: "submission <folderId> <submissionId> download <fileId>", Short: "Download a file from one of my submissions", Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissionDownload(args) },
	}
	fbDl := &cobra.Command{
		Use: "feedback <folderId> [entityId|me] download <fileId>", Short: "Download a feedback attachment (default: your own)", Args: cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentFeedbackDownload(args) },
	}
	addAsgFlags := func(cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.Flags().StringVar(&asgCourse, "course", "", "Course OrgUnitId (required)")
			c.Flags().StringVarP(&asgEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
			c.Flags().BoolVar(&asgAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
			c.Flags().BoolVar(&asgJSON, "json", false, "Emit raw JSON instead of a table")
		}
	}
	addAsgFlags(list, get, subs, fb, subDl, fbDl)
	subDl.Flags().StringVar(&asgOut, "out", defaultHome(), "Directory to write the downloaded file into (default: $HOME)")
	subDl.Flags().StringVar(&asgName, "name", "", "Override the output filename")
	fbDl.Flags().StringVar(&asgOut, "out", defaultHome(), "Directory to write the downloaded file into (default: $HOME)")
	fbDl.Flags().StringVar(&asgName, "name", "", "Override the output filename")
	dropbox.AddCommand(list, get, subs, fb, subDl, fbDl)
	rootCmd.AddCommand(dropbox)
}

func requireAsgCourse() (string, error) {
	if asgCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return asgCourse, nil
}

func asgSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     asgEnvFile,
		AutoRefresh: asgAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func runAssignmentList() error {
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/?onlyCurrentStudentsAndGroups=true",
		ua.D2LBase, dupdata.D2LVersion(), courseID)
	// The dropbox folders endpoint returns a raw array, not {Items: [...]}.
	var items []AssignmentFolder
	if err := dupdata.FetchJSON(raw, ens.Jar, &items); err != nil {
		return err
	}
	if asgJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No assignment folders found for this course.")
		return nil
	}
	fmt.Printf("%d assignment folder%s\n", len(items), plural(len(items)))
	for _, f := range items {
		due := "—"
		if f.DueDate != "" {
			due = f.DueDate
			if len(due) > 16 {
				due = due[:16]
			}
		}
		fmt.Printf("  %d\t%s\t%s\n", f.Id, due, f.Name)
	}
	return nil
}

func runAssignmentGet(folderID string) error {
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, folderID)
	var out AssignmentFolder
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func runAssignmentSubmissions(folderID string) error {
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/%s/submissions/mysubmissions/",
		ua.D2LBase, dupdata.D2LVersion(), courseID, folderID)
	res, err := httpclient.FollowRedirects(raw, ens.Jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return err
	}
	if res.StatusCode == 403 {
		// 403 is a permission wall (for example the folder's availability
		// window has closed), not proof that no submission exists.
		_ = res.Body.Close()
		return fmt.Errorf("D2L refused access to this folder's submissions (403 Not Authorized); the folder may be past its availability window")
	}
	if res.StatusCode != 200 {
		return apiError(res, raw)
	}
	// The submissions endpoint returns a raw array, not {Items: [...]}.
	var items []Submission
	if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
		_ = res.Body.Close()
		return err
	}
	_ = res.Body.Close()
	if asgJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No submissions found.")
		return nil
	}
	fmt.Printf("%d submission%s\n", len(items), plural(len(items)))
	for _, s := range items {
		when := s.SubmittedDate
		if len(when) > 16 {
			when = when[:16]
		}
		fmt.Printf("  %d\t%s\t%s\n", s.Id, when, truncate(s.SubmittedText, 80))
	}
	return nil
}

func splitFeedbackEntity(args []string) (entityType, entityID string) {
	entityType = "user"
	entityID = "me"
	if len(args) >= 2 {
		if strings.EqualFold(args[1], "me") {
			entityID = "me"
		} else {
			entityType = args[1]
		}
	}
	if len(args) == 3 {
		entityID = args[2]
	}
	return entityType, entityID
}

func runAssignmentFeedback(args []string) error {
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	folderID := args[0]
	entityType, entityID := splitFeedbackEntity(args)
	// The server does not accept the literal `me` — it requires a numeric
	// user id. Resolve `me` to the caller's own id via /users/whoami.
	if entityID == "me" {
		uid, err := dupdata.CurrentUserID(ens.Jar)
		if err != nil {
			return err
		}
		entityID = uid
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/%s/feedback/%s/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, folderID, entityType, entityID)
	res, err := httpclient.FollowRedirects(raw, ens.Jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return err
	}
	if res.StatusCode == 403 {
		// 403 is a permission wall (for example the folder's availability
		// window has closed), not proof that no feedback exists.
		_ = res.Body.Close()
		return fmt.Errorf("D2L refused access to this folder's feedback (403 Not Authorized); the folder may be past its availability window")
	}
	if res.StatusCode == 404 {
		// 404 on this route means no feedback record exists for the entity.
		fmt.Println("No feedback yet for this entity (nothing has been graded for it).")
		_ = res.Body.Close()
		return nil
	}
	if res.StatusCode != 200 {
		return apiError(res, raw)
	}
	var out Feedback
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		_ = res.Body.Close()
		return err
	}
	_ = res.Body.Close()
	if asgJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Print(formatFeedback(out))
	return nil
}

func formatFeedback(out Feedback) string {
	var b strings.Builder
	b.WriteString("Feedback:\n")
	comment := strings.TrimSpace(out.Feedback.Text)
	if comment == "" {
		comment = strings.TrimSpace(out.Feedback.Html)
	}
	if out.Score != "" || out.GradedSymbol != "" {
		if out.Score != "" {
			fmt.Fprintf(&b, "  Score: %s\n", trimJSONNumber(out.Score))
		}
		if out.GradedSymbol != "" {
			fmt.Fprintf(&b, "  Grade symbol: %s\n", out.GradedSymbol)
		}
	}
	if out.IsGraded {
		b.WriteString("  Status: graded\n")
	} else {
		b.WriteString("  Status: draft (not yet released)\n")
	}
	if comment != "" {
		fmt.Fprintf(&b, "  Comment: %s\n", comment)
	}
	for _, r := range out.RubricAssessments {
		fmt.Fprintf(&b, "  Rubric %d overall: %s\n", r.RubricId, trimJSONNumber(r.OverallScore))
		for _, c := range r.CriteriaOutcome {
			line := fmt.Sprintf("    criterion %d: %s", c.CriterionId, trimJSONNumber(c.Score))
			note := strings.TrimSpace(c.Feedback.Text)
			if note == "" {
				note = strings.TrimSpace(c.Feedback.Html)
			}
			if note != "" {
				line += " — " + note
			}
			fmt.Fprintln(&b, line)
		}
	}
	fmt.Fprintf(&b, "  Attachments: %d file(s), %d link(s)\n", len(out.Files), len(out.Links))
	return b.String()
}

func trimJSONNumber(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func runAssignmentSubmissionDownload(args []string) error {
	if args[2] != "download" {
		return fmt.Errorf("expected 'download' as the 3rd positional, got %q", args[2])
	}
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/%s/submissions/%s/files/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, args[0], args[1], args[3])
	return asgDownloadFile(ens.Jar, raw, fmt.Sprintf("submission-%s-%s", args[1], args[3]))
}

func runAssignmentFeedbackDownload(args []string) error {
	folderID := args[0]
	entityID := "me"
	if len(args) == 4 {
		entityID = args[1]
		if args[2] != "download" {
			return fmt.Errorf("expected 'download' as the 3rd positional, got %q", args[2])
		}
	} else if args[1] != "download" {
		return fmt.Errorf("expected 'download' as the 2nd positional, got %q", args[1])
	}
	fileID := args[len(args)-1]
	courseID, err := requireAsgCourse()
	if err != nil {
		return err
	}
	ens, err := asgSession()
	if err != nil {
		return err
	}
	// Resolve `me` to the caller's numeric user id; the D2L server does
	// not accept the literal `me` on this route.
	if entityID == "me" {
		uid, err := dupdata.CurrentUserID(ens.Jar)
		if err != nil {
			return err
		}
		entityID = uid
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/folders/%s/feedback/user/%s/attachments/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, folderID, entityID, fileID)
	return asgDownloadFile(ens.Jar, raw, fmt.Sprintf("feedback-%s-%s", folderID, fileID))
}

func asgDownloadFile(jar *cookiejar.Jar, rawURL, defaultName string) error {
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	if res.StatusCode/100 != 2 {
		_ = res.Body.Close()
		return fmt.Errorf("download returned %d %s", res.StatusCode, res.Status)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return err
	}
	outDir, err := filepath.Abs(asgOut)
	if err != nil {
		return fmt.Errorf("resolve out dir: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	name := asgName
	if name == "" {
		name = filenameFromContentDisposition(res.Header.Get("Content-Disposition"))
		if name == "" {
			if u, perr := url.Parse(rawURL); perr == nil {
				name = filepath.Base(u.Path)
			}
		}
		if name == "" || name == "/" || name == "." {
			name = defaultName
		}
	}
	name = sanitizeFilename(name)
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("Downloaded: %s (%s)\n", path, humanBytes(int64(len(body))))
	return nil
}
