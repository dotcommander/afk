package output

import (
	"fmt"
	"io"

	"github.com/dotcommander/afk/internal/task"
)

// This file renders `afk task` output — a task plus its durable lifecycle
// history (events and attempts) — in both JSON and text form. Shared bounding
// and limiting helpers live in output.go.

type explainDoc struct {
	Task            boundedTask    `json:"task"`
	Events          []task.Event   `json:"events"`
	Attempts        []task.Attempt `json:"attempts"`
	EventsOmitted   int            `json:"events_omitted,omitzero"`
	AttemptsOmitted int            `json:"attempts_omitted,omitzero"`
}

// WriteExplain renders a task and its durable lifecycle history.
func WriteExplain(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, asJSON bool) error {
	visibleEvents, omittedEvents := limitEvents(events)
	visibleAttempts, omittedAttempts := limitAttempts(attempts)
	if asJSON {
		return writeExplainJSON(w, t, visibleEvents, visibleAttempts, omittedEvents, omittedAttempts)
	}
	return writeExplainText(w, t, visibleEvents, visibleAttempts, omittedEvents, omittedAttempts)
}

func writeExplainJSON(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, omittedEvents, omittedAttempts int) error {
	return WriteJSONLine(w, explainDoc{
		Task:            boundTask(t, maxDetailBodyRunes),
		Events:          boundEvents(events),
		Attempts:        boundAttempts(attempts),
		EventsOmitted:   omittedEvents,
		AttemptsOmitted: omittedAttempts,
	}, "explain")
}

func writeExplainText(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, omittedEvents, omittedAttempts int) error {
	if err := writeTaskDetail(w, t); err != nil {
		return err
	}
	if err := writeExplainEvents(w, events, omittedEvents); err != nil {
		return err
	}
	return writeExplainAttempts(w, attempts, omittedAttempts)
}

func writeExplainEvents(w io.Writer, events []task.Event, omitted int) error {
	return writeExplainSection(w, "Events:", len(events), omitted, "events", func() error {
		for _, event := range boundEvents(events) {
			if err := writeExplainEvent(w, event); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeExplainEvent(w io.Writer, event task.Event) error {
	if _, err := fmt.Fprintf(w, "  %s  %s", event.At, event.Type); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if event.Message != "" {
		if _, err := fmt.Fprintf(w, "  %s", event.Message); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	return nil
}

func writeExplainAttempts(w io.Writer, attempts []task.Attempt, omitted int) error {
	return writeExplainSection(w, "Attempts:", len(attempts), omitted, "attempts", func() error {
		for _, attempt := range boundAttempts(attempts) {
			if err := writeExplainAttempt(w, attempt); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeExplainSection(w io.Writer, title string, count, omitted int, name string, writeRows func() error) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", title); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	if count == 0 {
		if _, err := fmt.Fprintln(w, "  none"); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if omitted > 0 {
		if _, err := fmt.Fprintf(w, "  ... %d older %s omitted by output limit\n", omitted, name); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	return writeRows()
}

func writeExplainAttempt(w io.Writer, attempt task.Attempt) error {
	if _, err := fmt.Fprintf(w, "  #%d  %s", attempt.ID, attempt.Status); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	fields := []string{
		fieldIfSet("started", attempt.Started),
		fieldIfSet("finished", attempt.Finished),
		fieldIfSet("error", attempt.Error),
		fieldIfSet("worker", attempt.WorkerID),
		fieldIfSet("agent", attempt.Agent),
	}
	for _, field := range fields {
		if field == "" {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s", field); err != nil {
			return fmt.Errorf("explain: write: %w", err)
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	return nil
}

func fieldIfSet(name, value string) string {
	if value == "" {
		return ""
	}
	return name + "=" + value
}
