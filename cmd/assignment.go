package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/dupdata"
	"github.com/IamCoder18/schooltools/internal/httpclient"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
	"github.com/IamCoder18/schooltools/internal/table"
	"github.com/IamCoder18/schooltools/internal/ua"
)

func apiError(res *http.Response, rawURL string) error {
	body, _ := httpclient.BodyBytes(res)
	msg := strings.TrimSpace(string(body))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Errorf("D2L API returned %d %s for %s: %s", res.StatusCode, res.Status, rawURL, msg)
}

var (
	asgCourse    string
	asgEnvFile   string
	asgAutoRef   bool
	asgJSON      bool
	asgOut       string
	asgName      string
	asgNoRelated bool
)

type RichText struct {
	Text string `json:"Text"`
	Html string `json:"Html"`
}

type FeedbackText = RichText

func (r RichText) Plain() string {
	if s := strings.TrimSpace(r.Text); s != "" {
		return s
	}
	return strings.TrimSpace(htmlToText(r.Html))
}

type TimeWindow struct {
	StartDate                 string `json:"StartDate"`
	EndDate                   string `json:"EndDate"`
	StartDateAvailabilityType any    `json:"StartDateAvailabilityType"`
	EndDateAvailabilityType   any    `json:"EndDateAvailabilityType"`
}

type RubricCell struct {
	Feedback    RichText `json:"Feedback"`
	Description RichText `json:"Description"`
	Points      *float64 `json:"Points"`
	LevelId     int      `json:"LevelId"`
}

type RubricLevel struct {
	Id     int     `json:"Id"`
	Name   string  `json:"Name"`
	Points float64 `json:"Points"`
}

type RubricCriterion struct {
	Id    int          `json:"Id"`
	Name  string       `json:"Name"`
	Cells []RubricCell `json:"Cells"`
}

type RubricCriteriaGroup struct {
	Name       string            `json:"Name"`
	Levels     []RubricLevel     `json:"Levels"`
	Criteria   []RubricCriterion `json:"Criteria"`
	LevelSetId int               `json:"LevelSetId"`
}

type RubricOverallLevel struct {
	Id         int     `json:"Id"`
	Name       string  `json:"Name"`
	RangeStart float64 `json:"RangeStart"`
}

type Rubric struct {
	RubricId       int                   `json:"RubricId"`
	Name           string                `json:"Name"`
	Description    RichText              `json:"Description"`
	RubricType     int                   `json:"RubricType"`
	ScoringMethod  int                   `json:"ScoringMethod"`
	CriteriaGroups []RubricCriteriaGroup `json:"CriteriaGroups"`
	OverallLevels  []RubricOverallLevel  `json:"OverallLevels"`
}

func (r Rubric) CriterionName(id int) (string, bool) {
	for _, g := range r.CriteriaGroups {
		for _, c := range g.Criteria {
			if c.Id == id {
				return c.Name, true
			}
		}
	}
	return "", false
}

func (r Rubric) LevelName(id int) (string, bool) {
	for _, g := range r.CriteriaGroups {
		for _, l := range g.Levels {
			if l.Id == id {
				return l.Name, true
			}
		}
	}
	return "", false
}

func (r Rubric) CriterionMax(id int) float64 {
	best := 0.0
	for _, g := range r.CriteriaGroups {
		for _, c := range g.Criteria {
			if c.Id != id {
				continue
			}
			for _, l := range g.Levels {
				if l.Points > best {
					best = l.Points
				}
			}
		}
	}
	return best
}

func (r Rubric) MaxScore() float64 {
	total := 0.0
	for _, g := range r.CriteriaGroups {
		top := 0.0
		for _, l := range g.Levels {
			if l.Points > top {
				top = l.Points
			}
		}
		total += top * float64(len(g.Criteria))
	}
	return total
}

func (r Rubric) OverallLevelName(score float64) string {
	name := ""
	best := 0.0
	found := false
	for _, l := range r.OverallLevels {
		if score >= l.RangeStart && (!found || l.RangeStart >= best) {
			name, best, found = l.Name, l.RangeStart, true
		}
	}
	return name
}

