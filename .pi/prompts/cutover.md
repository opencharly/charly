---
description: Start a hard cutover: fetch, branch, worktree, load skills, plan
argument-hint: "<slug>"
---
# Cutover: $1

## Setup

1. `git fetch origin --prune --tags`
2. `git merge --ff-only origin/main`
3. Create worktree: use `charly_worktree_create` with slug "$1"
4. Load skills matching the change using the AGENTS.md dispatcher + `charly_load_skills`
5. Review the applicable rules: R0–R10, RDD, ADE, SDD

## Plan

- Scope: what changes are in this cutover?
- RDD: what high-risk assumptions need a disposable bed before editing?
- Beds: which `charly check run <bed>` targets prove the change?
- CHANGELOG: what CalVer placeholder will the entry use?

## After completion

- `charly_worktree_remove` with slug "$1" to clean up