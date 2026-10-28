# Changelog

## Unreleased

## v0.1.0 — 2026-10-28

Source builds require a patched Go toolchain: Go 1.26.10 or 1.27.3 or newer.

Initial public early-stage release: local PostgreSQL workflows, explicit remote
environments with tier policy, pgschema/dbmate, scripts, branches and cross-project
dependencies. macOS/Linux amd64/arm64 archives, fixed-version Go installation and
Homebrew distribution share the same version contract.

Public-readiness fixes bind saved plans to the resolved destination and SQL inputs,
reject reserved/overlong/colliding database names and unsafe server/org path
components, enforce branch-switch policy, preserve desired state on failed pulls,
and keep fallback Docker passwords out of process arguments. Saved plans from
private development builds must be regenerated (format 2).

Dependency connections use the configured PostgreSQL role's permissions; lazypg
does not enforce read-only SQL access. Multi-database operations are sequential,
not atomic across databases. See docs/publication-review.md for review boundaries.
