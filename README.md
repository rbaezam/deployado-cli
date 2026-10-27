# deployado-cli

The command-line interface for the Deployado platform — a PaaS for
deploying applications, managing databases, viewing logs, and
diagnosing problems without leaving the terminal.

> **Status**: production, used daily. Tracks the `/api/v1` API of the
> Deployado control plane.

## Install

### Recommended (macOS & Linux)

```bash
curl -sSfL https://deployado.com/cli/install.sh | sh
```

Detects your OS/arch (`darwin`/`linux` × `amd64`/`arm64`), downloads the
matching tarball from the control plane, verifies its sha256 against
`checksums.txt`, and installs to `/usr/local/bin` — falling back to
`~/.local/bin` when `/usr/local/bin` isn't writable.

Options:

```bash
# pin a version instead of taking the latest
curl -sSfL https://deployado.com/cli/install.sh | sh -s -- --version 0.3.0

# install somewhere else
curl -sSfL https://deployado.com/cli/install.sh | sh -s -- --dir ~/bin
```

Re-running the installer upgrades in place. Binaries are also attached to
each [GitHub Release](https://github.com/rbaezam/deployado/releases) if you
prefer to download them by hand.

### From source

Requires Go 1.22+.

```bash
git clone git@github.com:rbaezam/deployado.git
cd deployado/deployado-cli
go build -o ~/.local/bin/deployado ./cmd/deployado/
```

Make sure `~/.local/bin` is on your `PATH`.

### Verify

```bash
deployado version
```

## Authentication

The CLI uses device-code OAuth — you'll get a short code to enter in
the browser and a long-lived access token is stored in
`~/.config/deployado/config.json`.

```bash
deployado auth login
# follow the prompt: visit the URL, enter the code, approve
```

To use multiple profiles (e.g. staging + production):

```bash
deployado auth login --profile staging
deployado --profile staging apps list
```

The active profile is settable via `DEPLOYADO_PROFILE` env var, or
the `--profile` flag (flag wins).

## Common workflows

### Deploy a new app from the current directory

```bash
cd my-app
deployado launch
# follow the prompts: pick a project, name, runtime
```

### Deploy a known app with progress streaming

```bash
deployado deploy <app-id> --watch
# streams docker build output (phase 1) and runtime stdout/stderr
# (phase 2) until the deployment is healthy or failed
```

Opt out of one stream:

```bash
deployado deploy <app-id> --watch --no-build-logs    # only runtime
deployado deploy <app-id> --watch --no-runtime-logs  # only build
```

### Diagnose an app that's acting weird

```bash
deployado doctor <app-id>
# runs ~8 checks in sequence and prints what's broken with a hint
# per failure pointing at the next command to run
```

For more detail (slower checks, longer log scan):

```bash
deployado doctor <app-id> --deep
```

The `Resources (live)` row in the output shows real container
CPU% and memory usage (refreshed every minute by the metrics
collector). Thresholds mirror the dashboard alert system:
**warn** at >80%, **fail** at >95% of CPU or memory limit. A
snapshot more than 2 minutes old is flagged so you don't act on
stale numbers.

Machine-readable output (status field uses stable string labels
`pass` / `info` / `warn` / `fail`, not integers):

```bash
deployado doctor <app-id> --json
```

Watch mode for incident response — re-runs every 5s (tunable with
`--interval N`), clears the screen between iterations, never exits
on failure (you want to see recovery, not bail):

```bash
deployado doctor <app-id> --watch
deployado doctor <app-id> -w --interval 10
```

Exit codes: `0` if no failures (warnings allowed), `1` if any check
failed, `2` if the app id isn't resolvable. (Watch mode never
exits with `1`.)

### Inspect a build that failed

`doctor` and `deploy --watch` point at this command when a build
breaks:

```bash
deployado releases logs <release-id>
# prints the full docker build transcript with status header + footer
```

Tail just the end:

```bash
deployado releases logs <release-id> --tail 50
```

Raw payload for scripts:

```bash
deployado releases logs <release-id> --json
```

### Rollback

```bash
deployado rollback <app-id>                          # to previous release
deployado rollback <app-id> --watch                  # follow the rollback live
deployado rollback <app-id> --list                   # show recent releases
deployado rollback <app-id> --release <release-id>   # pick a specific release
deployado rollback <app-id> --version 20260513.160342  # or by version string
```

`--release` and `--version` are mutually exclusive — pick one. The
release id is the full UUID shown by `rollback --list` or `releases
logs`. When `--watch` is set, runtime logs stream so you can
confirm the previous version comes back up healthy.

### Tail logs

```bash
deployado logs <app-id>                 # last 100 lines
deployado logs <app-id> --lines 500
deployado logs <app-id> --follow        # tail -f equivalent
```

### Run a command in the container

```bash
deployado exec <app-id> -- ls /app
deployado exec <app-id> -- 'echo "hello world"'
deployado ssh <app-id>                  # interactive shell (xterm.js wrapped)
```

### Environment variables

```bash
deployado env list <app-id>
deployado env set <app-id> KEY=value KEY2=value2
deployado env unset <app-id> KEY
```

Scope-aware variants (per-app vs per-pipeline-family) — see
`deployado env --help`.

### Databases

