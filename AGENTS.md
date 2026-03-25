# AGENTS.md

Repository-local guidance for Codex and similar coding agents.

## Scope

These instructions apply to the entire repository.

## Docs Policy

When a change affects behavior, user workflows, architecture, testing, or operator workflow, update the relevant docs in the same branch before opening a PR.

This includes reviewing and updating:
- `README.md` for current user-visible behavior, capabilities, limitations, and workflows
- design or architecture docs when implementation meaningfully changes the described system
- test or harness docs when new test flows, scripts, or manual procedures are added
- planning docs when they are intended to track the current implementation state

Do not assume existing docs are still correct after code changes.

## PR Checklist

Before pushing or creating a PR, explicitly check:
- does `README.md` still describe the current product behavior and current limitations?
- do any implementation/design docs need updates to match the shipped behavior?
- do new commands, slash commands, prompts, flows, or test harnesses need documentation?
- are stale statements from earlier phases still present and now misleading?

If the answer to any of these is yes, update the docs before creating the PR.

## Working Norms

- Keep changes behavior-preserving when doing pure refactors.
- Prefer focused follow-up branches for new feature slices instead of extending already large PRs.
- Leave the worktree clean before handing off for review unless the user explicitly asks otherwise.
