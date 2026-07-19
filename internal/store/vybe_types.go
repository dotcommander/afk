package store

import "encoding/json"

const (
	vybeArchiveFormat  = "vybe-archive-v1"
	vybeTableTasks     = "tasks"
	vybeTableEvents    = "events"
	vybeTableMemory    = "memory"
	vybeTableArtifacts = "artifacts"
	vybeScopeTask      = "task"
	vybeStatusTodo     = "todo"
	vybeStatusDoing    = "doing"
)

// VybeImportOptions selects a frozen archive and dry-run/apply behavior.
type VybeImportOptions struct {
	Source string
	Apply  bool
}

// VybeImportReport reconciles active imports with archive-only rows.
type VybeImportReport struct {
	SourceSHA256        string           `json:"source_sha256"`
	CutoverID           string           `json:"cutover_id"`
	DryRun              bool             `json:"dry_run"`
	AlreadyImported     bool             `json:"already_imported"`
	SourceRows          map[string]int64 `json:"source_rows"`
	ImportedTasks       int              `json:"imported_tasks"`
	ImportedEvents      int              `json:"imported_events"`
	ImportedCheckpoints int              `json:"imported_checkpoints"`
	ImportedArtifacts   int              `json:"imported_artifacts"`
	ArchivedOnly        map[string]int64 `json:"archived_only"`
	ArchivedOrphans     map[string]int64 `json:"archived_orphans"`
}

type vybeManifest struct {
	FormatVersion        string           `json:"format_version"`
	SourceSHA256         string           `json:"source_sha256"`
	CutoverID            string           `json:"cutover_id"`
	IntegrityCheck       string           `json:"integrity_check"`
	RowCounts            map[string]int64 `json:"row_counts"`
	ReferentialIntegrity struct {
		OK bool `json:"ok"`
	} `json:"referential_integrity"`
}

type vybeTask struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	Version       int64  `json:"version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ProjectID     string `json:"project_id"`
	Priority      int    `json:"priority"`
	BlockedReason string `json:"blocked_reason"`
}

type vybeEvent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	AgentName string          `json:"agent_name"`
	TaskID    string          `json:"task_id"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt string          `json:"created_at"`
}

type vybeMemory struct {
	ID            int64           `json:"id"`
	Key           string          `json:"key"`
	Value         json.RawMessage `json:"value"`
	ValueType     string          `json:"value_type"`
	Scope         string          `json:"scope"`
	ScopeID       string          `json:"scope_id"`
	SourceEventID *int64          `json:"source_event_id"`
	SourceTaskID  string          `json:"source_task_id"`
	CreatedAt     string          `json:"created_at"`
}

type vybeArtifact struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	EventID     int64  `json:"event_id"`
	FilePath    string `json:"file_path"`
	ContentType string `json:"content_type"`
	ProjectID   string `json:"project_id"`
	CreatedAt   string `json:"created_at"`
}

type vybeImportData struct {
	manifest  vybeManifest
	tasks     []vybeTask
	events    []vybeEvent
	memories  []vybeMemory
	artifacts []vybeArtifact
}

// ImportVybeArchive validates a frozen Vybe export before opening the write
// transaction. Apply writes every selected record and the import receipt in
// one transaction; dry-run always rolls the transaction back.
