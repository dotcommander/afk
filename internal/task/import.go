// Package task — import-batch types for `afk import`.
//
// ImportTask/ImportDoc/ImportResult are the wire format consumed by the
// `afk import` cobra command and produced by /afk spec. Flat schema by
// design: no version field, no defaults block, no wrapper. Slug is the
// user-chosen stable identifier; BlockedBy is a list of slugs in the
// same batch (slug→ID resolution happens in Service.Import).
package task

// ImportTask is one task in an import batch. Slug must be present and unique
// within the batch. Body must be non-empty. All other fields are optional.
type ImportTask struct {
	Slug        string   `json:"slug"`
	Body        string   `json:"body"`
	CWD         string   `json:"cwd,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Source      string   `json:"source,omitempty"`
	GroupID     string   `json:"group_id,omitempty"`
	ResourceKey string   `json:"resource_key,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
}

// ImportDoc is the stdin envelope for `afk import`.
type ImportDoc struct {
	Tasks []ImportTask `json:"tasks"`
}

// ImportResult is one stdout NDJSON line written per imported task.
type ImportResult struct {
	Slug string `json:"slug"`
	ID   string `json:"id"`
}
