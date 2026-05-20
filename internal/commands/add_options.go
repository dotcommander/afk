package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/afk/internal/task"
)

func buildAddCommandOptions(input addCommandInput) (task.AddOptions, string, error) {
	dependsOnID, err := normalizeBlockedBy(input.blockedBy, input.after)
	if err != nil {
		return task.AddOptions{}, "", err
	}
	resolvedCWD, err := resolveAddCWD(input.cwd, input.noCWD)
	if err != nil {
		return task.AddOptions{}, "", err
	}
	defaults := inferAddDefaults(resolvedCWD)
	tags := input.tags
	if len(tags) == 0 && defaults.repoTag != "" {
		tags = []string{defaults.repoTag}
	}
	source := input.source
	if source == "" {
		source = "cli"
	}
	resourceKey := input.resourceKey
	if strings.EqualFold(strings.TrimSpace(resourceKey), "none") {
		resourceKey = ""
	} else if resourceKey == "" {
		resourceKey = defaults.resourceKey
	}
	return task.AddOptions{
		Body:        strings.Join(input.args, " "),
		Priority:    input.priority,
		Tags:        tags,
		CWD:         resolvedCWD,
		Source:      source,
		Agent:       input.agent,
		GroupID:     input.groupID,
		ResourceKey: resourceKey,
	}, dependsOnID, nil
}

func resolveAddCWD(cwd string, noCWD bool) (string, error) {
	if noCWD {
		return "", nil
	}
	if cwd != "" {
		resolvedCWD, err := filepath.Abs(cwd)
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		return resolvedCWD, nil
	}
	resolvedCWD, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return resolvedCWD, nil
}

type addDefaults struct {
	repoTag     string
	resourceKey string
}

func inferAddDefaults(cwd string) addDefaults {
	if cwd == "" {
		return addDefaults{}
	}
	root, ok := findGitRoot(cwd)
	if !ok {
		return addDefaults{}
	}
	defaults := addDefaults{resourceKey: "repo:" + root}
	if name := filepath.Base(root); name != "" && name != "." && name != string(filepath.Separator) {
		defaults.repoTag = "repo:" + name
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

func normalizeBlockedBy(blockedBy, after string) (string, error) {
	if blockedBy != "" && after != "" && blockedBy != after {
		return "", fmt.Errorf("--blocked-by and --after disagree")
	}
	value := blockedBy
	if value == "" {
		value = after
	}
	if value == "" || strings.EqualFold(value, "none") {
		return "", nil
	}
	return value, nil
}