type RubricFile struct {
	FileId   int    `json:"FileId"`
	FileName string `json:"FileName"`
	Size     int64  `json:"Size"`
}

type AssignmentFolder struct {
	Id                 int               `json:"Id"`
	CategoryId         *int              `json:"CategoryId"`
	Name               string            `json:"Name"`
	CustomInstructions RichText          `json:"CustomInstructions"`
	Attachments        []RubricFile      `json:"Attachments"`
	TotalFiles         int               `json:"TotalFiles"`
	UnreadFiles        int               `json:"UnreadFiles"`
	FlaggedFiles       int               `json:"FlaggedFiles"`
	Availability       *TimeWindow       `json:"Availability"`
	IsHidden           bool              `json:"IsHidden"`
	Assessment         *FolderAssessment `json:"Assessment"`
	DropboxType        int               `json:"DropboxType"`
	GroupTypeId        *int              `json:"GroupTypeId"`
	DueDate            string            `json:"DueDate"`
	DisplayInCalendar  bool              `json:"DisplayInCalendar"`
	LinkAttachments    []FeedbackLink    `json:"LinkAttachments"`
	ActivityId         string            `json:"ActivityId"`
	IsAnonymous        bool              `json:"IsAnonymous"`
	SubmissionType     int               `json:"SubmissionType"`
	CompletionType     int               `json:"CompletionType"`
	GradeItemId        *int              `json:"GradeItemId"`

	Description      string `json:"Description"`
	StartDate        string `json:"StartDate"`
	EndDate          string `json:"EndDate"`
	DisplayGradeType string `json:"DisplayGradeType"`
	IsLocked         bool   `json:"IsLocked"`
}

type FolderAssessment struct {
	ScoreDenominator *float64 `json:"ScoreDenominator"`
	Rubrics          []Rubric `json:"Rubrics"`
}

func (f AssignmentFolder) PrimaryRubric() *Rubric {
	if f.Assessment == nil || len(f.Assessment.Rubrics) == 0 {
		return nil
	}
	return &f.Assessment.Rubrics[0]
}

func (f AssignmentFolder) MaxPoints() (float64, bool) {
	if f.Assessment != nil && f.Assessment.ScoreDenominator != nil {
		return *f.Assessment.ScoreDenominator, true
	}
	if r := f.PrimaryRubric(); r != nil {
		if m := r.MaxScore(); m > 0 {
			return m, true
		}
	}
	return 0, false
}

func (f AssignmentFolder) Window() (start, end string) {
	if f.Availability != nil {
		return f.Availability.StartDate, f.Availability.EndDate
	}
	return f.StartDate, f.EndDate
}

func (f AssignmentFolder) SubmissionTypeLabel() string {
	switch f.SubmissionType {
	case 0:
		return "File"
	case 1:
		return "Text"
	case 2:
		return "On paper"
	case 3:
		return "Observed"
	case 4:
		return "File or Text"
	}
	return "—"
}

func (f AssignmentFolder) DropboxTypeLabel() string {
	if f.GroupTypeId != nil || f.DropboxType == 1 {
		return "Group"
	}
	return "Individual"
}

type SubmissionFile struct {
	FileId    int    `json:"FileId"`
	FileName  string `json:"FileName"`
	Size      int64  `json:"Size"`
	IsRead    bool   `json:"isRead"`
	IsFlagged bool   `json:"isFlagged"`
}

type SubmissionBy struct {
	Id          string `json:"Id"`
	DisplayName string `json:"DisplayName"`
}

type LearnerSubmission struct {
	Id             int              `json:"Id"`
	SubmittedBy    SubmissionBy     `json:"SubmittedBy"`
	SubmissionDate string           `json:"SubmissionDate"`
	Comment        RichText         `json:"Comment"`
	Files          []SubmissionFile `json:"Files"`

	FolderId      int    `json:"FolderId"`
	EntityType    string `json:"EntityType"`
	EntityId      int    `json:"EntityId"`
	SubmittedDate string `json:"SubmittedDate"`
	SubmittedText string `json:"SubmittedText"`
}

