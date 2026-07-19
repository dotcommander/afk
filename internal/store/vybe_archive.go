package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func readVybeArchive(ctx context.Context, root string) (vybeImportData, error) {
	if err := ctx.Err(); err != nil {
		return vybeImportData{}, err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return vybeImportData{}, fmt.Errorf("resolve vybe archive: %w", err)
	}
	var out vybeImportData
	if err := readJSONFile(ctx, filepath.Join(root, "manifest.json"), &out.manifest); err != nil {
		return out, err
	}
	if err := validateVybeManifest(out.manifest); err != nil {
		return out, err
	}
	if err := readVybeRows(ctx, root, &out); err != nil {
		return out, err
	}
	if err := validateVybeRowCounts(out); err != nil {
		return out, err
	}
	if err := validateAndNormalizeVybeData(ctx, &out); err != nil {
		return out, err
	}
	sort.Slice(out.events, func(i, j int) bool { return out.events[i].ID < out.events[j].ID })
	return out, nil
}

func validateVybeManifest(manifest vybeManifest) error {
	if manifest.FormatVersion != vybeArchiveFormat {
		return fmt.Errorf("unsupported vybe archive format %q", manifest.FormatVersion)
	}
	if manifest.IntegrityCheck != "ok" || !manifest.ReferentialIntegrity.OK {
		return fmt.Errorf("vybe archive integrity checks did not pass")
	}
	if err := requireCanonicalID("source_sha256", manifest.SourceSHA256, false); err != nil {
		return fmt.Errorf("vybe archive: %w", err)
	}
	if err := requireCanonicalID("cutover_id", manifest.CutoverID, false); err != nil {
		return fmt.Errorf("vybe archive: %w", err)
	}
	for _, name := range []string{"projects", vybeTableTasks, vybeTableEvents, vybeTableMemory, vybeTableArtifacts, "agent_state", "idempotency"} {
		count, ok := manifest.RowCounts[name]
		if !ok {
			return fmt.Errorf("vybe archive row_counts is missing %q", name)
		}
		if count < 0 {
			return fmt.Errorf("vybe archive row_counts %q is negative", name)
		}
	}
	return nil
}

func readVybeRows(ctx context.Context, root string, out *vybeImportData) error {
	readers := []func() error{
		func() error { return readJSONL(ctx, filepath.Join(root, "tasks.jsonl"), &out.tasks) },
		func() error { return readJSONL(ctx, filepath.Join(root, "events.jsonl"), &out.events) },
		func() error { return readJSONL(ctx, filepath.Join(root, "memories.jsonl"), &out.memories) },
		func() error { return readJSONL(ctx, filepath.Join(root, "artifacts.jsonl"), &out.artifacts) },
	}
	for _, read := range readers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
	}
	return nil
}

func validateVybeRowCounts(data vybeImportData) error {
	checks := map[string]int{
		vybeTableTasks:     len(data.tasks),
		vybeTableEvents:    len(data.events),
		vybeTableMemory:    len(data.memories),
		vybeTableArtifacts: len(data.artifacts),
	}
	for name, count := range checks {
		if int64(count) != data.manifest.RowCounts[name] {
			return fmt.Errorf("vybe %s count mismatch: manifest=%d file=%d", name, data.manifest.RowCounts[name], count)
		}
	}
	return nil
}

func readJSONFile(ctx context.Context, path string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close() //nolint:errcheck // read-only input
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", filepath.Base(path))
		}
		return fmt.Errorf("decode %s trailing data: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSONL[T any](ctx context.Context, path string, out *[]T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close() //nolint:errcheck // read-only input
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line++
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return fmt.Errorf("decode %s line %d: %w", filepath.Base(path), line, err)
		}
		*out = append(*out, row)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return ctx.Err()
}
