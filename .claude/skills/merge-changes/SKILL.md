---
name: merge-changes
description: Merge a vimail PR or branch into main with pre-merge checks, then propagate the change into CHANGELOG.md and README.md in the same flow. Use when the user asks to merge, land, or ship a PR/branch/stack, or after a PR is approved — including stacked PRs based on other PR branches.
---

# Merge Changes

Merging is not done until the change is reflected in `CHANGELOG.md` and (when user-facing) `README.md`. Never merge without running the propagation step.

## Workflow

1. **Identify the PR**
   `gh pr view <n> --json title,baseRefName,headRefName,state,mergeable,mergeStateStatus,closingIssuesReferences`

2. **Stack check**
   If `baseRefName` is not `main`, the PR is stacked on another PR. Never merge a child before its parent — find the bottom of the stack (the PR whose base is `main`) and merge bottom-up. After each merge with branch deletion, GitHub retargets the next PR in the stack to `main`; verify the retarget with `gh pr view` before merging it.

3. **Gate**
   - `gh pr checks <n>` — all required checks green (not pending, not failed).
   - `mergeable` must be `MERGEABLE`; if the branch is behind or conflicted, update/rebase it, let CI rerun, then re-gate.
   - Never bypass a failing check; report the failure instead.

4. **Merge**
   `gh pr merge <n> --squash --delete-branch`
   Repo convention: squash to a single conventional commit; keep the `fix:` / `feat:` / `perf:` prefix from the PR title as the squash commit title.

5. **Propagate to docs** (on `main`, after `git pull`)
   - **CHANGELOG.md** — add one bullet under `## [Unreleased]` in the category mapped from the commit prefix: `feat` → Added, `fix` → Fixed, `perf`/`refactor` → Changed, `security` → Security, breaking changes → Changed with a **Breaking:** prefix. Write it from the user's perspective (what behaves differently), not as a copy of the commit message. End with `(#<PR>)`.
   - **README.md** — scan the merged diff for user-facing surface: new subcommands, config keys, keybindings, commands (`:something`), themes, or behavior README already documents. Update the matching sections. If nothing user-facing changed, skip README and state that explicitly in the docs commit body.
   - Commit directly to `main` as `docs: changelog and readme for #<n>` and push.

6. **Close out**
   Confirm linked issues were auto-closed (`closingIssuesReferences`); close and comment manually on any that were not.

## Merging a whole stack

When landing several PRs (or an entire stack) in one session, merge them in order (steps 1–4 per PR), then do **one** propagation pass at the end: a single `docs:` commit whose CHANGELOG bullets cover every merged PR and whose README updates reflect the combined result. This avoids N churn commits on `main`.

## CHANGELOG conventions

Keep a Changelog format (`Added` / `Changed` / `Fixed` / `Security` under `[Unreleased]`). On release, rename `[Unreleased]` to the version + date and open a fresh empty `[Unreleased]` section. Entries are short, plain-language, and mention the PR number — a reader should understand the change without opening the PR.

## Hard rules

- No merge with failing or pending required checks.
- No stacked child merged before its parent.
- No merge left without a CHANGELOG entry (except pure `docs:`/`chore:` merges with no behavior change — those may skip).
- The docs commit goes to `main` only after the merge commit exists, never bundled into the feature PR.
