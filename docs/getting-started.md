# getting started

Add a task:

```sh
id=$(afk add "fix the failing queue test")
```

Inspect work:

```sh
afk status
afk tasks
afk find queue --json
afk task "$id"
```

Preview what an agent could claim:

```sh
afk take --dry-run --limit 5 --json --full
```

Claim and finish one task:

```sh
claimed=$(afk take --lease 30m --worker codex:1 --summary)
id=$(printf '%s\n' "$claimed" | jq -r .task.id)

# do the work
afk set "$id" done --note "verified" --worker codex:1 --summary
```

Record failure without losing history:

```sh
afk set "$id" failed --note "missing credentials" --worker codex:1 --summary
```

Hide obsolete work without deleting history:

```sh
afk set "$id" deleted --note "superseded by a narrower task"
```

Drive work automatically with the built-in worker-driver — it claims ready
tasks and runs an agent command per task:

```sh
afk loop --command 'claude -p {{.Prompt}}' --max-tasks 5
```

Configure the command once in `~/.config/afk/loop.yaml` so you can just run
`afk loop`. See [runner.md](runner.md) for how each iteration works and
[configuration.md](configuration.md) for the config file.

Compile a free-text objective into an approved, queued task chain:

```sh
afk goal --setup-command 'claude -p {{.Prompt}}' "migrate auth off the old session store"
```

`afk goal` is fail-closed: it errors until a setup command is configured (file
or flag). On approval, it prints a JSON receipt `{"goal_id":"<uuid>","tasks":N}`
to stdout — use the `goal_id` with `afk goal status` or `afk goal audit`. See
[command-reference.md](command-reference.md#goal) for the contract, approval, and
`goal status` / `goal audit` subcommands.

Run the visibility layer:

```sh
afk serve
```