type Submission = LearnerSubmission

func (s LearnerSubmission) Date() string {
	if s.SubmissionDate != "" {
		return s.SubmissionDate
	}
	return s.SubmittedDate
}

func (s LearnerSubmission) Note() string {
	if s.Comment.Text != "" || s.Comment.Html != "" {
		return s.Comment.Plain()
	}
	return s.SubmittedText
}

const (
	dropboxUnsubmitted = 0
	dropboxSubmitted   = 1
	dropboxDraft       = 2
	dropboxPublished   = 3
)

func entityStatusName(s int) string {
	switch s {
	case dropboxSubmitted:
		return "Submitted"
	case dropboxDraft:
		return "Draft"
	case dropboxPublished:
		return "Published"
	default:
		return "Unsubmitted"
	}
}

type EntityDropbox struct {
	Entity         SubmissionBy        `json:"Entity"`
	Status         int                 `json:"Status"`
	Feedback       *Feedback           `json:"Feedback"`
	Submissions    []LearnerSubmission `json:"Submissions"`
	CompletionDate string              `json:"CompletionDate"`
}

type FeedbackCriteriaOutcome struct {
	CriterionId int          `json:"CriterionId"`
	LevelId     int          `json:"LevelId"`
	Score       json.Number  `json:"Score"`
	Feedback    FeedbackText `json:"Feedback"`
}

type FeedbackRubricAssessment struct {
	RubricId        int                       `json:"RubricId"`
	OverallScore    json.Number               `json:"OverallScore"`
	OverallFeedback FeedbackText              `json:"OverallFeedback"`
	CriteriaOutcome []FeedbackCriteriaOutcome `json:"CriteriaOutcome"`
}

type FeedbackFile struct {
	FileId   int    `json:"FileId"`
	FileName string `json:"FileName"`
	Size     int64  `json:"Size"`
}

type FeedbackLink struct {
	Type     string `json:"Type"`
	LinkId   int    `json:"LinkId"`
	LinkName string `json:"LinkName"`
	Href     string `json:"Href"`
}

type Feedback struct {
	Score             json.Number                `json:"Score"`
	GradedSymbol      string                     `json:"GradedSymbol"`
	IsGraded          bool                       `json:"IsGraded"`
	Feedback          FeedbackText               `json:"Feedback"`
	RubricAssessments []FeedbackRubricAssessment `json:"RubricAssessments"`
	Files             []FeedbackFile             `json:"Files"`
	Links             []FeedbackLink             `json:"Links"`
}

var errSubmissionsBlocked = fmt.Errorf("submissions blocked")

