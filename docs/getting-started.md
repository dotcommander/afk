# getting started

```sh
afk add "write tests for the scheduler"
afk pop --lease 30m
afk done 1
```

## install

Build from source and copy the binary into your path:

```sh
go build -o afk ./cmd/afk
mkdir -p ~/go/bin
rm -f ~/go/bin/afk
install -m 0755 afk ~/go/bin/afk
```

Confirm the install:

```sh
afk --version
afk doctor
```

`afk doctor` reports the queue path, database presence, task counts, working-task warning, binary path, and prompt health.

## queue your first task

```sh
afk add "fix the failing queue test"
```

`afk` prints the new task id. Inspect what is waiting:

```sh
afk ls
afk status
afk ready --limit 1
```

`afk ready --limit 1` shows the task `afk pop` will claim next without mutating anything.

## capture work while busy

When you already know the work but cannot do it now, add the task from the
project root:

```sh
cd /Users/you/code/my-project
afk add "Fix the settings save bug. Success: settings persist after refresh. Verify with go test ./..."
```

Running from the project root records enough context for a later worker:

- `cwd` is the current project directory.
- `source` is `cli`.
- inside a git repo, `resource` is `repo:<git-root>` so two workers do not claim tasks against the same repo at once.

If you are not in the project directory, pass the project path explicitly:

```sh
afk add --cwd /Users/you/code/my-project \
  "Fix the settings save bug. Success: settings persist after refresh. Verify with go test ./..."
```

The task stays `pending` until you or a runner claims it.

## claim and finish

Claim the next ready task with a 30-minute lease:

```sh
afk pop --lease 30m
```

The command prints the claimed task as JSON and moves it to `working`. Do the work, then close the loop:

```sh
afk done 1
```

If the work is blocked or impossible, record the reason and move on:

```sh
afk fail 1 "missing credentials"
afk retry 1
```

`afk retry` returns the task to `pending` while preserving the attempt history. Read it back with `afk explain 1`.

## chain dependent work

Use `--blocked-by` when one task must finish before another can run:

```sh
schema=$(afk add "add migration")
afk add --blocked-by "$schema" "update README after migration ships"
afk ready
```

The second task stays `pending` and is excluded from `afk ready` until the first task is `done`. See [scheduling.md](scheduling.md) for blocks and resource locks.

## next steps

- Read [tasks.md](tasks.md) for metadata flags (`--tag`, `--priority`, `--source`, `--cwd`).
- Run `afk prompt --discover`, or read [task-discovery.md](task-discovery.md), to mine concrete high-impact candidate tasks from review material.
- Read [runner.md](runner.md) for `afk run`, the built-in worker loop.
- Read [command-reference.md](command-reference.md) for the full surface.
