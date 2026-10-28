# lazypg development tasks. Run `just` to list recipes.

set shell := ["bash", "-euo", "pipefail", "-c"]
set positional-arguments

bin := justfile_directory() / "bin" / "lazypg"
sandbox := justfile_directory() / ".sandbox"
demo := sandbox / "demo"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-X github.com/daviddwlee84/lazypg/internal/cli.Version=" + version

# Isolated XDG dirs: sandbox runs never touch ~/.config/lazypg or your real state.
xdg := "XDG_CONFIG_HOME=" + sandbox / "xdg/config" + " XDG_DATA_HOME=" + sandbox / "xdg/data" + " XDG_STATE_HOME=" + sandbox / "xdg/state" + " XDG_CACHE_HOME=" + sandbox / "xdg/cache"

# List recipes
default:
    @just --list --unsorted

# Build ./bin/lazypg
build:
    @go build -ldflags "{{ ldflags }}" -o "{{ bin }}" ./cmd/lazypg

# Install lazypg into $GOBIN
install:
    go install -ldflags "{{ ldflags }}" ./cmd/lazypg

# Unit tests
test *args:
    go test ./... "$@"

# Docker-backed integration tests (reuses the sandbox's pgschema download when present)
it *args:
    #!/usr/bin/env bash
    set -euo pipefail
    cached=$(ls -d "{{ sandbox }}"/xdg/data/lazypg/tools/pgschema/*/pgschema 2>/dev/null | tail -1 || true)
    if [[ -n "$cached" && -z "${LAZYPG_PGSCHEMA_BIN:-}" ]]; then export LAZYPG_PGSCHEMA_BIN="$cached"; fi
    LAZYPG_IT=1 go test ./integration -v -count=1 -timeout 15m "$@"

# gofmt (write) + go mod tidy
fmt:
    gofmt -w .
    go mod tidy

# Formatting check, vet, and unit tests (what CI should run)
check:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted=$(gofmt -l .)
    if [[ -n "$unformatted" ]]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi
    go vet ./...
    go test ./...

# Run the dev build in the sandbox from the current directory: just lp status
lp *args: build
    @cd "{{ invocation_directory() }}" && env {{ xdg }} "{{ bin }}" "$@"

# Create the demo project in .sandbox/demo and start it
demo: build
    #!/usr/bin/env bash
    set -euo pipefail
    lp() { env {{ xdg }} "{{ bin }}" -C "{{ demo }}" "$@"; }
    if [[ ! -f "{{ demo }}/lazypg.toml" ]]; then
        mkdir -p "{{ demo }}" && git -C "{{ demo }}" init -q
        lp init --name demo
        cat > "{{ demo }}/db/schema/public/main.sql" <<'SQL'
    CREATE TABLE notes (
        id bigserial PRIMARY KEY,
        body text NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now()
    );
    SQL
        echo "INSERT INTO notes (body) VALUES ('hello'), ('world');" > "{{ demo }}/db/seeds/seed.sql"
    fi
    lp start
    echo
    echo "demo ready: just d <command>   (e.g. just d plan, just d psql, just d branch create exp)"

# Run a lazypg command against the demo project: just d plan
d *args: build
    @env {{ xdg }} "{{ bin }}" -C "{{ demo }}" "$@"

# Edit the demo's desired schema, then run: just d plan && just d migrate
demo-edit:
    ${EDITOR:-vi} "{{ demo }}/db/schema/public/main.sql"

# Stop sandbox containers, delete their data, and remove .sandbox and bin/
clean:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -x "{{ bin }}" && -d "{{ sandbox }}/xdg/state/lazypg" ]]; then
        for f in "{{ sandbox }}"/xdg/state/lazypg/projects/*.toml; do
            [[ -e "$f" ]] || continue
            dir=$(sed -n "s/^path = '\(.*\)'$/\1/p; s/^path = \"\(.*\)\"$/\1/p" "$f")
            [[ -f "$dir/lazypg.toml" ]] && env {{ xdg }} "{{ bin }}" -C "$dir" stop --purge --yes || true
        done
        for f in "{{ sandbox }}"/xdg/state/lazypg/servers/*.toml; do
            [[ -e "$f" ]] || continue
            env {{ xdg }} "{{ bin }}" server stop "$(basename "$f" .toml)" --purge --yes || true
        done
    fi
    rm -rf "{{ sandbox }}" "{{ justfile_directory() }}/bin"
    echo "sandbox cleaned"
