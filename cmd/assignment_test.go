package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSplitFeedbackEntity(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantType     string
		wantEntityID string
	}{
		{name: "no extras", args: nil, wantType: "user", wantEntityID: "me"},
		{name: "folder only", args: []string{"2000001"}, wantType: "user", wantEntityID: "me"},
		{name: "me in type slot falls back to own id",
			args: []string{"2000001", "me"}, wantType: "user", wantEntityID: "me"},
		{name: "me is case-insensitive",
			args: []string{"2000001", "ME"}, wantType: "user", wantEntityID: "me"},
		{name: "explicit user keeps me default",
			args: []string{"2000001", "user"}, wantType: "user", wantEntityID: "me"},
		{name: "user with id", args: []string{"2000001", "user", "100001"}, wantType: "user", wantEntityID: "100001"},
		{name: "group with id", args: []string{"2000001", "group", "1042"}, wantType: "group", wantEntityID: "1042"},
		{name: "me then explicit id", args: []string{"2000001", "me", "100001"}, wantType: "user", wantEntityID: "100001"},
	}
	for _, tc := range cases {
		gotType, gotEntityID := splitFeedbackEntity(tc.args)
		require.Equal(t, tc.wantType, gotType, tc.name)
		require.Equal(t, tc.wantEntityID, gotEntityID, tc.name)
	}
}

const liveFolderJSON = `{
  "Id":2000001,"CategoryId":null,"Name":"Lab Report Feedback",
  "CustomInstructions":{"Text":"","Html":""},
  "Attachments":[{"FileId":4411,"FileName":"rubric.pdf","Size":10240}],
  "TotalFiles":-1,"UnreadFiles":-1,"FlaggedFiles":-1,
  "Availability":{"StartDate":null,"EndDate":"2026-10-19T04:00:00.000Z",
    "StartDateAvailabilityType":null,"EndDateAvailabilityType":0},
  "IsHidden":false,
  "Assessment":{"ScoreDenominator":null,"Rubrics":[
    {"RubricId":900001,"Name":"Lab Report Rubric","RubricType":1,"ScoringMethod":1,
     "CriteriaGroups":[{"Name":"Criteria","LevelSetId":788606,
       "Levels":[{"Id":920001,"Name":"Level 2","Points":2.0},{"Id":920002,"Name":"Level 1","Points":1.0}],
       "Criteria":[
         {"Id":910001,"Name":"Materials","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910002,"Name":"Scale","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910003,"Name":"Accuracy","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910004,"Name":"Detail","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910005,"Name":"Labels","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910006,"Name":"Layout","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]},
         {"Id":910007,"Name":"Conclusion","Cells":[
           {"Feedback":{"Text":"","Html":""},"Description":{"Text":"","Html":""},"Points":null,"LevelId":920001}]}
       ]}],
     "OverallLevels":[
       {"Id":920006,"Name":"Level 4","RangeStart":11.0},
       {"Id":920005,"Name":"Level 3","RangeStart":8.0},
       {"Id":920004,"Name":"Level 2","RangeStart":5.0},
       {"Id":920003,"Name":"Level 1","RangeStart":0.0}]}]},
  "DropboxType":2,"GroupTypeId":null,"DueDate":"2026-10-19T04:00:00.000Z",
  "DisplayInCalendar":false,"LinkAttachments":[],"IsAnonymous":false,
  "SubmissionType":0,"CompletionType":0,"GradeItemId":null
}`

func TestAssignmentFolderDecodesLiveShape(t *testing.T) {
	var f AssignmentFolder
	require.NoError(t, json.Unmarshal([]byte(liveFolderJSON), &f))

	require.Equal(t, 2000001, f.Id)
	require.Equal(t, "Lab Report Feedback", f.Name)
	require.Equal(t, "Individual", f.DropboxTypeLabel())
	require.Equal(t, "File", f.SubmissionTypeLabel())

	start, end := f.Window()
	require.Equal(t, "", start)
	require.Equal(t, "2026-10-19T04:00:00.000Z", end)

	require.Len(t, f.Attachments, 1)
	require.Equal(t, "rubric.pdf", f.Attachments[0].FileName)

	rubric := f.PrimaryRubric()
	require.NotNil(t, rubric)
	require.Equal(t, 900001, rubric.RubricId)

	name, ok := rubric.CriterionName(910004)
	require.True(t, ok)
	require.Equal(t, "Detail", name)

	level, ok := rubric.LevelName(920002)
	require.True(t, ok)
	require.Equal(t, "Level 1", level)

	require.Equal(t, 2.0, rubric.CriterionMax(910001))
	require.Equal(t, 14.0, rubric.MaxScore())
	require.Equal(t, "Level 4", rubric.OverallLevelName(13))
	require.Equal(t, "Level 2", rubric.OverallLevelName(5))
	require.Equal(t, "Level 1", rubric.OverallLevelName(0))
}