func assignmentCmdTree() (*cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command, *cobra.Command) {
	root := &cobra.Command{
		Use:   "assignment",
		Short: "List assignment folders, view my submissions, and download files (alias: dropbox)",
		Long: "Browse a course's assignment folders, view your submissions, and\n" +
			"download submission / feedback files.\n\n" +
			"`assignment get` is the wide view: it fetches the folder, its rubric,\n" +
			"your submissions and the released feedback in one go and renders them\n" +
			"as one report. Pass --no-related to fetch the folder alone.\n\n" +
			"`dropbox` is an alias for `assignment` (D2L's internal term and the\n" +
			"`/dropbox/` URL prefix). Both names are interchangeable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List assignment folders in a course",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runAssignmentList() },
	}
	get := &cobra.Command{
		Use:   "get <folderId>",
		Short: "Show one assignment folder, with my submissions and feedback",
		Long: "Fetch one dropbox folder and render everything a student needs for\n" +
			"it in one report: the availability window, the rubric, your\n" +
			"submissions (with files), and the released feedback and score.\n\n" +
			"Related lookups run by default and degrade quietly when D2L refuses\n" +
			"(a closed folder blocks submissions but still serves feedback).\n" +
			"Pass --no-related to fetch only the folder record.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentGet(args[0]) },
	}
	subs := &cobra.Command{
		Use:   "submissions <folderId>",
		Short: "List my submissions for one assignment folder",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissions(args[0]) },
	}
	fb := &cobra.Command{
		Use:   "feedback <folderId> [user|group|me [entityId]]",
		Short: "Show feedback on a submission (default: yours)",
		Long: "Fetch the feedback entry for one entity in a dropbox folder.\n\n" +
			"With no extra arguments this shows your own feedback; `me` as the\n" +
			"second argument resolves the same way. Pass `user <entityId>` or\n" +
			"`group <entityId>` to view another entity's entry.\n\n" +
			"When the folder's rubric is readable the criteria are rendered by\n" +
			"name and level rather than by numeric id.",
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error { return runAssignmentFeedback(args) },
	}
	subDl := &cobra.Command{
		Use:   "submission <folderId> <submissionId> download <fileId>",
		Short: "Download a file from one of my submissions",
		Args:  cobra.ExactArgs(4),
		RunE:  func(cmd *cobra.Command, args []string) error { return runAssignmentSubmissionDownload(args) },
	}
	fbDl := &cobra.Command{
		Use:   "feedback <folderId> [entityId|me] download <fileId>",
		Short: "Download a feedback attachment (default: your own)",
		Args:  cobra.RangeArgs(3, 4),
		RunE:  func(cmd *cobra.Command, args []string) error { return runAssignmentFeedbackDownload(args) },
	}

	addAsgFlags := func(cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.Flags().StringVar(&asgCourse, "course", "", "Course OrgUnitId (required)")
			c.Flags().StringVarP(&asgEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
			c.Flags().BoolVar(&asgAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
			c.Flags().BoolVar(&asgJSON, "json", false, "Emit a JSON envelope instead of a report")
		}
	}
	addAsgFlags(list, get, subs, fb, subDl, fbDl)
	get.Flags().BoolVar(&asgNoRelated, "no-related", false, "Fetch only the folder; skip submissions and feedback")
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
		Use: "get <folderId>", Short: "Show one assignment folder, with my submissions and feedback", Args: cobra.ExactArgs(1),
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
			c.Flags().BoolVar(&asgJSON, "json", false, "Emit a JSON envelope instead of a report")
		}
	}
	addAsgFlags(list, get, subs, fb, subDl, fbDl)
	get.Flags().BoolVar(&asgNoRelated, "no-related", false, "Fetch only the folder; skip submissions and feedback")
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

func asgURL(courseID, path string) string {
	return fmt.Sprintf("%s/d2l/api/le/%s/%s/dropbox/%s", ua.D2LBase, dupdata.D2LVersion(), courseID, path)
}

func fetchFolder(jar *cookiejar.Jar, courseID, folderID string) (AssignmentFolder, error) {
	raw := asgURL(courseID, "folders/"+folderID)
	var out AssignmentFolder
	if err := dupdata.FetchJSON(raw, jar, &out); err != nil {
		return out, err
	}
	return out, nil
}

