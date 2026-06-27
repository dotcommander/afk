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
	Gates           []task.Gate    `json:"gates"`
	Events          []task.Event   `json:"events"`
	Attempts        []task.Attempt `json:"attempts"`
	EventsOmitted   int            `json:"events_omitted,omitzero"`
	AttemptsOmitted int            `json:"attempts_omitted,omitzero"`
}

type explainData struct {
	task            task.Task
	gates           []task.Gate
	events          []task.Event
	attempts        []task.Attempt
	eventsOmitted   int
	attemptsOmitted int
}

// WriteExplain renders a task and its durable lifecycle history.
func WriteExplain(w io.Writer, t task.Task, events []task.Event, attempts []task.Attempt, asJSON bool) error {
	return WriteExplainWithGates(w, t, nil, events, attempts, asJSON)
}

// WriteExplainWithGates renders a task, its gates, and its durable lifecycle history.
func WriteExplainWithGates(w io.Writer, t task.Task, gates []task.Gate, events []task.Event, attempts []task.Attempt, asJSON bool) error {
	visibleEvents, omittedEvents := limitEvents(events)
	visibleAttempts, omittedAttempts := limitAttempts(attempts)
	data := explainData{
		task:            t,
		gates:           gates,
		events:          visibleEvents,
		attempts:        visibleAttempts,
		eventsOmitted:   omittedEvents,
		attemptsOmitted: omittedAttempts,
	}
	if asJSON {
		return writeExplainJSON(w, data)
	}
	return writeExplainText(w, data)
}

func writeExplainJSON(w io.Writer, data explainData) error {
	return WriteJSONLine(w, explainDoc{
		Task:            boundTask(data.task, maxDetailBodyRunes),
		Gates:           data.gates,
		Events:          boundEvents(data.events),
		Attempts:        boundAttempts(data.attempts),
		EventsOmitted:   data.eventsOmitted,
		AttemptsOmitted: data.attemptsOmitted,
	}, "explain")
}

func writeExplainText(w io.Writer, data explainData) error {
	if err := writeTaskDetail(w, data.task); err != nil {
		return err
	}
	if err := writeExplainGates(w, data.gates); err != nil {
		return err
	}
	if err := writeExplainEvents(w, data.events, data.eventsOmitted); err != nil {
		return err
	}
	return writeExplainAttempts(w, data.attempts, data.attemptsOmitted)
}

func writeExplainGates(w io.Writer, gates []task.Gate) error {
	return writeExplainSection(w, "Gates:", len(gates), 0, "gates", func() error {
		for _, gate := range gates {
			if err := writeExplainGate(w, gate); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeExplainGate(w io.Writer, gate task.Gate) error {
	state := "unsatisfied"
	if gate.Satisfied {
		state = "satisfied"
	}
	if _, err := fmt.Fprintf(w, "  %s  %s\n", gate.Name, state); err != nil {
		return fmt.Errorf("explain: write: %w", err)
	}
	return nil
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