func TestAssignmentFolderMaxPointsFromDenominator(t *testing.T) {
	den := 20.0
	f := AssignmentFolder{Assessment: &FolderAssessment{ScoreDenominator: &den}}
	got, ok := f.MaxPoints()
	require.True(t, ok)
	require.Equal(t, 20.0, got)
}

func TestEntityDropboxDecodesSubmissionShape(t *testing.T) {
	sample := `[
	  {"Entity":{"Id":"100001","DisplayName":"Student One"},
	   "Status":1,
	   "Feedback":{"Score":null,"IsGraded":true,
	     "Feedback":{"Text":"Nice work.","Html":"<p>Nice work.</p>"},
	     "RubricAssessments":[],"Files":[],"Links":[],"GradedSymbol":null},
	   "Submissions":[{"Id":8871234,
	     "SubmittedBy":{"Id":"100001","DisplayName":"Student One"},
	     "SubmissionDate":"2026-10-18T21:12:00.000Z",
	     "Comment":{"Text":"here is my drawing","Html":""},
	     "Files":[{"FileId":4451,"FileName":"drawing.jpg","Size":2201600,"isRead":true,"isFlagged":false}]}],
	   "CompletionDate":null}
	]`
	var items []EntityDropbox
	require.NoError(t, json.Unmarshal([]byte(sample), &items))
	require.Len(t, items, 1)

	e := items[0]
	require.Equal(t, "Submitted", entityStatusName(e.Status))
	require.NotNil(t, e.Feedback)
	require.Equal(t, "Nice work.", e.Feedback.Feedback.Plain())
	require.Len(t, e.Submissions, 1)

	s := e.Submissions[0]
	require.Equal(t, 8871234, s.Id)
	require.Equal(t, "2026-10-18T21:12:00.000Z", s.Date())
	require.Equal(t, "here is my drawing", s.Note())
	require.Len(t, s.Files, 1)
	require.Equal(t, "drawing.jpg", s.Files[0].FileName)
	require.Equal(t, int64(2201600), s.Files[0].Size)
}

const liveFeedbackJSON = `{
	"Files": [],
	"Links": [],
	"Score": null,
	"Feedback": {"Text": "Nice work.", "Html": "<p>Nice work.</p>"},
	"GradedSymbol": null,
	"IsGraded": true,
	"RubricAssessments": [{
		"RubricId": 900001,
		"OverallScore": 13.000000000,
		"OverallFeedback": {"Text": "", "Html": ""},
		"CriteriaOutcome": [
			{"CriterionId": 910001, "LevelId": 920001, "Score": 2.000000000, "Feedback": {"Text": "", "Html": ""}},
			{"CriterionId": 910004, "LevelId": 920002, "Score": 1.000000000,
			 "Feedback": {"Text": "Keep the lines continuous rather than broken", "Html": ""}}
		]
	}]
}`

func decodeLiveFeedback(t *testing.T) Feedback {
	t.Helper()
	var out Feedback
	require.NoError(t, json.Unmarshal([]byte(liveFeedbackJSON), &out))
	return out
}

func TestFeedbackDecode(t *testing.T) {
	out := decodeLiveFeedback(t)
	require.Equal(t, json.Number(""), out.Score)
	require.Equal(t, "Nice work.", out.Feedback.Text)
	require.True(t, out.IsGraded)
	require.Len(t, out.RubricAssessments, 1)

	r := out.RubricAssessments[0]
	require.Equal(t, json.Number("13.000000000"), r.OverallScore)
	require.Len(t, r.CriteriaOutcome, 2)
	require.Equal(t, 910001, r.CriteriaOutcome[0].CriterionId)
}

