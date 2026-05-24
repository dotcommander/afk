package output

import (
	"io"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// SnapshotTaskDetail carries optional task history for snapshot output.
type SnapshotTaskDetail struct {
	Task     task.Task
	Events   []task.Event
	Attempts []task.Attempt
}

type snapshotCounts struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
	Ready   int `json:"ready"`
}

type snapshotTasks struct {
	Ready []boundedTask `json:"ready"`
	Todo  []boundedTask `json:"todo"`
	Doing []boundedTask `json:"doing"`
}

type snapshotTaskDetail struct {
	Task            boundedTask    `json:"task"`
	Events          []task.Event   `json:"events"`
	Attempts        []task.Attempt `json:"attempts"`
	EventsOmitted   int            `json:"events_omitted,omitzero"`
	AttemptsOmitted int            `json:"attempts_omitted,omitzero"`
}

type snapshotDoc struct {
	Label   string              `json:"label,omitzero"`
	Created string              `json:"created"`
	Counts  snapshotCounts      `json:"counts"`
	Tasks   snapshotTasks       `json:"tasks"`
	Task    *snapshotTaskDetail `json:"task,omitzero"`
}

// WriteSnapshot renders a read-only queue evidence snapshot.
func WriteSnapshot(w io.Writer, label, created string, tally map[task.Status]int, ready, todo, doing []task.Task, detail *SnapshotTaskDetail, now time.Time) error {
	total := 0
	for _, n := range tally {
		total += n
	}
	doc := snapshotDoc{
		Label:   label,
		Created: created,
		Counts: snapshotCounts{
			Todo:    tally[task.StatusPending],
			Doing:   tally[task.StatusWorking],
			Done:    tally[task.StatusDone],
			Failed:  tally[task.StatusFailed],
			Deleted: tally[task.StatusDeleted],
			Total:   total,
			Ready:   len(ready),
		},
		Tasks: snapshotTasks{
			Ready: statusListJSON(ready),
			Todo:  statusListJSON(todo),
			Doing: statusDoingListJSON(doing, now),
		},
	}
	if detail != nil {
		events, eventsOmitted := limitEvents(detail.Events)
		attempts, attemptsOmitted := limitAttempts(detail.Attempts)
		doc.Task = &snapshotTaskDetail{
			Task:            boundTask(detail.Task, maxDetailBodyRunes),
			Events:          boundEvents(events),
			Attempts:        boundAttempts(attempts),
			EventsOmitted:   eventsOmitted,
			AttemptsOmitted: attemptsOmitted,
		}
	}
	return WriteJSONLine(w, doc, "snapshot")
}
