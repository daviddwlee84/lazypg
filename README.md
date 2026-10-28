# lazypg

**Early stage (v0.1).** Review schema plans and keep backups before using shared or production databases. See [limitations and review](docs/publication-review.md).

A PostgreSQL development workflow with a Supabase-CLI-style UX, built from existing tools: Docker Compose, [pgschema](https://github.com/pgplex/pgschema), [dbmate](https://github.com/amacneil/dbmate) and pgx.

- **Reproducible local databases**: `lazypg start` brings up PostgreSQL, applies the schema from the repo, and seeds new databases.
- **Safe environments**: you work against `local`, `dev`, `stage` and `prod` by name instead of juggling `.env` files. Each tier has its own guard rails.
- **Schema as code, per database**: choose declarative pgschema (the default) or versioned dbmate migrations.
- **Multi-project sharing**: every repo owns its own schema. Repos can share one machine-level instance, and consume each other's databases as dependencies. Database roles control write permissions.

You only need Docker and the `lazypg` binary. `psql` and `pg_dump` run inside containers, and pgschema is downloaded at its pinned version with checksum verification.

> **Scope.** lazypg is glue for the development workflow, not a database platform. For a TUI browser it launches [pgtui](https://github.com/pgplex/pgtui). For a shared web editor or an MCP gateway for agents, see [pgconsole](https://github.com/pgplex/pgconsole).

## Install

```sh
brew install daviddwlee84/tap/lazypg
lazypg --version
```

Alternatively, download a matching macOS/Linux amd64/arm64 archive from
[Releases](https://github.com/daviddwlee84/lazypg/releases), verify it against
`checksums.txt`, and install the executable on your PATH. Source installation
requires patched Go 1.26.10 or Go 1.27.3 or newer:

```sh
go install github.com/daviddwlee84/lazypg/cmd/lazypg@v0.1.0
```

Inspect `go env GOBIN GOPATH` to locate the installed binary (`GOBIN`, or
`bin` under `GOPATH`); include that directory in PATH. Docker with Compose v2
and a running daemon is needed for local databases. Git is needed for Git-based
orgs and dependencies. pgtui is an optional, separately installed browser.
Windows users can use the Linux build inside WSL with Docker integration.

Upgrade using the original installation channel: `brew upgrade daviddwlee84/tap/lazypg`,
`go install github.com/daviddwlee84/lazypg/cmd/lazypg@latest`, or a new verified
release archive. v0.1 deliberately uses these external upgrade paths and has no
self-update command. Check `command -v lazypg` and `lazypg --version` afterward
so another copy on PATH does not hide the upgrade. See the [changelog](CHANGELOG.md).

[Set up another machine](docs/new-machine.md) without copying credentials or runtime state.

## Quick start

```sh
lazypg init                    # writes lazypg.toml and db/ (touches no database)
$EDITOR db/schema/public/main.sql
lazypg start                   # container → create DB → plan/apply → seed
lazypg plan                    # after editing the desired state
lazypg migrate
lazypg exec -- pytest          # DATABASE_URL, PG*, <NAME>_DATABASE_URL injected
lazypg psql
```

## Configuration layers

| Layer | File | Holds | Commit? |
|---|---|---|---|
| Project | `lazypg.toml` + `db/` | Owned databases, engine, desired schema or migrations, scripts, seeds, deps, env → instance alias, pinned tool versions | yes |
| Org | `lazypg-org.toml`, kept in your infrastructure repo | Team instances: host, port, sslmode, tier. **No secrets.** | yes (in the org repo) |
| User | `~/.config/lazypg/` | Registered orgs, personal instances, `credentials.toml` (0600), per-project dep path overrides | no |

Runtime state (allocated ports, selected env, active branch, `audit.jsonl`) is kept in `~/.local/state/lazypg`. Generated compose files and downloaded tools are kept in `~/.local/share/lazypg`. The XDG variables are honored on macOS too.

```toml
# lazypg.toml
version = 1
project = "trading_journal"
org = "acme"

[local]
mode = "project"                 # or "server": one shared container per machine
image = "pgvector/pgvector:pg18"

[[databases]]
name = "journal"
engine = "pgschema"              # or "dbmate"
schemas = ["journal"]
extensions = ["vector"]          # created by lazypg; pgschema does not manage extensions

[[deps]]                         # owned by another repo; read-only here
name = "events"
git = "git@github.com:acme/agent-events.git"
ref = "v1.2.0"

[envs.dev]
tier = "dev"
instance = "acme/dev"

[envs.prod]
tier = "prod"
instance = "acme/prod"
```

```toml
# lazypg-org.toml (in the org's infra repo)
org = "acme"
[instances.dev]
host = "pg-dev.tailnet.ts.net"
tier = "dev"
shared = true                    # physical names become <project>_<db>
[instances.prod]
host = "acme-prod.postgres.database.azure.com"
sslmode = "verify-full"
tier = "prod"
```

```sh
lazypg org add acme --git git@github.com:acme/infra.git     # or --path ../infra
lazypg login acme/prod --password-cmd 'op read op://infra/pg-prod/password'
lazypg login acme/dev                                       # prompts; stored 0600
lazypg doctor                                               # docker, tools, every env's connectivity
```

## Environments and tiers

`local` always exists and is the default. `lazypg env use dev` makes another env the default for this checkout. **prod can never be made the default**, so every prod command has to name `--env prod` explicitly.

| Tier | reset / seed / rollback / branch | migrate | exec, psql, url, ui |
|---|---|---|---|
| local | yes | yes | yes |
| dev | confirm (y/N or `--yes`) | confirm | yes |
| stage | no reset, no rollback, no branch; seed asks for confirmation | confirm | yes |
| prod | **denied** | type `prod`, or pass `--confirm prod` (`--yes` is not enough) | confirm |

- Database-changing and connection commands print a colored banner such as `● prod · acme/prod · journal`.
- Schema/data changes made through lazypg against non-local envs are appended to `~/.local/state/lazypg/audit.jsonl`.
- Policy denials exit with code 3.
- If an env's tier and its instance's declared tier disagree, lazypg refuses to run instead of guessing which one is right.

Ways to get connection settings without `.env` files:

```sh
lazypg exec --env stage -- python manage.py shell
eval "$(lazypg env shell)"          # direnv
lazypg env dotenv > .env.local      # for tools that insist on a file
lazypg url --db events
```

`lazypg ui` opens the selected env in pgtui. In pgtui's connection dialog, press Tab to reach "Discovered", then Enter on the "(Environment)" entry. pgtui v0.3.0 ignores `PGDATABASE` and opens the `postgres` database; its main branch opens the right database (`go install github.com/pgplex/pgtui/cmd/pgtui@main`).

For CI, set `LAZYPG_URL_<ENV>` (for example `LAZYPG_URL_PROD=postgres://…`). It overrides host, port, user, password and sslmode; the URL database path is ignored, and database naming and tier come from `lazypg.toml`. Set a suitable database role and TLS settings for the target. This explicit override bypasses instance-tier cross-checking.

## Schema engines

**pgschema (default, declarative).** The desired state lives in `db/schema/<schema>/main.sql`; `\i` includes are allowed. `plan` runs `pgschema plan` once per schema.

- **Plan validation.** lazypg points pgschema's `--plan-host` at the scratch database `lazypg_plan` in your local container. Desired state is validated with your real image and extensions, such as pgvector.
- **Adopting an existing database.** `lazypg db pull` writes the live schema as desired-state files.
- **What pgschema doesn't cover** (CREATE DATABASE, SCHEMA, EXTENSION, renames, data migrations) is handled by lazypg:
  - **Bootstrap.** lazypg creates the database, schemas and extensions idempotently, and runs `bootstrap` SQL once when a database is created.
  - **Scripts.** `lazypg script new <name> --phase pre|post` creates a dbmate-format file under `db/scripts/{pre,post}`. Pre scripts run before the diff is computed; this is where renames go, so the diff becomes a no-op instead of drop-and-add. Post scripts run after apply and suit backfills. Script history lives in the `lazypg` schema, which pgschema never sees.
  - **New databases skip scripts.** When a database is newly created (fresh env, `reset`, deps), existing scripts are recorded as applied but not run: the database is built straight from the current desired state.

**dbmate (opt-in, versioned).** Migrations live in `db/migrations`.

- `lazypg migration new <name>` creates a file. `migrate` applies pending migrations, and `rollback` reverts the last one (local and dev only).
- On shared envs, an advisory lock serializes concurrent deploys.

For any engine, `lazypg db dump` writes a schema-only snapshot to `db/schema.sql`: one file an agent can read to see the whole schema.

### CI: review the plan, then apply exactly that plan

```sh
# in the pull request
lazypg plan --env prod --out plan.json        # attach to the PR for review
# after merge
lazypg migrate --env prod --plan plan.json --confirm prod
```

Saved plans are bound to the resolved connection target, branch, physical database names,
engine, project configuration, and migration/script contents. pgschema also checks the
live schema fingerprint; dbmate checks its pending migration set. Edited scripts or
migrations require another plan and review. Plans are trusted execution artifacts:
protect them as you would SQL deployment scripts. Plans from the pre-publication
format must be regenerated. `plan` does not apply changes to the target, but can
create/bootstrap the local `lazypg_plan` scratch database and download pgschema.

## Local instances, branches, multi-project sharing

**Instances**

- **`mode = "project"`** gives each checkout its own container. The compose project name comes from the checkout path, so git worktrees are isolated automatically. Ports are allocated from 54320 upward and remembered.
- **`mode = "server"`** puts projects on a named machine-level server (`lazypg server start|stop|ls`), one database per project: `<project>_<db>`. A repo that depends on another repo's database then shares the very same physical database. Each repo still manages its own schema, so you don't need a separate shared-db repo.
- **`lazypg eject`** copies the generated compose file into `.lazypg/compose.yaml` for customization.

**Branches**

- `lazypg branch create exp` copies every owned database with `CREATE DATABASE … TEMPLATE` into `<db>__exp` and switches to it.
- While a branch is active, every command targets the copies.
- `lazypg branch use main` switches back; `lazypg branch delete exp` drops the copies.
- Branches work on local and dev.

**Deps** are databases owned by other repositories. Their sources are `git` + `ref` (pinned in `lazypg.lock`, which you commit), `path`, or `vendor` (a SQL file).

- On local, lazypg builds a dep from its owner's own engine, scripts and seeds.
- Existing dep databases are left alone, because on a shared server the owner may be developing a newer schema in that very database. Use `lazypg deps apply --force` to rebuild them.
- A personal override (`[overrides.<project>.dep_paths]` in your user config) points a dep at a local checkout without touching the lock.
- On remote envs, deps resolve to the owner's database name. Schema/data management commands select owned databases; `psql`, `exec`, and exported URLs still have the permissions of the configured role. Use a database-enforced read-only role when needed.

## Trust and credentials

Run lazypg only in trusted projects: schema, bootstrap, seeds and migrations execute SQL;
Git dependency SQL is also executable input. `password_cmd` intentionally executes a
configured shell command. Credentials remain machine-local, with `credentials.toml`
written as mode 0600. Environment exports and child applications receive credentials
intentionally; avoid recording their output. Tier confirmations reduce mistakes and
do not replace PostgreSQL authorization or backups. Audit logs cover lazypg-managed
schema/data operations, are best effort, and are not a complete database audit trail.

## Development

```sh
go test ./...                                         # unit tests
LAZYPG_IT=1 go test ./integration -v -timeout 15m      # Docker-backed end-to-end tests
```

Code layout: `internal/cli` is a thin Cobra layer over `internal/app`, which holds the operations. Engines live in `internal/engine`, environment resolution and policy in `internal/env`, and containers in `internal/instance`.

## Roadmap

- **v0.2:**
  - An instance manifest in `lazypg-org.toml`, with `instance apply` to rebuild a whole instance across projects, and `instance snapshot/restore` (`pg_dumpall --globals` plus per-database `pg_dump -Fc`) for Azure ↔ VM moves and NAS backups.
  - Role templates (`app_rw`, `agent_ro`, `migrator`).
  - Multiple repos owning different schemas of one database.
  - CI templates.
  - `console config` for pgconsole.
  - Keychain support.
- **Later:** a Bubble Tea dashboard.

## License

[MIT](LICENSE). Development skill files retain their own MIT notice.