func TestFormatFeedbackWithoutRubric(t *testing.T) {
	out := decodeLiveFeedback(t)
	got := formatFeedback(out)

	require.Contains(t, got, "Score   13 points")
	require.Contains(t, got, "graded")
	require.Contains(t, got, "Comment")
	require.Contains(t, got, "Nice work.")
	require.Contains(t, got, "Criterion 910001")
	require.Contains(t, got, "Criterion 910004")
	require.Contains(t, got, `Keep the lines continuous rather than broken`)
	require.NotContains(t, got, "13.000000000")
}

func TestFormatFeedbackWithRubricNamesCriteria(t *testing.T) {
	out := decodeLiveFeedback(t)
	var f AssignmentFolder
	require.NoError(t, json.Unmarshal([]byte(liveFolderJSON), &f))

	got := formatFeedbackRubric(out, f.PrimaryRubric(), f.Id, "3000001")

	require.Contains(t, got, "Score   13 / 14 points")
	require.Contains(t, got, "Detail")
	require.Contains(t, got, "Materials")
	require.Contains(t, got, "Level 1")
	require.Contains(t, got, "Overall")
	require.NotContains(t, got, "Criterion 910004")
}

func TestFormatFeedbackListsAttachments(t *testing.T) {
	out := decodeLiveFeedback(t)
	out.Files = []FeedbackFile{{FileId: 9987, FileName: "annotated.jpg", Size: 831488}}
	out.Links = []FeedbackLink{{Type: "MediaContent", LinkId: 4, LinkName: "voice note"}}

	got := formatFeedbackRubric(out, nil, 2000001, "3000001")

	require.Contains(t, got, "Attachments")
	require.Contains(t, got, "annotated.jpg")
	require.Contains(t, got, "schooltools assignment feedback 2000001 download 9987 --course 3000001")
	require.Contains(t, got, "voice note")
}

func TestFeedbackScoreLineVariants(t *testing.T) {
	rubric := &Rubric{CriteriaGroups: []RubricCriteriaGroup{{
		Levels:   []RubricLevel{{Id: 1, Name: "Full", Points: 2}, {Id: 2, Name: "Part", Points: 1}},
		Criteria: []RubricCriterion{{Id: 10, Name: "a"}},
	}}}

	full := decodeLiveFeedback(t)
	require.Equal(t, "13 / 2 points", feedbackScoreLine(&full, rubric))

	symbol := Feedback{GradedSymbol: "B+", IsGraded: true}
	require.Equal(t, "B+", feedbackScoreLine(&symbol, nil))

	plain := Feedback{Score: json.Number("7.5"), IsGraded: true}
	require.Equal(t, "7.5 points", feedbackScoreLine(&plain, nil))

	empty := Feedback{IsGraded: false}
	require.Equal(t, "—", feedbackScoreLine(&empty, nil))

	gradedNoScore := Feedback{IsGraded: true}
	require.Equal(t, "graded (no score published)", feedbackScoreLine(&gradedNoScore, nil))
}

func TestTrimJSONNumber(t *testing.T) {
	cases := []struct {
		in   json.Number
		want string
	}{
		{json.Number("13"), "13"},
		{json.Number("13.000000000"), "13"},
		{json.Number("1.500000000"), "1.5"},
		{json.Number("0.100"), "0.1"},
		{json.Number("1.0e3"), "1.0e3"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, trimJSONNumber(tc.in), string(tc.in))
	}
}

func TestRichTextPlainPrefersText(t *testing.T) {
	require.Equal(t, "hi", RichText{Text: "hi", Html: "<p>hi</p>"}.Plain())
	require.Equal(t, "hi there", RichText{Html: "<p>hi&#160;there</p>"}.Plain())
	require.Equal(t, "", RichText{}.Plain())
}

func TestFolderStatus(t *testing.T) {
	past := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)

	require.Equal(t, "open", folderStatus(AssignmentFolder{}))
	require.Equal(t, "closed", folderStatus(AssignmentFolder{
		Availability: &TimeWindow{EndDate: past},
	}))
	require.Equal(t, "upcoming", folderStatus(AssignmentFolder{
		Availability: &TimeWindow{StartDate: future, EndDate: future},
	}))
	require.Equal(t, "overdue", folderStatus(AssignmentFolder{DueDate: past}))
	require.Equal(t, "open", folderStatus(AssignmentFolder{DueDate: future}))
}
