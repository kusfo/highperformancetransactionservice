# Continuous integration

`.github/workflows/ci.yml` runs `make check-all` on an Ubuntu 24.04 GitHub-hosted
VM with Docker. It runs on every branch push, pull-request opening/update/reopen,
and manual dispatch. Push and pull-request runs are separate, so a branch with an
open PR intentionally runs both. New runs cancel older runs for the same event
and ref. Manual dispatch becomes available after the workflow reaches `main`.

## Checks and permissions

The workflow is **CI**; its stable job/check name is **Repository validation**.
It runs contracts/examples, orchestration regressions, formatting, vet, all Go
builds, unit/race tests and isolated PostgreSQL integration. Logs appear in the
Actions run and PR checks. Nonzero validation exits fail the job. The job has a
20-minute timeout. The existing validation script cleans its temporary resources
on success, failure and handled signals; GitHub discards the VM after the job,
including resources left by a forced termination.

Only `contents: read` is granted. Checkout is pinned to the official v7.0.1 commit
and does not persist credentials. Tests require no repository secrets or cloud
account. Fork PRs use `pull_request`, not `pull_request_target`; GitHub may require
maintainer approval before a contributor's workflow can run. No deploy, publish,
frontend or broker jobs exist yet; those additions belong to T54-B or later work.

## Require the check before merging

After a successful hosted run, a repository administrator can open **Settings →
Rules → Rulesets**, create or edit an active branch ruleset targeting `main`,
require pull requests and required status checks, then select **Repository
validation** (GitHub Actions). Require up-to-date branches if desired and review
bypass permissions. Availability depends on repository visibility and plan.
The workflow alone does not restrict merges or direct pushes; this task does not
change repository rules. If merge queues are introduced, add a `merge_group`
trigger before requiring this check for queued merges.

## Local verification and maintenance

Run `make check-all`, or `DOCKER_CONTEXT=desktop-linux make check-all` for Docker
Desktop. Lint changes to the workflow independently:

```sh
docker run --rm -i rhysd/actionlint:1.7.7 -color - < .github/workflows/ci.yml
```

Keep checkout pinned to a reviewed full commit SHA when upgrading. Preserve the
job name when editing the workflow or update required-check rules at the same
time. Build caches and separate jobs can be introduced when measured runtime
justifies them; currently local and hosted checks use the same entry point.

## Hosted verification — 2026-10-25

Workflow commit `3ffe8f8` passed both its
[push run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40239902511)
and [pull-request run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240024178).
A test added only to the disposable `t54/ci-failure-probe` branch caused the
[expected failed check](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240098031):
`TestCIFailureProbe` failed and Make exited 2. Its
[preceding run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240082449)
was cancelled when superseded. No intentional failure is included in PR #1.
Local actionlint and the full Docker suite also passed.
