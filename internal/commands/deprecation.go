package commands

import (
	"fmt"
	"io"
)

func warnDeprecated(w io.Writer, old, replacement string) error {
	if _, err := fmt.Fprintf(w, "warning: %s is deprecated; use %s instead.\n", old, replacement); err != nil {
		return fmt.Errorf("write deprecation warning: %w", err)
	}
	return nil
}
