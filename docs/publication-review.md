# Public release review — v0.1.0

Reviewed on 2026-10-28. This is a bounded source and test review of an early-stage
tool, not a production-readiness certification or an independent security audit.
The final candidate is the commit carrying this report, plus any explicitly
recorded validation corrections before the immutable release tag.

## Public content

The original seven-commit private history was inventoried, including 21 historical
objects under development-conversation and plan directories. Gitleaks found no
credential matches. Manual classification nevertheless found local paths and
private infrastructure/discussion context. Those conversations and private plan
files are excluded from the independent public history, rather than treating a
negative scanner result as permission to publish them. Original private history
and active recordings stay in the original checkout. Program commits and their
intentional Git author/co-author attribution are retained; commit IDs change after
filtering. No original private refs are pushed.

Project code uses root MIT. The development skill has its own MIT notice. Release
support scripts and tests are adapted from the same maintainer's MIT lazypkg
project. Linked tools remain under their own licenses; downloaded tools are not
bundled into lazypg archives.

## Findings addressed

| Area | Change and verification |
| --- | --- |
| Saved plans | Format 2 binds destination, branch, physical names, engine, project config and migration/script file contents; unit and Docker regression tests reject changed inputs. |
| Names and isolation | Reject reserved, duplicate and overlong physical names; reserve `__` for branches; validate server/org filesystem components; branch deletion preflights lengths. |
| Environment policy | Branch switching now enforces local/dev policy; JSON mode cannot prompt; existing prod migration and destructive-operation denials remain tested. |
| Schema pull | Dump into a sibling staging directory and validate its entry file before replacing desired state; failure test preserves the old files. Replacement is per schema, not an all-schema transaction. |
| Credentials | Malformed URL errors omit the original secret-bearing input; known target credentials are redacted in connection/status/audit errors; Docker fallback passes passwords through environment rather than argument values. |
| External tools | Validate pgschema version components; checksums protect initial downloads; unique temporary files avoid concurrent download collisions. Failure tests do not install corrupt or failed downloads. |
| Version/distribution | Go installs recover module build version; injected release version takes precedence; verify source, module ZIP, four archives and native executable behavior. |

The initial vulnerability scan detected [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)
in the review host's Go 1.27.2 standard library. The minimum Go patch was raised to
1.26.10, and release builds use the fixed 1.26.10 toolchain. Source users must use
1.26.10 or 1.27.3 or newer; Go 1.27.0–1.27.2 remains affected despite its higher
minor version. A clean vulnerability scan with the fixed toolchain is a release gate.

## Validation contract

Local checks include Go unit/race tests, vet, formatting, minimum-Go tests,
Gitleaks history/worktree scans, workflow lint, release publisher contract tests,
and isolated Docker integration. Integration covers pgschema rename/data retention,
seed/reset, branch isolation, prod guards/stale schema plans, dbmate rollback and
changed saved-plan SQL, separate checkouts, shared servers and dependency-owner
preservation. Only test-created containers/volumes are removed.

CI is required on the exact candidate before tagging. Its four native runners
cover macOS/Linux amd64/arm64 with the minimum Go version. Linux additionally runs
Docker integration. The source/module verifier unpacks and builds actual Git and
Go ZIP artifacts independently. Release verification rejects missing platforms,
checksum mismatches, unsafe archives and incomplete public-release mutation.
Public fixed-tag/latest source installs and actual Homebrew installation are
post-publication acceptance checks; their final results belong in the release
handoff and workflow links, not an assumed claim in this pre-publication report.

## Local acceptance evidence

On the candidate source with the patched Go 1.26.10 toolchain:

- `go test -race ./...` and `go vet ./...`: passed.
- `LAZYPG_IT=1 go test ./integration -v -count=1 -timeout 15m`: all four scenarios passed.
- `govulncheck v1.9.0 ./...`: no vulnerabilities found.
- Workflow lint and publisher/source-distribution contract tests: passed.
- Independent source archive and module ZIP unpack/build/smoke: passed.
- Four-platform snapshot archive/checksum/architecture checks and native macOS arm64 smoke: passed.

The release tag workflow repeats the required checks on the exact public commit.
Other native platforms are verified by its CI matrix; these local results alone
make no claim about their runtime execution.

## Remaining limitations

- Connections and arbitrary SQL have the configured PostgreSQL role's privileges.
  Dependencies are excluded from owned-schema operations, not SQL-enforced read-only.
- Multi-database migrations/branch operations are sequential. A late error can leave
  earlier databases changed; inspect status and repair before retrying. Branch
  creation terminates sessions on the source database to satisfy PostgreSQL TEMPLATE.
- Plans, migrations, seeds, bootstrap SQL, Git deps, configured paths and
  `password_cmd` are trusted inputs. Protect plan artifacts like deployment SQL.
  Do not concurrently edit migration/script files during an apply.
- `plan` may download tools and create/bootstrap a local scratch database. Scratch
  validation shares its local instance; concurrent plans are not serialized.
- Port selection is best effort across processes; simultaneous initial starts can
  race. Start sequentially or retry after a port conflict. No distributed locking
  protects a whole pgschema multi-step deployment; dbmate uses its migration lock.
- Remote TLS defaults to `prefer` unless configured; choose explicit verification
  settings and provision certificates for sensitive environments. URL override
  supports host/port/user/password/sslmode, not arbitrary URI query parameters.
- Audit is best effort for lazypg-managed schema/data operations, not a full SQL
  access log. SQL itself can contain sensitive data; review logs and plan artifacts.
- Cached tools and explicit `LAZYPG_PGSCHEMA_BIN` overrides are trusted local files.
  First downloads are checksum-verified; tool-cache tampering is outside this boundary.
- pgtui is optional; the known stable-version database-selection limitation is
  documented in the README. A full interactive dashboard is future work.
- Existing data/volumes are not migrated between machines; rebuild from schema and
  seeds or use a separately reviewed PostgreSQL backup procedure.

These disclosed operational limits do not block an experimental public release.
An unresolved credential leak, wrong-target operation, data-loss regression,
failing required check or incomplete release artifact does block publication.