```bash
deployado database list
deployado database create --project <project-id> --name mydb
deployado database link <db-id> --to <app-id>         # injects DATABASE_URL
deployado database import <db-id> --file dump.sql    # streams a dump
```

### Querying an app's database

Most Deployado-managed DBs live on a private IP (10.0.0.x) and aren't
reachable from your workstation. The `--app` form sidesteps that: the
control plane forwards the SQL to the DB server's agent, which runs
`psql` locally. No local psql, no SSH tunnel, no network access needed.

```bash
# one-shot statement, result to stdout
deployado db query --app <app-id> "select count(*) from users"
deployado db query --app <app-id> --json "select id, name from plans limit 5"
deployado db query --app <app-id> --readonly "select * from users limit 1"

# interactive REPL (accumulates until `;`; \dt, \d, \l work)
deployado db psql --app <app-id>
deployado db psql --app <app-id> --readonly
```

SQL, credentials, and results are never written to the control plane's logs.

Passing a **db-id** instead of `--app` spawns your *local* `psql` against the
database (credentials fetched via the API, password passed via `PGPASSWORD`
so it doesn't leak via `ps auxe`):

```bash
deployado db psql <db-id>
deployado db psql <db-id> -- -c "SELECT count(*) FROM users;"
deployado db psql <db-id> -- -f migration.sql
```

That path needs a local `psql` binary and network reach to the database. If
the API reports a private host, the command refuses up-front and prints a
copy-paste SSH tunnel recipe — `--force` only if you're already on the
private network.

## Full command reference

| Command       | Purpose                                                       |
| ------------- | ------------------------------------------------------------- |
| `apps`        | List, show, update, delete apps                               |
| `auth`        | Device-code login, profile management                         |
| `database`    | Postgres provisioning, `query`, `psql`, dumps (alias: `db`)    |
| `deploy`      | Trigger a deployment; `--watch` to follow until done          |
| `doctor`      | Diagnostic checklist for one app                              |
| `domains`     | Attach/detach custom domains                                  |
| `env`         | Read/write environment variables (per-app or per-pipeline)    |
| `exec`        | One-shot command in the running container                     |
| `init`        | Create a `deployado.yaml` blueprint                           |
| `launch`      | Interactive new-app setup from current directory              |
| `link`        | Wire a frontend app to an API backend                         |
| `logs`        | Runtime stdout/stderr; `--follow` for tailing                 |
| `projects`    | List, show, update projects                                   |
| `ps`          | Live overview of all running deployments                      |
| `releases`    | `releases logs <id>` — fetch a build transcript               |
| `rollback`    | Revert to a previous release; `--watch` to follow             |
| `ssh`         | Interactive shell (TTY) into a running container              |
| `up`          | Deploy a multi-service stack from `deployado.yaml`            |
| `version`     | Print version info                                            |

Every command supports `--help` for full flag documentation.

## Configuration

| Path                                   | What it holds                          |
| -------------------------------------- | -------------------------------------- |
| `~/.config/deployado/config.json`      | Profiles + access tokens               |
| `./deployado.yaml`                     | Project blueprint (used by `up`)       |

### Environment variables

| Variable             | Effect                                  |
| -------------------- | --------------------------------------- |
| `DEPLOYADO_PROFILE`  | Selects which profile to use (default: `active` from config). Overridden by `--profile`. |

## Troubleshooting

### "not logged in. Run 'deployado auth login' first"

Self-explanatory — your token expired or got cleared. Re-run
`auth login`.

### `deploy --watch` hangs at "Build queued..."

Builds can take 3+ minutes. The watcher polls release status every
2 seconds and prints status changes. If the spinner is moving,
nothing is stuck. To see the actual build progress, leave
`--watch` running — phase 2 (deployment) begins automatically when
the build finishes.

If you really need to inspect a stuck build, in another terminal:

```bash
deployado releases logs <release-id>
```

The release id is printed by `deploy` when the build is triggered.

### "App not found" for a valid app id

The CLI requires the full UUID, not the 8-char prefix shown in
`apps list`. Use `apps list --json` to get the full id, or use the
app's slug instead.

### Doctor's HTTP probe times out

The default timeout is 3s. Add `--deep` to raise it to 10s. If it
still times out, the edge (Traefik) is down or the domain DNS is
pointing somewhere else — check the dashboard.

## Architecture pointers

- **API contract**: the control plane's `/api/v1` scope (https://docs.deployado.com).
- **CLI structure**: every subcommand is its own file in
  `internal/cmd/`, registered via `init()` calls hooked into
  `rootCmd`. Tests live next to the file as `*_test.go`.
- **HTTP client**: `internal/api/client.go` — one method per
  endpoint, all return typed structs from the same file.

## Contributing

When adding a new command:

1. Create `internal/cmd/<name>.go` with a `cobra.Command` registered
   in `init()`.
2. Add tests for any pure helpers — the established pattern is to
   extract the non-I/O logic into a small function and exercise the
   edges (empty, boundary, defensive cases).
3. Update [`../deployado/CHANGELOG.md`](../deployado/CHANGELOG.md)
   under the right date with an **Added** entry. Lead with what the
   user sees, link the commit.
4. If the command is something AI agents would want to call directly,
   add a tool to `../deployado-mcp/src/tools/` too.

See `internal/cmd/doctor.go` and `internal/cmd/releases.go` for
canonical examples of well-tested commands with rich help text.
