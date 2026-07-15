package task

// Provenance identifies an immutable source record copied into AFK.
type Provenance struct {
	System  string `json:"system"`
	ID      string `json:"id"`
	EventID *int64 `json:"event_id,omitempty"`
}

// Checkpoint is task-scoped durable progress or imported task memory.
type Checkpoint struct {
	ID         int64      `json:"id"`
	TaskID     string     `json:"task_id"`
	Kind       string     `json:"kind"`
	Key        string     `json:"key,omitempty"`
	ValueJSON  string     `json:"value_json"`
	Provenance Provenance `json:"provenance"`
	CreatedAt  string     `json:"created_at"`
}

// Artifact is a task-owned output with immutable source metadata.
type Artifact struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	Path         string     `json:"path"`
	ContentType  string     `json:"content_type,omitempty"`
	MetadataJSON string     `json:"metadata_json,omitempty"`
	Provenance   Provenance `json:"provenance"`
	CreatedAt    string     `json:"created_at"`
}
