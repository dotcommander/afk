# Quality improvement workflow

Use this workflow when asked to improve the repo without a preselected bug.
The goal is ten measurable improvements that strengthen AFK's purpose: a local
SQLite task queue with reliable lifecycle, scheduling, CLI, prompt, and web
contracts for coding agents.

## Purpose first

1. Re-read `CLAUDE.md`, `AGENTS.md`, and `README.md`.
2. State the repo purpose in one sentence.
3. Pick improvement goals that protect that purpose:
   - queue correctness: lifecycle, dependencies, leases, resource locks
   - stable agent contract: CLI help, JSON output, prompt wording
   - operator safety: bounded input/output, clear errors, no stale verbs
   - repeatability: one-command verification and focused regression tests

## Baseline

Run and record:

```sh
go test ./...
go vet ./...
go test -cover ./...
just verify
```

If a baseline fails, the first improvement is a narrow fix for the failing
gate. Do not count broad diagnosis as an improvement.

## Select ten improvements

For each candidate, require all of:

- Evidence: exact file, command output, test gap, or stale contract.
- Scope: one package, command family, document, or helper.
- Metric: a test added, warning removed, command added, error made explicit,
  stale wording eliminated, or duplicated behavior collapsed.
- Verification: exact command that proves it.

Reject candidates that are only style, naming, dependency drift, or TODO text
without current corroboration.

## Execute

Work in small slices:

1. Add or update the focused test first when practical.
2. Make the smallest code/doc change that satisfies it.
3. Run the narrow package test.
4. Repeat until ten improvements are complete.
5. Run `just verify`.
6. If CLI behavior, help, prompt, or JSON shape changed, run `just install` and
   probe `/path/to/project/go/bin/afk --version` plus the affected help/command.

## Report

Close with:

- repo purpose
- ten improvements and their metric
- commands run
- any residual risks or follow-ups
