# Repository Guidelines

## Project Structure & Module Organization

This workspace is currently an empty project scaffold. No application source, tests, assets, or dependency manifests are present. The `.agents/`, `.codex/`, `.aws/`, and `.git/` directories are environment-managed; do not use them for application code or modify them as part of routine contributions.

When introducing the transaction service, organize production code under `src/`, automated tests under `tests/`, and architectural or operational documentation under `docs/`, unless the chosen framework requires another layout. Group modules by responsibility and keep transaction processing separate from transport and persistence concerns.

## Build, Test, and Development Commands

No build, test, or local development commands are configured yet. Do not assume commands such as `npm test` or `make build` work.

The first implementation should add a dependency manifest and document exact setup, build, test, and run commands in `README.md`. Prefer reproducible commands and commit dependency lockfiles when supported by the selected toolchain.

## Coding Style & Naming Conventions

No language, formatter, or linter has been selected. Follow the chosen language's established conventions, use consistent indentation, and configure formatting and linting alongside the first source files. Use descriptive names that reflect transaction and service responsibilities; avoid unexplained abbreviations.

## Testing Guidelines

No testing framework or coverage threshold exists yet. Add automated tests with new behavior and document how to run them. Follow the selected framework's test naming conventions. For transaction logic, cover successful operations, validation failures, duplicate requests, concurrency, and rollback behavior where applicable.

## Commit & Pull Request Guidelines

Git history is unavailable in this workspace, so no existing commit convention can be verified. Use concise, imperative commit subjects, such as `Add transaction validation`.

Pull requests should explain the change, its purpose, and validation performed. Link relevant issues and call out configuration changes or migrations. Include screenshots only for user interface changes.

## Security & Configuration

Never commit credentials, private keys, or sensitive transaction data. Provide sanitized configuration examples and document required environment variables when configuration is introduced.
