# Changelog

## v0.4.0 (unreleased)

### Added

- Added `Client.Ping(ctx)` as a lightweight request/response probe for the
  active ESPHome API session.
- Added an explicit MIT `LICENSE` file.

### Changed

- Enabled TCP keepalive on API sockets with a 60-second keepalive period, so
  dead peers are detected by the existing read loop and reconnect path.
- Added Dependabot configuration for daily dependency checks with a 7-day
  cooldown across ecosystems.

## v0.3.0 (2026-04-22)

### Changed

- Announce ESPHome API 1.14 during the Hello handshake, matching ESPHome
  2026.4 and avoiding server-side version warnings.
- Handle ESPHome API 1.14 entity discovery responses that omit `object_id` by
  deriving stable IDs from entity names, so stock ratgdo entities continue to
  resolve.

### Added

- Added README documentation covering install, library usage, CLI usage,
  firmware requirements, and package docs.
- Added `scripts/` ESPHome firmware helpers, including an encrypted native API
  overlay, HTTP basic auth configuration, Docker-backed build/flash targets,
  and setup instructions.

## v0.2.0 (2026-04-20)

### Added

- Added Buildkite CI for formatting, linting, tests, and command builds.
- Added a `Makefile` with `test` and `release` targets for consistent version
  bumps.
- Added the initial ratgdo Go client for ESPHome native API devices, including
  Noise encryption, reconnect handling, command methods, state snapshots,
  subscriptions, transition helpers, and `WaitFor`.
- Added the `ratgdo` CLI with `info`, `state`, `watch`, door control, and light
  control subcommands.