func fetchMySubmissions(jar *cookiejar.Jar, courseID, folderID string) ([]EntityDropbox, error) {
	raw := asgURL(courseID, "folders/"+folderID+"/submissions/mysubmissions/")
	res, err := httpclient.FollowRedirects(raw, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 403 {
		return nil, errSubmissionsBlocked
	}
	if res.StatusCode != 200 {
		return nil, apiError(res, raw)
	}
	var items []EntityDropbox
	if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func fetchFeedback(jar *cookiejar.Jar, courseID, folderID, entityType, entityID string) (*Feedback, error) {
	raw := asgURL(courseID, fmt.Sprintf("folders/%s/feedback/%s/%s", folderID, entityType, entityID))
	res, err := httpclient.FollowRedirects(raw, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, nil
	}
	if res.StatusCode == 403 {
		return nil, errSubmissionsBlocked
	}
	if res.StatusCode != 200 {
		return nil, apiError(res, raw)
	}
	var out Feedback
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func folderStatus(f AssignmentFolder) string {
	now := time.Now()
	start, end := f.Window()
	if t, ok := render.Parse(start); ok && now.Before(t) {
		return "upcoming"
	}
	if t, ok := render.Parse(end); ok && now.After(t) {
		return "closed"
	}
	if t, ok := render.Parse(f.DueDate); ok && now.After(t) {
		return "overdue"
	}
	return "open"
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
	raw := asgURL(courseID, "folders/?onlyCurrentStudentsAndGroups=true")
	var items []AssignmentFolder
	if err := dupdata.FetchJSON(raw, ens.Jar, &items); err != nil {
		return err
	}
	if asgJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No assignment folders in this course."))
		return nil
	}
	fmt.Print(render.Title(fmt.Sprintf("Assignment folders · %s", courseID)))
	rows := make([][]string, len(items))
	for i, f := range items {
		_, end := f.Window()
		due := f.DueDate
		if due == "" {
			due = end
		}
		rows[i] = []string{
			strconv.Itoa(f.Id),
			render.StatusWord(folderStatus(f)),
			render.DateTime(due),
			f.SubmissionTypeLabel(),
			render.Truncate(f.Name, 60),
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"ID", "Status", "Due", "Type", "Name"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Fixed(10), table.Fixed(24), table.Fixed(12), table.Flex(30)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	fmt.Print(render.Hint("schooltools assignment get <id> --course %s   for submissions, rubric and feedback", courseID))
	return nil
}

type assignmentReport struct {
	Folder      AssignmentFolder    `json:"folder"`
	Submissions []LearnerSubmission `json:"submissions"`
	Feedback    *Feedback           `json:"feedback"`
	Notes       []string            `json:"notes"`
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
	folder, err := fetchFolder(ens.Jar, courseID, folderID)
	if err != nil {
		return err
	}

	rep := assignmentReport{Folder: folder, Submissions: []LearnerSubmission{}}
	if !asgNoRelated {
		entities, serr := fetchMySubmissions(ens.Jar, courseID, folderID)
		switch {
		case serr == errSubmissionsBlocked:
			rep.Notes = append(rep.Notes, "D2L blocks this folder's submissions now that it has closed. Feedback is still readable below.")
		case serr != nil:
			rep.Notes = append(rep.Notes, "Could not read submissions: "+serr.Error())
		default:
			for _, e := range entities {
				if e.Feedback != nil && rep.Feedback == nil {
					rep.Feedback = e.Feedback
				}
				rep.Submissions = append(rep.Submissions, e.Submissions...)
			}
		}

		if rep.Feedback == nil {
			uid, uerr := dupdata.CurrentUserID(ens.Jar)
			if uerr == nil {
				fb, ferr := fetchFeedback(ens.Jar, courseID, folderID, "user", uid)
				switch {
				case ferr == errSubmissionsBlocked:
					rep.Notes = append(rep.Notes, "Feedback is also behind the folder's closed-window wall.")
				case ferr != nil:
					rep.Notes = append(rep.Notes, "Could not read feedback: "+ferr.Error())
				default:
					rep.Feedback = fb
				}
			}
		}
	}

	if asgJSON {
		return render.PrintJSON(rep)
	}
	fmt.Print(renderAssignmentReport(rep))
	return nil
}

func renderAssignmentReport(rep assignmentReport) string {
	f := rep.Folder
	var b strings.Builder

	b.WriteString(render.Title(f.Name))

	start, end := f.Window()
	rows := []render.Row{{Key: "Folder", Value: fmt.Sprintf("%d  ·  %s  ·  %s submission", f.Id, f.DropboxTypeLabel(), f.SubmissionTypeLabel())}}
	if f.DueDate != "" {
		rows = append(rows, render.Row{Key: "Due", Value: render.When(f.DueDate)})
	}
	rows = append(rows, render.Row{Key: "Open", Value: render.Window(start, end)})
	b.WriteString(render.KV(rows...))

	if max, ok := f.MaxPoints(); ok {
		b.WriteString(render.KV(render.Row{Key: "Out of", Value: fmt.Sprintf("%s points", trimNum(max))}))
	}

	if instr := f.CustomInstructions.Plain(); instr != "" {
		b.WriteString(render.Section("Instructions"))
		b.WriteString(render.Wrap(instr, render.Width()) + "\n")
	}
	for _, a := range f.Attachments {
		b.WriteString(render.Bullets(fmt.Sprintf("%s (%s)   → schooltools assignment folder %d attachment %d", a.FileName, render.HumanBytes(a.Size), f.Id, a.FileId)))
	}

	b.WriteString(render.Section("Status"))
	b.WriteString(statusSection(rep))

	if len(rep.Notes) > 0 {
		b.WriteString(render.Section("Notes"))
		for _, n := range rep.Notes {
			b.WriteString(render.Note("%s", n))
		}
	}

	if !asgNoRelated {
		b.WriteString(render.Section("Your submissions"))
		b.WriteString(submissionsSection(rep))
		b.WriteString(render.Section("Feedback"))
		b.WriteString(feedbackSection(rep))
	}

	b.WriteString(render.Section("Next"))
	b.WriteString(render.Hint("schooltools assignment feedback %d --course %s", f.Id, asgCourse))
	if len(rep.Submissions) > 0 {
		b.WriteString(render.Hint("schooltools assignment submissions %d --course %s", f.Id, asgCourse))
	}
	return b.String()
}

func statusSection(rep assignmentReport) string {
	f := rep.Folder
	var rows []render.Row
	rows = append(rows, render.Row{Key: "Window", Value: render.StatusWord(folderStatus(f))})

	switch {
	case len(rep.Submissions) > 0:
		latest := rep.Submissions[len(rep.Submissions)-1]
		rows = append(rows, render.Row{Key: "Submitted", Value: render.OK(fmt.Sprintf("yes  ·  %s", render.When(latest.Date())))})
	case rep.Feedback != nil:
		rows = append(rows, render.Row{Key: "Submitted", Value: render.Muted("not visible (folder closed)")})
	default:
		rows = append(rows, render.Row{Key: "Submitted", Value: render.Muted("no")})
	}

	if rep.Feedback != nil && rep.Feedback.IsGraded {
		rows = append(rows, render.Row{Key: "Marked", Value: render.OK("graded") + "  ·  " + feedbackScoreLine(rep.Feedback, f.PrimaryRubric())})
	} else if rep.Feedback != nil {
		rows = append(rows, render.Row{Key: "Marked", Value: render.Warn("draft — not yet released")})
	} else {
		rows = append(rows, render.Row{Key: "Marked", Value: render.Muted("not yet")})
	}
	return render.KV(rows...)
}

func submissionsSection(rep assignmentReport) string {
	if len(rep.Submissions) == 0 {
		return render.Empty("No submissions recorded.")
	}
	var b strings.Builder
	for i, s := range rep.Submissions {
		b.WriteString(fmt.Sprintf("  %s %s\n",
			render.Accent(fmt.Sprintf("#%d", i+1)),
			render.When(s.Date())))
		if note := s.Note(); note != "" {
			b.WriteString("    " + render.Wrap(note, render.Width()-4) + "\n")
		}
		for _, f := range s.Files {
			flag := ""
			if f.IsFlagged {
				flag = "  " + render.Warn("flagged")
			}
			b.WriteString("    " + render.KV(
				render.Row{Key: "File", Value: fmt.Sprintf("%s  (%s)%s", f.FileName, render.HumanBytes(f.Size), flag)},
				render.Row{Key: "Get", Value: fmt.Sprintf("schooltools assignment submission %d %d download %d --course %s", rep.Folder.Id, s.Id, f.FileId, asgCourse)},
			))
		}
	}
	return b.String()
}

func feedbackSection(rep assignmentReport) string {
	fb := rep.Feedback
	if fb == nil {
		return render.Empty("No feedback released yet.")
	}
	return formatFeedbackRubric(*fb, rep.Folder.PrimaryRubric(), rep.Folder.Id, asgCourse)
}

func feedbackScoreLine(fb *Feedback, rubric *Rubric) string {
	if fb.GradedSymbol != "" {
		return fb.GradedSymbol
	}
	score := ""
	if fb.Score.String() != "" {
		score = trimJSONNumber(fb.Score)
	}
	if len(fb.RubricAssessments) > 0 {
		if v, err := fb.RubricAssessments[0].OverallScore.Float64(); err == nil {
			score = trimNum(v)
		}
	}
	if score == "" {
		if fb.IsGraded {
			return "graded (no score published)"
		}
		return "—"
	}
	if rubric != nil {
		if max := rubric.MaxScore(); max > 0 {
			return fmt.Sprintf("%s / %s points", score, trimNum(max))
		}
	}
	return score + " points"
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
	entities, err := fetchMySubmissions(ens.Jar, courseID, folderID)
	if err == errSubmissionsBlocked {
		return fmt.Errorf("D2L refused access to this folder's submissions (403 Not Authorized); the folder may be past its availability window — try `schooltools assignment feedback %s --course %s`, which stays readable", folderID, courseID)
	}
	if err != nil {
		return err
	}
	var subs []LearnerSubmission
	for _, e := range entities {
		subs = append(subs, e.Submissions...)
	}
	if asgJSON {
		return render.PrintJSON(render.NewList(subs))
	}
	if len(subs) == 0 {
		fmt.Print(render.Empty("No submissions yet for this folder."))
		return nil
	}
	fmt.Print(render.Title("My submissions"))
	for i, s := range subs {
		fmt.Printf("  %s %s\n", render.Accent(fmt.Sprintf("#%d", i+1)), render.When(s.Date()))
		if note := s.Note(); note != "" {
			fmt.Println("    " + render.Wrap(note, render.Width()-4))
		}
		for _, f := range s.Files {
			fmt.Println("    " + render.KV(
				render.Row{Key: "File", Value: fmt.Sprintf("%s  (%s)", f.FileName, render.HumanBytes(f.Size))},
				render.Row{Key: "Get", Value: fmt.Sprintf("schooltools assignment submission %s %d download %d --course %s", folderID, s.Id, f.FileId, courseID)},
			))
		}
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
	if entityID == "me" {
		uid, err := dupdata.CurrentUserID(ens.Jar)
		if err != nil {
			return err
		}
		entityID = uid
	}
	out, err := fetchFeedback(ens.Jar, courseID, folderID, entityType, entityID)
	if err == errSubmissionsBlocked {
		return fmt.Errorf("D2L refused access to this folder's feedback (403 Not Authorized); the folder may be past its availability window")
	}
	if err != nil {
		return err
	}
	if out == nil {
		fmt.Print(render.Empty("No feedback yet for this entity (nothing has been graded for it)."))
		return nil
	}
	if asgJSON {
		return render.PrintJSON(out)
	}
	var rubric *Rubric
	if folder, ferr := fetchFolder(ens.Jar, courseID, folderID); ferr == nil {
		rubric = folder.PrimaryRubric()
	}
	fid, _ := strconv.Atoi(folderID)
	fmt.Print(render.Title(fmt.Sprintf("Feedback · folder %s", folderID)))
	fmt.Print(formatFeedbackRubric(*out, rubric, fid, courseID))
	return nil
}

func formatFeedback(out Feedback) string {
	return formatFeedbackRubric(out, nil, 0, "")
}

func formatFeedbackRubric(out Feedback, rubric *Rubric, folderID int, courseID string) string {
	var b strings.Builder
	b.WriteString(render.KV(
		render.Row{Key: "Score", Value: feedbackScoreLine(&out, rubric)},
		render.Row{Key: "Status", Value: statusWord(out)},
	))

	if comment := out.Feedback.Plain(); comment != "" {
		b.WriteString(render.Section("Comment"))
		b.WriteString(render.Wrap(comment, render.Width()) + "\n")
	}

	if len(out.RubricAssessments) > 0 {
		b.WriteString(render.Section("Rubric"))
		for _, r := range out.RubricAssessments {
			if rubric != nil && rubric.Name != "" {
				b.WriteString("  " + render.Bold(rubric.Name) + "\n")
			}
			b.WriteString(rubricRows(r, rubric))
			if score, err := r.OverallScore.Float64(); err == nil && rubric != nil {
				value := trimNum(score)
				if max := rubric.MaxScore(); max > 0 {
					value = fmt.Sprintf("%s / %s", value, trimNum(max))
				}
				if lvl := rubric.OverallLevelName(score); lvl != "" {
					value += "  →  " + render.Accent(lvl)
				}
				b.WriteString(fmt.Sprintf("\n    %-*s  %s\n", 46, "Overall", value))
			}
		}
	}

	if len(out.Files) > 0 || len(out.Links) > 0 {
		b.WriteString(render.Section("Attachments"))
		for _, f := range out.Files {
			get := "schooltools assignment feedback download <fileId>"
			if folderID != 0 && courseID != "" {
				get = fmt.Sprintf("schooltools assignment feedback %d download %d --course %s", folderID, f.FileId, courseID)
			}
			b.WriteString("  " + render.KV(
				render.Row{Key: "File", Value: fmt.Sprintf("%s  (%s)", f.FileName, render.HumanBytes(f.Size))},
				render.Row{Key: "Get", Value: get},
			))
		}
		for _, l := range out.Links {
			name := l.LinkName
			if name == "" {
				name = l.Type
			}
			b.WriteString("  " + render.KV(render.Row{Key: "Link", Value: name}))
		}
	}
	return b.String()
}

func statusWord(out Feedback) string {
	if out.IsGraded {
		return render.OK("graded")
	}
	return render.Warn("draft — not yet released")
}

func rubricRows(r FeedbackRubricAssessment, rubric *Rubric) string {
	var b strings.Builder
	for _, c := range r.CriteriaOutcome {
		name := fmt.Sprintf("Criterion %d", c.CriterionId)
		max := 0.0
		level := ""
		if rubric != nil {
			if n, ok := rubric.CriterionName(c.CriterionId); ok {
				name = n
			}
			max = rubric.CriterionMax(c.CriterionId)
			if n, ok := rubric.LevelName(c.LevelId); ok {
				level = render.Muted(n)
			}
		}

		mark := render.Muted("·")
		if max > 0 {
			if v, err := c.Score.Float64(); err == nil && v < max {
				mark = render.Warn("△")
			} else {
				mark = render.OK("✓")
			}
		}

		score := trimJSONNumber(c.Score)
		if max > 0 {
			score = fmt.Sprintf("%s / %s", score, trimNum(max))
		}

		line := fmt.Sprintf("    %s %-*s  %s", mark, 46, render.Truncate(name, 46), score)
		if level != "" {
			line += "  " + level
		}
		b.WriteString(line + "\n")

		if note := c.Feedback.Plain(); note != "" {
			for _, w := range strings.Split(render.Wrap(note, render.Width()-8), "\n") {
				b.WriteString("        " + w + "\n")
			}
		}
	}
	return b.String()
}

func trimNum(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return s
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
	raw := asgURL(courseID, fmt.Sprintf("folders/%s/submissions/%s/files/%s", args[0], args[1], args[3]))
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
	if entityID == "me" {
		uid, err := dupdata.CurrentUserID(ens.Jar)
		if err != nil {
			return err
		}
		entityID = uid
	}
	raw := asgURL(courseID, fmt.Sprintf("folders/%s/feedback/user/%s/attachments/%s", folderID, entityID, fileID))
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
	fmt.Print(render.KV(
		render.Row{Key: "Saved", Value: path},
		render.Row{Key: "Size", Value: render.HumanBytes(int64(len(body)))},
	))
	return nil
}
