// Package prompt generates agent instruction prompts from current afk behavior.
package prompt

import (
	_ "embed"
	"os"
	"strings"
	"text/template"

	"github.com/dotcommander/afk/internal/task"
)

const (
	maxPromptBodyRunes    = 8000
	maxPromptHistoryItems = 50
	maxPromptMessageRunes = 1000
)

// LoopOptions controls loop prompt rendering.
type LoopOptions struct {
	ExecutablePath string
	SQLitePath     string
}

//go:embed loop.tmpl.md
var loopTmplText string

//go:embed task.tmpl.md
var taskTmplText string

var loopTmpl = template.Must(template.New("loop").Parse(loopTmplText))
var taskTmpl = template.Must(template.New("task").Parse(taskTmplText))

type taskView struct {
	ID, Status, Body, CWD string
	MetaLines             []string
	HasHistory            bool
	CanRetry              bool
	OmittedEvents         int
	OmittedAttempts       int
	Events                []eventView
	Attempts              []attemptView
	Gates                 []gateView
	Relations             []relationView
	DoneCmd, FailCmd      string
	RetryCmd              string
}

type gateView struct {
	Name      string
	Satisfied bool
}

type relationView struct {
	ID, Type string
}

type eventView struct {
	At, Type, Message string
}

type attemptView struct {
	ID                               int64
	Status, Started, Finished, Error string
}

type loopView struct {
	SQLitePath, PopCmd, PreviewCmd, StatusCmd, LsPendingCmd string
	LsWorkingCmd, DoneCmd, FailCmd, ExplainCmd              string
	RecoverFailCmd, RecoverAddCmd, RetryCmd                 string
}

// joinCmd joins exe and args into a single command string.
func joinCmd(exe, args string) string {
	if args == "" {
		return exe
	}
	return exe + " " + args
}

// Task renders a focused execution prompt for one task.
func taskMetaLines(t task.Task) []string {
	var meta []string
	if t.Stage != "" {
		meta = append(meta, "Stage: `"+t.Stage+"`")
	}
	if t.Priority != "" {
		meta = append(meta, "Priority: `"+string(t.Priority)+"`")
	}
	if len(t.Tags) > 0 {
		meta = append(meta, "Tags: `"+strings.Join(t.Tags, ", ")+"`")
	}
	if t.CWD != "" {
		meta = append(meta, "CWD: `"+t.CWD+"`")
	}
	if t.Source != "" {
		meta = append(meta, "Source: `"+t.Source+"`")
	}
	if t.Agent != "" {
		meta = append(meta, "Agent: `"+t.Agent+"`")
	}
	if t.GroupID != "" {
		meta = append(meta, "Group: `"+t.GroupID+"`")
	}
	if t.ResourceKey != "" {
		meta = append(meta, "Resource: `"+t.ResourceKey+"`")
	}
	return meta
}

