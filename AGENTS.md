# AGENTS.md

This repository's detailed build, architecture, and coding guidance lives in
`CLAUDE.md`. Read it before making AFK changes.

## AFK surface update checklist

When AFK command behavior, JSON shape, prompt wording, or lifecycle semantics
change, update and verify every affected agent/tool surface, not just this repo.
Treat installed `afk prompt` and `afk <cmd> --help` as the runtime contract,
then propagate wording to Codex, Claude, and Pi surfaces.

### Source of truth in this repo

- `internal/commands/*`
- `internal/output/*`
- `internal/prompt/*.tmpl.md`
- `internal/commands/discover_stub.md`
- `README.md`
- `docs/*.md`
- `CLAUDE.md`
- command/help tests under `internal/commands`, `internal/prompt`, and `cmd/afk`

### Runtime verification

- Run `go test ./...`.
- Run `go vet ./...`.
- Run `just install`.
- Verify `/Users/vampire/go/bin/afk ...`, not only source-tree behavior.

### Codex AFK surfaces

- `/Users/vampire/.codex/skills/afk-operational-workflows/SKILL.md`
- `/Users/vampire/.codex/skills/afk-operational-workflows/references/legacy/afk-queue-worker/references/legacy-workflow.md`
- `/Users/vampire/.codex/skills/afk-operational-workflows/references/legacy/afk-queue-worker/workflows/*.md`

### Claude AFK surfaces

- `/Users/vampire/.claude/kb/claude-code/afk-task-authoring.md`

### Pi AFK surfaces

- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/features/afk/index.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/features/afk/lib/command.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/features/afk/lib/tools.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/features/afk/lib/afk-cli.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/features/afk/index.test.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/lib/work/registrations/index.ts`
- `/Users/vampire/code/ts/pi-extensions/extensions/dc-app/lib/knowledge/skill-vault/afk-queue-worker/SKILL.md`
- `/Users/vampire/code/ts/pi-extensions/README.md`
- relevant Pi smoke tests and changelog entries

### Search patterns

After every AFK behavior change, search related repos and local agent surfaces
for:

- `afk take`
- `afk set`
- `afk prompt`
- `take --dry-run`
- `body_truncated`
- `--summary`
- `--full`
- `--note`
- `--note-file`
- `afk pop`
- `afk done`
- `afk fail`
- `afk ready`
- `afk run`
- `HITL`, `approval`, and `permission` when autonomy wording changed
