package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/afk/internal/app"
	"github.com/dotcommander/afk/internal/task"
)

func buildAddCommandOptions(input addCommandInput) (task.AddOptions, string, error) {
	dependsOnID := normalizeBlockedBy(input.blockedBy)
	resolvedCWD, err := resolveAddCWD(input.cwd, input.noCWD)
	if err != nil {
		return task.AddOptions{}, "", err
	}
	defaults := app.InferAddDefaults(resolvedCWD)
	tags := input.tags
	if len(tags) == 0 && defaults.RepoTag != "" {
		tags = []string{defaults.RepoTag}
	}
	source := input.source
	if source == "" {
		source = "cli"
	}
	resourceKey := input.resourceKey
	if strings.EqualFold(strings.TrimSpace(resourceKey), "none") {
		resourceKey = ""
	} else if resourceKey == "" {
		resourceKey = defaults.ResourceKey
	}
	return task.AddOptions{
		Body:        strings.Join(input.args, " "),
		Priority:    task.Priority(input.priority),
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

func normalizeBlockedBy(blockedBy string) string {
	if blockedBy == "" || strings.EqualFold(blockedBy, "none") {
		return ""
	}
	return blockedBy
}
