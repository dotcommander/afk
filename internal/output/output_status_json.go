package output

import (
	"io"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

type statusTasksJSON struct {
	Todo  []boundedTask `json:"todo"`
	Doing []boundedTask `json:"doing"`
}

type blockedTaskJSON struct {
	Task     boundedTask    `json:"task"`
	Blockers []task.Blocker `json:"blockers"`
}

// QueueCounts is the canonical per-status tally shared by every JSON envelope
// in this package and by callers in internal/commands. Embed it (anonymous
// embedding) so JSON inlines the fields at the parent's top level — wire
// shape stays identical to the previous hand-written field lists. Adding a
// future status means editing this struct once instead of N parallel
// definitions.
type QueueCounts struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

// NewQueueCounts builds a QueueCounts from a status tally. Total is the sum
// of every map entry, so callers do not need to maintain it separately.
func NewQueueCounts(tally map[task.Status]int) QueueCounts {
	total := 0
	for _, n := range tally {
		total += n
	}
	return QueueCounts{
		Todo:    tally[task.StatusTodo],
		Doing:   tally[task.StatusDoing],
		Done:    tally[task.StatusDone],
		Failed:  tally[task.StatusFailed],
		Deleted: tally[task.StatusDeleted],
		Total:   total,
	}
}

type statusDoc struct {
	QueueCounts
	Tasks   statusTasksJSON    `json:"tasks"`
	Blocked *[]blockedTaskJSON `json:"blocked,omitempty"`
	Health  task.QueueHealth   `json:"health"`
}

type takeSummaryQueue struct {
	QueueCounts
	ReadyRemaining int `json:"ready_remaining"`
}

type takePreviewDoc struct {
	Claimed bool             `json:"claimed"`
	Tasks   []boundedTask    `json:"tasks"`
	Queue   takeSummaryQueue `json:"queue"`
}

type takeSummaryDoc struct {
	Claimed bool             `json:"claimed"`
	Task    boundedTask      `json:"task"`
	Queue   takeSummaryQueue `json:"queue"`
}

func statusListJSON(tasks []task.Task) []boundedTask {
	return boundTasks(tasks, maxListBodyRunes)
}

func statusDoingListJSON(tasks []task.Task, now time.Time) []boundedTask {
	bounded := make([]boundedTask, 0, len(tasks))
	for _, t := range tasks {
		bounded = append(bounded, boundTaskWithClaim(t, maxListBodyRunes, now, unleasedStaleAfter))
	}
	return bounded
}

func blockedListJSON(blocked []task.BlockedTask) []blockedTaskJSON {
	out := make([]blockedTaskJSON, 0, len(blocked))
	for _, item := range blocked {
		out = append(out, blockedTaskJSON{
			Task:     boundTask(item.Task, maxListBodyRunes),
			Blockers: item.Blockers,
		})
	}
	return out
}

func writeStatusJSON(w io.Writer, data statusData) error {
	var blockedDoc *[]blockedTaskJSON
	if data.blocked != nil {
		value := blockedListJSON(data.blocked)
		blockedDoc = &value
	}
	return WriteJSONLine(w, statusDoc{
		QueueCounts: NewQueueCounts(data.tally),
		Tasks: statusTasksJSON{
			Todo:  statusListJSON(data.todo),
			Doing: statusDoingListJSON(data.doing, data.now),
		},
		Blocked: blockedDoc,
		Health:  data.health,
	}, "status")
}
