package task

import (
	"errors"
	"fmt"
	"strings"
)

// RelationType names the kind of edge a Dependency records. The persisted
// form is the lowercase string; the Go-side type adds compile-time safety
// over plain string. Only RelationBlocks gates readiness.
type RelationType string

// Canonical relation values. The empty RelationType is treated as
// RelationBlocks for backward compatibility with pre-existing dependency rows.
const (
	RelationBlocks     RelationType = "blocks"
	RelationRelates    RelationType = "relates"
	RelationDuplicates RelationType = "duplicates"
	RelationParent     RelationType = "parent"
)

// ErrInvalidRelation reports an unknown relation type.
var ErrInvalidRelation = errors.New("invalid relation type")

// ParseRelationType canonicalizes user input (case- and whitespace-insensitive)
// into a RelationType value. Empty input defaults to RelationBlocks, preserving
// backward compatibility with existing dependency rows.
func ParseRelationType(s string) (RelationType, error) {
	normalized := strings.ToLower(strings.TrimSpace(s))
	switch RelationType(normalized) {
	case "":
		return RelationBlocks, nil
	case RelationBlocks, RelationRelates, RelationDuplicates, RelationParent:
		return RelationType(normalized), nil
	default:
		return "", fmt.Errorf("%q: %w", s, ErrInvalidRelation)
	}
}
