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

// SnapshotData is the complete payload rendered by WriteSnapshot.
type SnapshotData struct {
	Label   string
	Created string
	Counts  map[task.Status]int
	Ready   []task.Task
	Todo    []task.Task
	Doing   []task.Task
	Health  task.QueueHealth
	Detail  *SnapshotTaskDetail
	Now     time.Time
}

type snapshotCounts struct {
	QueueCounts
	Ready int `json:"ready"`
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
	Health  task.QueueHealth    `json:"health"`
	Task    *snapshotTaskDetail `json:"task,omitzero"`
}

// WriteSnapshot renders a read-only queue evidence snapshot.
func WriteSnapshot(w io.Writer, data SnapshotData) error {
	doc := snapshotDoc{
		Label:   data.Label,
		Created: data.Created,
		Counts: snapshotCounts{
			QueueCounts: NewQueueCounts(data.Counts),
			Ready:       len(data.Ready),
		},
		Tasks: snapshotTasks{
			Ready: statusListJSON(data.Ready),
			Todo:  statusListJSON(data.Todo),
			Doing: statusDoingListJSON(data.Doing, data.Now),
		},
		Health: data.Health,
	}
	if data.Detail != nil {
		events, eventsOmitted := limitEvents(data.Detail.Events)
		attempts, attemptsOmitted := limitAttempts(data.Detail.Attempts)
		doc.Task = &snapshotTaskDetail{
			Task:            boundTask(data.Detail.Task, maxDetailBodyRunes),
			Events:          boundEvents(events),
			Attempts:        boundAttempts(attempts),
			EventsOmitted:   eventsOmitted,
			AttemptsOmitted: attemptsOmitted,
		}
	}
	return WriteJSONLine(w, doc, "snapshot")
}
