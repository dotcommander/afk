package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dotcommander/afk/internal/output"
	"github.com/dotcommander/afk/internal/store"
)

const vybeReportEvents = "events"

type ImportCmd struct {
	Vybe ImportVybeCmd `cmd:"" help:"Reconcile and import operational state from a frozen Vybe archive."`
}
type ImportVybeCmd struct {
	Source string `arg:"" required:"" help:"Vybe archive directory."`
	DryRun bool   `name:"dry-run" help:"Validate and reconcile, then roll back."`
	Apply  bool   `help:"Apply the import atomically."`
	JSON   bool   `help:"Emit JSON reconciliation output."`
}

func (c *ImportVybeCmd) Run(d *Deps, ctx context.Context) error {
	if c.DryRun == c.Apply {
		return fmt.Errorf("exactly one of --dry-run or --apply is required")
	}
	report, err := d.Service.ImportVybe(ctx, c.Source, c.Apply)
	if err != nil {
		return err
	}
	if c.JSON {
		return output.WriteJSONLine(d.Stdout, report, "import vybe")
	}
	_, err = fmt.Fprintln(d.Stdout, formatVybeImportReport(report))
	return err
}

func formatVybeImportReport(report store.VybeImportReport) string {
	mode := "apply"
	if report.DryRun {
		mode = "dry-run"
	}
	imported := map[string]int64{
		"artifacts":      int64(report.ImportedArtifacts),
		"checkpoints":    int64(report.ImportedCheckpoints),
		vybeReportEvents: int64(report.ImportedEvents),
		cmdTasks:         int64(report.ImportedTasks),
	}
	archivedOnly := withCountKeys(report.ArchivedOnly, "agent_state", "artifacts", vybeReportEvents, "idempotency", "memory", "projects")
	archivedOrphans := withCountKeys(report.ArchivedOrphans, "artifacts", vybeReportEvents, "memory")
	return fmt.Sprintf(
		"vybe import source_sha256=%s cutover_id=%s mode=%s replay=%t source_rows=%s imported=%s archived_only=%s archived_orphans=%s",
		report.SourceSHA256,
		report.CutoverID,
		mode,
		report.AlreadyImported,
		formatCountMap(report.SourceRows),
		formatCountMap(imported),
		formatCountMap(archivedOnly),
		formatCountMap(archivedOrphans),
	)
}

func withCountKeys(counts map[string]int64, keys ...string) map[string]int64 {
	complete := make(map[string]int64, len(counts)+len(keys))
	for key, count := range counts {
		complete[key] = count
	}
	for _, key := range keys {
		if _, ok := complete[key]; !ok {
			complete[key] = 0
		}
	}
	return complete
}

func formatCountMap(counts map[string]int64) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&out, "%s=%d", key, counts[key])
	}
	out.WriteByte('}')
	return out.String()
}
