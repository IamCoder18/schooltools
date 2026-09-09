# Changelog

All notable changes to **schooltools** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial public release on GitHub.
- Nix flake packaging for running or installing `schooltools` on NixOS and
  other systems with flakes enabled.
- Automated update pull requests for pinned Nix inputs and the Go module hash.

## [0.2.0] - 2026-09-05

### Added
- Full D2L Valence feature-parity redesign. See
  `D2L_API_RESEARCH.md` and the original design notes in
  `.kilo/plans/cli-feature-parity-redesign.md`.
- New top-level commands: `course`, `content`, `download`, `archive`,
  `assignment` / `dropbox`, `quiz`.
- `archive list`, `archive path`, `archive read`, `archive topic` for
  inspecting the on-disk content-addressed archive store.
- `prune` to reclaim old versions after `archive` runs.
- `systemd` subcommand with a `schooltools-archive.timer` template for
  nightly archival.
- `auth log` and `--no-auth-log` opt-out for the JSONL auth event log.

### Changed
- **Breaking:** CLI flag convention switched to singular nouns with
  scope-via-flag (`--course <id>`, `--org CSV`). See `README.md` for the
  full old → new table.
- HTTP client moved to a single `internal/httpclient` package with a
  Bearer-token auto-refresh path on 401.
- Session storage moved to `~/.config/schooltools/session.json` with the
  JSON cookie schema documented in the README.

### Deprecated
- Legacy positional forms of `content` and `download` still work but emit
  a one-line deprecation notice. They are hidden from `--help` and will
  be removed in a future release.

## [0.1.0] - 2026-08-15

### Added
- Go port of the previous TypeScript CLI: cobra, goquery, and
  `net/http/cookiejar` replaced commander, cheerio, and tough-cookie.

## [0.0.x] - 2026-07 and earlier

### Added
- Original TypeScript CLI prototype.

[Unreleased]: https://github.com/aarav/schooltools/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/aarav/schooltools/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aarav/schooltools/releases/tag/v0.1.0
