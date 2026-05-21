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
	OmittedEvents         int
	OmittedAttempts       int
	Events                []eventView
	Attempts              []attemptView
	DoneCmd, FailCmd      string
}

type eventView struct {
	At, Type, Message string
}

type attemptView struct {
	ID                               int64
	Status, Started, Finished, Error string
}

type loopView struct {
	SQLitePath, PopCmd, StatusCmd, LsPendingCmd, LsWorkingCmd   string
	DoneCmd, FailCmd, ExplainCmd, RecoverFailCmd, RecoverAddCmd string
}

// joinCmd joins exe and args into a single command string.
func joinCmd(exe, args string) string {
	if args == "" {
		return exe
	}
	return exe + " " + args
}

// Task renders a focused execution prompt for one task.
func Task(exe string, t task.Task, events []task.Event, attempts []task.Attempt) string {
	if exe == "" {
		exe = "afk"
	}

	var meta []string
	if t.Priority != "" {
		meta = append(meta, "Priority: `"+t.Priority+"`")
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

	v := taskView{
		ID:              t.ID,
		Status:          string(t.Status),
		Body:            truncatePrompt(t.Body, maxPromptBodyRunes),
		CWD:             t.CWD,
		MetaLines:       meta,
		HasHistory:      len(events) > 0 || len(attempts) > 0,
		OmittedEvents:   omE,
		OmittedAttempts: omA,
		Events:          evs,
		Attempts:        atts,
		DoneCmd:         joinCmd(exe, "done "+t.ID),
		FailCmd:         joinCmd(exe, "fail "+t.ID+` "<one-line reason>"`),
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
		PopCmd:         joinCmd(exe, "pop"),
		StatusCmd:      joinCmd(exe, "status"),
		LsPendingCmd:   joinCmd(exe, "ls --status pending --json"),
		LsWorkingCmd:   joinCmd(exe, "ls --status working --json"),
		DoneCmd:        joinCmd(exe, "done <id>"),
		FailCmd:        joinCmd(exe, `fail <id> "<one-line reason>"`),
		ExplainCmd:     joinCmd(exe, "explain <id>"),
		RecoverFailCmd: joinCmd(exe, `fail <id> "orphaned working claim"`),
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
