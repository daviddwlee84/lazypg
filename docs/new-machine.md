# Set up another machine

Install the same lazypg version on macOS or Linux (amd64/arm64). The current public
version is v0.1.2. A release binary needs no Go installation.

```sh
brew install daviddwlee84/tap/lazypg
lazypg --version
docker compose version
docker info
```

Use a patched Go toolchain (1.26.10 or 1.27.3 or newer) for source builds.

For an exact version, use the matching release archive and its checksums, or:

```sh
go install github.com/daviddwlee84/lazypg/cmd/lazypg@v0.1.2
```

Homebrew follows the latest stable release; fixed Go versions and release assets
are the reproducible installation channels. For manual downloads select exactly
one of `darwin_arm64`, `darwin_amd64`, `linux_arm64`, `linux_amd64`, then use
`shasum -a 256` (macOS) or `sha256sum` (Linux) to compare the archive with the
same release's `checksums.txt`. Extract the verified archive and install `lazypg`
into a directory already on PATH. Never run `lazypg start` with sudo.

Clone your **database project**, which contains `lazypg.toml`, `db/`, and any
`lazypg.lock`. Those files define reproducible schema, migrations, scripts and
seed data. They do not contain your previous machine's live database rows.

```sh
cd your-project
lazypg doctor
lazypg start
lazypg doctor
lazypg status
lazypg plan
lazypg exec -- your-test-command
lazypg psql
```

The first `doctor` can download the project's pinned pgschema. `start` pulls the
configured PostgreSQL image, allocates a loopback port, applies schema and seeds
new databases. The same checkout path on another machine is not required.

If your project uses an org, register its config and establish credentials on
each machine; substitute your own aliases and trusted config source:

```sh
lazypg org add acme --git git@github.com:acme/infra.git
lazypg login acme/dev --password-env ACME_DEV_PGPASSWORD
lazypg doctor
lazypg status --env dev
```

Set `ACME_DEV_PGPASSWORD` through your shell or secret manager. Alternatively use
`login` interactively or a trusted `--password-cmd`. SSH access to private Git
repos, network access to remote PostgreSQL, and TLS certificates are configured
separately on each machine. Avoid putting secrets into project files.

| Carry with Git | Configure on each machine | Recreate locally |
| --- | --- | --- |
| `lazypg.toml`, `db/`, `lazypg.lock` | Org registration, credentials, dep path overrides | Allocated ports, selected env/branch, containers, tool caches |

Do not copy `~/.local/state/lazypg` or generated compose files to the new machine.
Existing rows, database branches and Docker volumes do not transfer. This release
rebuilds development databases from source and seeds; data transfer needs a
separately reviewed PostgreSQL backup/restore procedure.

For offline pgschema use, install the project's pinned version in advance and
set `LAZYPG_PGSCHEMA_BIN` to that trusted executable. Docker images and Git deps
must also be available. This override intentionally bypasses download verification.

For upgrades, use the installation channel listed in the README, then verify
`command -v lazypg` and `lazypg --version`. For shell completions, Homebrew installs
bash/zsh files; other channels can use `lazypg completion bash|zsh|fish` and their
shell's usual completion directory.
