# lazypg

A PostgreSQL development workflow with a Supabase-CLI-style UX, built from existing tools: Docker Compose, [pgschema](https://github.com/pgplex/pgschema), [dbmate](https://github.com/amacneil/dbmate) and pgx.

- **Reproducible local databases**: `lazypg start` brings up PostgreSQL, applies the schema from the repo, and seeds new databases.
- **Safe environments**: you work against `local`, `dev`, `stage` and `prod` by name instead of juggling `.env` files. Each tier has its own guard rails.
- **Schema as code, per database**: choose declarative pgschema (the default) or versioned dbmate migrations.
- **Multi-project sharing**: every repo owns its own schema. Repos can share one machine-level instance, and consume each other's databases as read-only deps.

You only need Docker and the `lazypg` binary. `psql` and `pg_dump` run inside containers, and pgschema is downloaded at its pinned version with checksum verification.

> **Scope.** lazypg is glue for the development workflow, not a database platform. For a TUI browser it launches [pgtui](https://github.com/pgplex/pgtui). For a shared web editor or an MCP gateway for agents, see [pgconsole](https://github.com/pgplex/pgconsole).

## Install

```sh
go install github.com/daviddwlee84/lazypg/cmd/lazypg@latest
```

Supported platforms: macOS and Linux. pgschema does not support Windows; use WSL there.

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

- Every command that touches a database first prints a colored banner such as `● prod · acme/prod · journal`.
- Commands against non-local envs are appended to `~/.local/state/lazypg/audit.jsonl`.
- Policy denials exit with code 3.
- If an env's tier and its instance's declared tier disagree, lazypg refuses to run instead of guessing which one is right.

Ways to get connection settings without `.env` files:

```sh
lazypg exec --env stage -- python manage.py shell
eval "$(lazypg env shell)"          # direnv
lazypg env dotenv > .env.local      # for tools that insist on a file
lazypg url --db events
```

For CI, set `LAZYPG_URL_<ENV>` (for example `LAZYPG_URL_PROD=postgres://…`). It supplies the whole connection, and the tier still comes from `lazypg.toml`.

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

If the database changed after the plan was made, pgschema's fingerprint check rejects the stale plan.

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
- On remote envs, deps resolve to the owner's database name. Writes to them are refused.

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
