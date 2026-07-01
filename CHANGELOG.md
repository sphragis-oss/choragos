# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial project scaffold: modules, cobra CLI, config loading.
- Bubble Tea TUI with a status-card column and an auto-expanding accordion layout.
- PTY pane management allowing real-time injection and capture of agent activity via `vt10x`.
- IPC via UNIX socket (0600) enabling inter-agent `delegate` and `work-done` flows.
- Sphragis gateway supervisor mapping LLM traffic implicitly into a local AI Act compliance layer.
- `Orchestrator`, `Coder`, `Reviewer`, `Auditor`, and `Release` default crew setups via TOML config.
