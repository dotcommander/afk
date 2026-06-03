package app

import (
	"strings"
	"testing"
)

// TestParseAuditDecision exercises parseAuditDecision per Verification Surface
// §3. Behavior is implemented in Phase E; these tests encode the contract and
// may fail until then.
func TestParseAuditDecision(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		input           string
		wantApproved    bool
		wantDisapproved bool
	}{
		{"approved", "work done\n<approved/>", true, false},
		{"disapproved", "not done\n<disapproved/>", false, true},
		{"both -> disapproved wins", "x\n<approved/>\n<disapproved/>", false, true},
		{"no markers", "no markers here", false, false},
		{
			name:            "markers only in trailing 2000 bytes",
			input:           "<approved/>" + strings.Repeat("x", 3000),
			wantApproved:    false,
			wantDisapproved: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			approved, disapproved := parseAuditDecision(tc.input)
			if approved != tc.wantApproved || disapproved != tc.wantDisapproved {
				t.Fatalf("parseAuditDecision = (%v, %v), want (%v, %v)",
					approved, disapproved, tc.wantApproved, tc.wantDisapproved)
			}
		})
	}
}

// TestBuildSetupPrompt exercises buildSetupPrompt per Verification Surface §4.
// Behavior is implemented in Phase D; these tests encode the contract and may
// fail until then.
func TestBuildSetupPrompt(t *testing.T) {
	t.Parallel()

	cfg := GoalConfig{
		SetupPromptTemplate: `<untrusted_goal_intent>{{.EscapedObjective}}</untrusted_goal_intent>`,
	}
	got, err := buildSetupPrompt(cfg, `add <script>export to "report"`)
	if err != nil {
		t.Fatalf("buildSetupPrompt error: %v", err)
	}
	if !strings.Contains(got, "<untrusted_goal_intent") {
		t.Fatalf("prompt missing <untrusted_goal_intent wrapper: %q", got)
	}
	// HTML-escaped objective must appear inside the wrapper.
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("prompt missing HTML-escaped objective: %q", got)
	}
}

// TestParseGoalContract exercises parseGoalContract per Verification Surface §4.
// Behavior is implemented in Phase D; these tests encode the contract and may
// fail until then.
func TestParseGoalContract(t *testing.T) {
	t.Parallel()

	const out = `preamble
<contract>
{"outcome":"CSV export works","done_criteria":["report --csv emits valid CSV"],"must_do":["add flag"],"avoid":["breaking JSON output"],"philosophy":"minimal","tasks":["add --csv flag","write encoder"]}
</contract>
trailer`

	c, err := parseGoalContract(out)
	if err != nil {
		t.Fatalf("parseGoalContract error: %v", err)
	}
	if c.Outcome != "CSV export works" {
		t.Fatalf("Outcome = %q, want %q", c.Outcome, "CSV export works")
	}
	if len(c.Tasks) != 2 {
		t.Fatalf("len(Tasks) = %d, want 2", len(c.Tasks))
	}
}