// Task renders the focused single-task instruction prompt.
func Task(exe string, t task.Task, events []task.Event, attempts []task.Attempt, gates []task.Gate) string {
	if exe == "" {
		exe = "afk"
	}

	meta := taskMetaLines(t)

	vEvents, omE := limitPromptEvents(events)
	vAttempts, omA := limitPromptAttempts(attempts)

	evs := make([]eventView, len(vEvents))
	for i, e := range vEvents {
		evs[i] = eventView{
			At:      e.At,
			Type:    string(e.Type),
			Message: truncatePrompt(e.Message, maxPromptMessageRunes),
		}
	}

	atts := make([]attemptView, len(vAttempts))
	for i, a := range vAttempts {
		atts[i] = attemptView{
			ID:       a.ID,
			Status:   string(a.Status),
			Started:  a.Started,
			Finished: a.Finished,
			Error:    truncatePrompt(a.Error, maxPromptMessageRunes),
		}
	}

	gvs := make([]gateView, len(gates))
	for i, g := range gates {
		gvs[i] = gateView{Name: g.Name, Satisfied: g.Satisfied}
	}

	rvs := make([]relationView, len(t.Dependencies))
	for i, dep := range t.Dependencies {
		relType := dep.Type
		if relType == "" {
			relType = task.RelationBlocks
		}
		rvs[i] = relationView{ID: dep.DependsOnID, Type: string(relType)}
	}

	v := taskView{
		ID:              t.ID,
		Status:          string(t.Status),
		Body:            truncatePrompt(t.Body, maxPromptBodyRunes),
		CWD:             t.CWD,
		MetaLines:       meta,
		HasHistory:      len(events) > 0 || len(attempts) > 0,
		CanRetry:        task.NormalizeStatus(t.Status) == task.StatusFailed,
		OmittedEvents:   omE,
		OmittedAttempts: omA,
		Events:          evs,
		Attempts:        atts,
		Gates:           gvs,
		Relations:       rvs,
		DoneCmd:         joinCmd(exe, "set "+t.ID+` done --note "<verification evidence>"`),
		FailCmd:         joinCmd(exe, "set "+t.ID+` failed --note "<one-line reason>"`),
		RetryCmd:        joinCmd(exe, "retry "+t.ID+` --reason "<why retrying now>"`),
	}

	var b strings.Builder
	if err := taskTmpl.Execute(&b, v); err != nil {
		panic(err)
	}
	return b.String()
}

// Loop renders the loop-tick instruction prompt.
func Loop(opts LoopOptions) string {
	exe := promptExecutable(opts.ExecutablePath)
	sqlitePath := ""
	if opts.SQLitePath != "" {
		sqlitePath = tildeRelative(opts.SQLitePath)
	}

	v := loopView{
		SQLitePath:     sqlitePath,
		PopCmd:         joinCmd(exe, "take --worker <name> --lease 60m --summary"),
		PreviewCmd:     joinCmd(exe, "take --dry-run --summary --full --envelope --limit 5"),
		StatusCmd:      joinCmd(exe, "status --summary"),
		LsPendingCmd:   joinCmd(exe, "tasks --status todo --json"),
		LsWorkingCmd:   joinCmd(exe, "tasks --status doing --json"),
		DoneCmd:        joinCmd(exe, `set <id> done --note "<verification evidence>" --summary`),
		FailCmd:        joinCmd(exe, `set <id> failed --note "<one-line reason>" --summary`),
		RetryCmd:       joinCmd(exe, `retry <id> --reason "<why retrying now>"`),
		ExplainCmd:     joinCmd(exe, "task <id>"),
		RecoverFailCmd: joinCmd(exe, `set <id> failed --note "orphaned doing claim"`),
		RecoverAddCmd:  joinCmd(exe, `add "replacement task body, if still needed"`),
	}

	var b strings.Builder
	if err := loopTmpl.Execute(&b, v); err != nil {
		panic(err)
	}
	return b.String()
}

func promptExecutable(exe string) string {
	if exe == "" {
		return "afk"
	}
	return exe
}

// tildeRelative renders an absolute path under the user's home dir as a
// ~-relative path so generated prompts stay machine-agnostic. Paths outside
// the home dir (and any path when the home dir is unknown) pass through unchanged.
func tildeRelative(absPath string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(absPath, home+"/") {
		return absPath
	}
	return "~/" + absPath[len(home)+1:]
}

func limitPromptEvents(events []task.Event) ([]task.Event, int) {
	if len(events) <= maxPromptHistoryItems {
		return events, 0
	}
	return events[len(events)-maxPromptHistoryItems:], len(events) - maxPromptHistoryItems
}

func limitPromptAttempts(attempts []task.Attempt) ([]task.Attempt, int) {
	if len(attempts) <= maxPromptHistoryItems {
		return attempts, 0
	}
	return attempts[len(attempts)-maxPromptHistoryItems:], len(attempts) - maxPromptHistoryItems
}

func truncatePrompt(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
