# tasks

Add a task:

```sh
afk add "fix the failing queue test"
afk add --dry-run --json "validate this task shape"
```

Useful metadata:

```sh
afk add --tag repo:afk --priority high --source roadmap.md "review scheduler indexes"
afk add --cwd /path/to/repo --resource repo:/path/to/repo "run the local smoke test"
afk add --blocked-by 123 "update docs after task 123 lands"
```

List and search:

```sh
afk tasks
afk tasks --status todo --json
afk tasks --status deleted
afk find scheduler --json
```

Inspect one task:

```sh
afk task 123
afk task 123 --json
```

Set status:

```sh
afk set 123 done --note "implemented and tested" --summary
afk set 123 failed --note "missing credentials" --summary
afk set 123 deleted --note "superseded by a narrower task"
```

`--summary` emits a JSON receipt with the task id, status, title, note, and
queue counts. Use `--note-file -` when the evidence note contains shell-special
characters or spans multiple lines:

```sh
printf '%s\n' "$evidence" | afk set 123 done --note-file - --summary
```

Deleted tasks are hidden from default listings but remain available through `afk tasks --status deleted` and `afk task <id>`.
