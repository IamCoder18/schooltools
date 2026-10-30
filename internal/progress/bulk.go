package progress

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/IamCoder18/schooltools/internal/content"
)

// BulkOptions drives one multi-course progress pass.
type BulkOptions struct {
	Client   *Client
	Plan     PlanOptions
	DryRun   bool
	Interval time.Duration
	// CourseIDs scopes the pass; empty means every enrolled course.
	CourseIDs []string
	// Courses lists the enrolled course ids when CourseIDs is empty.
	Courses func() ([]string, error)
	// Topics lists a course's topics; defaults to the live TOC.
	Topics func(orgUnitID string) ([]content.TocTopic, error)
	// Event receives progress callbacks; nil is fine.
	Event func(BulkEvent)
}

// BulkEvent reports pass progress to a spinner or log.
type BulkEvent struct {
	Phase     string
	CourseID  string
	TopicID   int
	TopicDone int
	TopicMax  int
}

// BulkResult summarizes one pass.
type BulkResult struct {
	CoursesScanned int       `json:"coursesScanned"`
	TopicsChecked  int       `json:"topicsChecked"`
	TopicsChanged  int       `json:"topicsChanged"`
	TopicsWritten  int       `json:"topicsWritten"`
	TopicsFailed   int       `json:"topicsFailed"`
	Changes        []Change  `json:"changes"`
	Failures       []Failure `json:"failures"`
}

// Failure records a topic whose write did not go through.
type Failure struct {
	CourseID string `json:"courseId"`
	TopicID  int    `json:"topicId"`
	Title    string `json:"title"`
	Error    string `json:"error"`
}

// Run walks every topic in scope, plans the requested change against the
// topic's current record, and (unless DryRun) writes it. Topics that need
// no change are never written; a write failure is counted and does not
// stop the pass.
func Run(opts BulkOptions) (BulkResult, error) {
	res := BulkResult{Changes: []Change{}, Failures: []Failure{}}
	if opts.Client == nil {
		return res, fmt.Errorf("progress: client is required")
	}
	listTopics := opts.Topics
	if listTopics == nil {
		listTopics = func(orgUnitID string) ([]content.TocTopic, error) {
			toc, err := content.FetchToc(orgUnitID, opts.Client.Jar)
			if err != nil {
				return nil, err
			}
			var out []content.TocTopic
			var walk func([]content.TocModule)
			walk = func(modules []content.TocModule) {
				for _, m := range modules {
					out = append(out, m.Topics...)
					walk(m.Modules)
				}
			}
			walk(toc.Modules)
			return out, nil
		}
	}

	courseIDs := opts.CourseIDs
	if len(courseIDs) == 0 {
		if opts.Courses == nil {
			return res, fmt.Errorf("progress: course lister is required when CourseIDs is empty")
		}
		all, err := opts.Courses()
		if err != nil {
			return res, err
		}
		courseIDs = all
	}

	pace := newPacer(opts.Interval)
	for _, courseID := range courseIDs {
		topics, err := listTopics(courseID)
		if err != nil {
			return res, fmt.Errorf("list topics for course %s: %w", courseID, err)
		}
		res.CoursesScanned++
		emit(opts.Event, BulkEvent{Phase: "course", CourseID: courseID, TopicMax: len(topics)})
		for _, t := range topics {
			pace.wait()
			cur, err := opts.Client.Fetch(courseID, t.TopicId)
			if err != nil {
				if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == 401 {
					return res, fmt.Errorf("session expired; run 'schooltools login' again")
				}
				res.TopicsFailed++
				res.Failures = append(res.Failures, Failure{CourseID: courseID, TopicID: t.TopicId, Title: t.Title, Error: err.Error()})
				continue
			}
			res.TopicsChecked++
			ch := Plan(cur, opts.Plan, opts.Client.now())
			ch.CourseID = courseID
			ch.TopicID = t.TopicId
			ch.Title = t.Title
			if ch.Action == "" {
				continue
			}
			res.TopicsChanged++
			if opts.DryRun {
				res.Changes = append(res.Changes, ch)
				continue
			}
			pace.wait()
			echo, err := opts.Client.Update(courseID, ch.After)
			if err != nil {
				if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == 401 {
					return res, fmt.Errorf("session expired; run 'schooltools login' again")
				}
				res.TopicsFailed++
				res.Failures = append(res.Failures, Failure{CourseID: courseID, TopicID: t.TopicId, Title: t.Title, Error: err.Error()})
				continue
			}
			ch.After = echo
			res.TopicsWritten++
			res.Changes = append(res.Changes, ch)
			emit(opts.Event, BulkEvent{Phase: "topic", CourseID: courseID, TopicID: t.TopicId, TopicDone: res.TopicsChecked, TopicMax: len(topics)})
		}
		emit(opts.Event, BulkEvent{Phase: "course-done", CourseID: courseID, TopicDone: res.TopicsChecked})
	}
	return res, nil
}

func emit(fn func(BulkEvent), ev BulkEvent) {
	if fn != nil {
		fn(ev)
	}
}

// pacer spaces out consecutive requests by a randomized gap in
// [interval, 2*interval); a zero interval disables pacing.
type pacer struct {
	interval time.Duration
	sleep    func(time.Duration)
}

func newPacer(interval time.Duration) *pacer {
	return &pacer{interval: interval, sleep: time.Sleep}
}

func (p *pacer) wait() {
	if p.interval <= 0 {
		return
	}
	d := p.interval
	if p.interval > 1 {
		d += time.Duration(rand.Int63n(int64(p.interval)))
	}
	p.sleep(d)
}
