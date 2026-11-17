package ps

// Student is the parsed /ws/xte/student/{DCID}/enrollments response.
// The shape is roughly PowerSchool's "students" PowerPlug record.
//
// PowerSchool emits a number of arbitrary record-level fields
// (photoflag, gradelevel, …) and an array of _enrollments where each
// entry mirrors the "enrollment" + "coursesectioninfo" structure. We
// only model the fields this CLI surfaces.
type Student struct {
	DCID        int64        `json:"dcid"`
	ID          int64        `json:"id"`
	PersonID    int64        `json:"personid"`
	StudentNo   string       `json:"studentnumber"`
	LastFirst   string       `json:"lastfirst"`
	FirstName   string       `json:"firstname"`
	LastName    string       `json:"lastname"`
	Gender      string       `json:"gender"`
	GradeLevel  int          `json:"gradelevel"`
	SchoolID    int          `json:"schoolid"`
	SchoolDCID  int64        `json:"schoolsdcid"`
	Enrollments []Enrollment `json:"_enrollments"`
}

// Enrollment is one active class section for a student, lifted from
// /ws/xte/student/{DCID}/enrollments.
type Enrollment struct {
	SectionDCID  int64       `json:"sectiondcid"`
	Status       string      `json:"statuscode"`
	EnrolledLate bool        `json:"enrolledlate"`
	Section      SectionInfo `json:"_coursesectioninfo"`
}

// SectionInfo is the joined course+section+teacher+term info that
// PowerSchool returns inline with each enrollment.
type SectionInfo struct {
	SectionNum       string `json:"sectionnum"`
	Expression       string `json:"expression"`
	CourseName       string `json:"coursename"`
	CourseNum        string `json:"coursenum"`
	CourseDCID       int64  `json:"coursesdcid"`
	SchoolName       string `json:"schoolname"`
	SchoolDCID       int64  `json:"schoolsdcid"`
	TermName         string `json:"termname"`
	TermAbbreviation string `json:"termabbreviation"`
	TeacherName      string `json:"teachername"`
	Completed        bool   `json:"completed"`
}

// Year is one academic year for the year picker, e.g. {"abbr":"26-27","yearid":36}.
type Year struct {
	Abbr   string `json:"abbr"`
	YearID int    `json:"yearid"`
}

// Standard is one standards-grading rubric definition attached to a
// section. Returned by /ws/xte/section/standard.
type Standard struct {
	ID                  int64  `json:"standardid"`
	Name                string `json:"name"`
	Identifier          string `json:"identifier"`
	Description         string `json:"description"`
	SubjectArea         string `json:"subjectarea"`
	YearID              int    `json:"yearid"`
	DisplayPos          int    `json:"displayposition"`
	IsExcluded          bool   `json:"isexcludedfromreports"`
	IsAssignmentAllowed bool   `json:"isassignmentallowed"`
	MaxCommentLength    int    `json:"maxcommentlength"`
}

// StandardGrade is one (student × section × standard) row, returned
// by POST /ws/xte/standard_grade_section. It carries the proficiency
// code (Grade) and the percentage PowerSchool calculated for that
// outcome (Percent), plus per-row flags (isexempt, isincomplete,
// islocked, isenrolled, …).
type StandardGrade struct {
	ID                      int64    `json:"standardgradesectionid"`
	StudentDCID             int64    `json:"studentsdcid"`
	StandardID              int64    `json:"standardid"`
	SectionDCID             int64    `json:"sectionsdcid"`
	SchoolDCID              int64    `json:"schoolsdcid"`
	YearID                  int      `json:"yearid"`
	GradeLevel              int      `json:"gradelevel"`
	StoreCode               string   `json:"storecode"`
	SchoolName              string   `json:"schoolname"`
	IsExempt                bool     `json:"isexempt"`
	IsIncomplete            bool     `json:"isincomplete"`
	IsLocked                bool     `json:"islocked"`
	IsEnrolled              bool     `json:"isenrolled"`
	IsMigrated              bool     `json:"ismigrated"`
	IsAdminModified         bool     `json:"isadminmodified"`
	IsReportsRollupExcluded bool     `json:"isreportsrollupexcluded"`
	IsManuallyOverridden    bool     `json:"ismanuallyoverridden"`
	LastUpdated             string   `json:"lastupdated"`
	Grade                   string   `json:"standardgrade"`
	Percent                 *float64 `json:"standardpercent"`
}

// SchoolCommentLimit is one entry from /ws/xte/school/comment. We use
// this to surface "max 2048 characters" per school so any future
// grade-comment command can validate.
type SchoolCommentLimit struct {
	SchoolID                 int   `json:"schoolid"`
	SchoolDCID               int64 `json:"schoolsdcid"`
	MaxFinalGradeCommentSize int   `json:"maxfinalgradecommentsize"`
}

// StemAssignment is one row from /guardian/studentSectionStemAssignments.json
// (the legacy per-cell assignment + score drill-down). The endpoint
// returns `{"data": [ {…} ]}`. The fields are strings (not numbers)
// because PowerSchool's legacy JSON emits everything as string —
// coerce to int at the consumer.
type StemAssignment struct {
	DCID               string   `json:"dcid"`
	StudentID          string   `json:"studentid"`
	SectionDCID        string   `json:"sectionDcid"`
	StandardID         string   `json:"standardId"`
	AssignmentID       string   `json:"assignmentId"`
	AssignmentName     string   `json:"assignmentName"`
	Category           string   `json:"category"`
	DueDate            string   `json:"dueDate"`
	ActualScoreEntered string   `json:"actualScoreEntered"`
	IsCollected        string   `json:"isCollected"`
	IsLate             string   `json:"isLate"`
	IsMissing          string   `json:"isMissing"`
	IsExempt           string   `json:"isExempt"`
	IsAbsent           string   `json:"isAbsent"`
	IsIncomplete       string   `json:"isIncomplete"`
	Percent            *float64 `json:"percent,omitempty"`
	Score              *float64 `json:"score,omitempty"`
	ScorePossible      *float64 `json:"scorepossible,omitempty"`
}

// StemAssignmentsResponse is the wrapper around the array of per-cell
// scores. PowerSchool's legacy endpoint returns {"data": [...]} even
// for a single row.
type StemAssignmentsResponse struct {
	Data []StemAssignment `json:"data"`
}
