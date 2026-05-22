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

- `/Users/vampire/.codex/skills/afk-queue-worker/SKILL.md`
- `/Users/vampire/.codex/skills/afk-queue-worker/references/commands.md`
- `/Users/vampire/.codex/skills/afk-queue-worker/references/discovery-checklist.md`
- `/Users/vampire/.codex/skills/afk-queue-worker/agents/openai.yaml`
- `/Users/vampire/.codex/skills/afk-discovery/SKILL.md`
- `/Users/vampire/.codex/skills/afk-discovery/agents/openai.yaml`
- AFK planning helpers such as `bulk-afk-planner` and `project-manager` if
  queue, import, add, or planning contracts changed.

### Claude AFK surfaces

- `/Users/vampire/.claude/agents/afk-agent.md`
- `/Users/vampire/.claude/commands/afk.md` if present
- `/Users/vampire/.claude/skills/afk-loop/SKILL.md`
- historical or related AFK skills such as `afk-tasks` if present
- `/Users/vampire/.claude/hooks/afk-perpetuate.sh`
- `/Users/vampire/.claude/kb/claude-code/afk-task-authoring.md`
- `/Users/vampire/.claude/CLAUDE.md` if it documents AFK behavior

### Pi AFK surfaces

- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/features/dc-afk/index.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/features/dc-afk/lib/command.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/features/dc-afk/lib/tools.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/features/dc-afk/lib/afk-cli.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/features/dc-afk/index.test.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/lib/registrations/dc-afk.ts`
- `/Users/vampire/go/src/pi-extensions/extensions/dc-work/README.md`
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
