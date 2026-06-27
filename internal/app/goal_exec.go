package app

// Goal setup/audit agent execution helpers: prompt rendering, contract/decision
// parsing, and the runAgent-backed exec seams (runSetupAgent/runAuditAgent) that
// tests substitute. Reuses runAgent in loop_exec.go as the single argv/exec
// authority.

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"strings"
	"text/template"
)

// maxObjectiveLen caps the free-text objective. Objectives longer than this are
// rejected before prompt rendering.
const maxObjectiveLen = 4000

// runSetupAgent is the exec seam for the setup agent. It defaults to runAgent
// (the single argv/exec authority in loop_exec.go) and is a package var so
// tests can substitute a stub child process.
var runSetupAgent = runAgent

// runAuditAgent is the exec seam for the audit agent. Like runSetupAgent it
// defaults to runAgent (the single argv/exec authority in loop_exec.go) and is
// a package var so tests can substitute a stub child process.
var runAuditAgent = runAgent

// AuditResult is the outcome of an independent completion audit.
type AuditResult struct {
	Approved    bool   `json:"approved"`
	Disapproved bool   `json:"disapproved"`
	Output      string `json:"output"` // full captured stdout
	Error       string `json:"error,omitempty"`
}

// parseAuditDecision scans the trailing 2000 bytes of output for terminal
// markers. <disapproved/> wins when both appear (fail-safe), and the absence of
// both markers reads as not-approved.
func parseAuditDecision(output string) (approved, disapproved bool) {
	tail := output
	if len(tail) > 2000 {
		tail = tail[len(tail)-2000:]
	}
	disapproved = strings.Contains(tail, "<disapproved/>")
	approved = strings.Contains(tail, "<approved/>") && !disapproved
	return approved, disapproved
}

// buildSetupPrompt renders cfg.SetupPromptTemplate with the HTML-escaped
// objective injected as {{.EscapedObjective}}. The objective is always treated
// as untrusted data — escaping it before interpolation prevents tag-injection
// into the surrounding prompt structure.
func buildSetupPrompt(cfg GoalConfig, objective string) (string, error) {
	if len([]rune(objective)) > maxObjectiveLen {
		return "", ErrGoalObjectiveTooLong
	}
	tmpl, err := template.New("setup").Parse(cfg.SetupPromptTemplate)
	if err != nil {
		return "", err
	}
	data := struct{ EscapedObjective string }{EscapedObjective: html.EscapeString(objective)}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// parseGoalContract extracts the <contract>...</contract> block from captured
// setup-agent output and decodes it into a GoalContract. Returns
// ErrGoalContractMissing when no block is present and ErrGoalNoTasks when the
// decoded contract carries no tasks.
func parseGoalContract(output string) (GoalContract, error) {
	const openTag, closeTag = "<contract>", "</contract>"
	start := strings.Index(output, openTag)
	end := strings.Index(output, closeTag)
	if start < 0 || end < 0 || end < start {
		return GoalContract{}, ErrGoalContractMissing
	}
	body := strings.TrimSpace(output[start+len(openTag) : end])
	var c GoalContract
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return GoalContract{}, err
	}
	if len(c.Tasks) == 0 {
		return GoalContract{}, ErrGoalNoTasks
	}
	return c, nil
}

// runGoalSetupAgent shells out to cfg.SetupCommand with prompt, capturing
// stdout into a buffer (so contract parsing happens atomically after exit)
// while forwarding child stderr to errw. It reuses runAgent's argv/exec
// mechanism — the single source of truth for command construction.
func runGoalSetupAgent(ctx context.Context, cfg GoalConfig, prompt string, out, errw io.Writer) (string, error) {
	var buf bytes.Buffer
	if err := runSetupAgent(ctx, cfg.SetupCommand, prompt, cfg.SetupTimeout, &buf, errw); err != nil {
		return "", err
	}
	captured := buf.String()
	if out != nil {
		_, _ = io.WriteString(out, captured)
	}
	return captured, nil
}

// buildAuditPrompt renders cfg.AuditPromptTemplate with the HTML-escaped
// objective and completion note. Both objective and completion note are
// untrusted data — escaping them before interpolation prevents tag-injection
// into the surrounding prompt structure (the auditor must not be steered by
// content under review).
func buildAuditPrompt(cfg GoalConfig, objective, completionNote string) (string, error) {
	tmpl, err := template.New("audit").Parse(cfg.AuditPromptTemplate)
	if err != nil {
		return "", err
	}
	data := struct {
		EscapedObjective string
		CompletionNote   string
	}{
		EscapedObjective: html.EscapeString(objective),
		CompletionNote:   html.EscapeString(completionNote),
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// runGoalAuditAgent shells out to cfg.AuditCommand with prompt, capturing stdout
// into a buffer (so decision parsing happens atomically after exit) while
// forwarding child stderr to errw. It reuses runAgent's argv/exec mechanism via
// the runAuditAgent seam — the single source of truth for command construction.
func runGoalAuditAgent(ctx context.Context, cfg GoalConfig, prompt string, out, errw io.Writer) (string, error) {
	var buf bytes.Buffer
	if err := runAuditAgent(ctx, cfg.AuditCommand, prompt, cfg.AuditTimeout, &buf, errw); err != nil {
		return buf.String(), err
	}
	captured := buf.String()
	if out != nil {
		_, _ = io.WriteString(out, captured)
	}
	return captured, nil
}
