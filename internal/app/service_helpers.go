package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dotcommander/afk/internal/task"
)

// recentPaths collects up to limit distinct non-empty CWDs from the most
// recently created tasks, then returns them sorted alphabetically.
func recentPaths(tasks []task.Task, limit int) []string {
	ordered := append([]task.Task(nil), tasks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Created > ordered[j].Created })
	seen := make(map[string]bool)
	var out []string
	for _, t := range ordered {
		if t.CWD == "" || seen[t.CWD] {
			continue
		}
		seen[t.CWD] = true
		out = append(out, t.CWD)
		if len(out) == limit {
			break
		}
	}
	sort.Strings(out)
	return out
}

func filterByStatus(tasks []task.Task, status string) []task.Task {
	status = strings.TrimSpace(status)
	if status == "" {
		return filterVisible(tasks)
	}
	if status == statusFilterAll {
		return append([]task.Task(nil), tasks...)
	}
	parsed, ok := task.ParseStatus(status)
	if !ok {
		return nil
	}
	out := tasks[:0:0]
	for _, t := range tasks {
		if task.NormalizeStatus(t.Status) == parsed {
			out = append(out, t)
		}
	}
	return out
}

func filterVisible(tasks []task.Task) []task.Task {
	out := tasks[:0:0]
	for _, t := range tasks {
		if task.VisibleStatus(t.Status) {
			out = append(out, t)
		}
	}
	return out
}

func validateStatusFilter(status string) error {
	status = strings.TrimSpace(status)
	if status == "" || status == statusFilterAll {
		return nil
	}
	if _, ok := task.ParseStatus(status); !ok {
		return task.ErrInvalidStatus
	}
	return nil
}

func taskMatches(t task.Task, query string) bool {
	fields := []string{
		t.ID,
		string(task.NormalizeStatus(t.Status)),
		t.Body,
		string(t.Priority),
		t.CWD,
		t.Source,
		t.Agent,
		t.GroupID,
		t.ResourceKey,
		t.Error,
		strings.Join(t.Tags, " "),
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func formatTime(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}

func leaseExpires(now time.Time, lease time.Duration) time.Time {
	if lease <= 0 {
		return time.Time{}
	}
	return now.Add(lease)
}

func workerOrDefault(workerID string) string {
	if workerID != "" {
		return workerID
	}
	return defaultWorkerID()
}

// AddDefaults are inferred from a task working directory.
type AddDefaults struct {
	RepoTag     string
	ResourceKey string
}

// InferAddDefaults returns repo-scoped metadata for tasks rooted in a git
// checkout. Callers decide whether explicit user-provided values override it.
func InferAddDefaults(cwd string) AddDefaults {
	if cwd == "" {
		return AddDefaults{}
	}
	root, ok := findGitRoot(cwd)
	if !ok {
		return AddDefaults{}
	}
	defaults := AddDefaults{ResourceKey: "repo:" + root}
	if name := filepath.Base(root); name != "" && name != "." && name != string(filepath.Separator) {
		defaults.RepoTag = "repo:" + name
	}
	return defaults
}

func findGitRoot(cwd string) (string, bool) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false
		}
		abs = parent
	}
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}
