# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues. Use the `gh` CLI for all operations.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`.
- **Read an issue**: `gh issue view <number> --comments`, including labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,assignees` with appropriate `--label` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`.
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`.
- **Close**: `gh issue close <number> --comment "..."`.

Infer the repository from `git remote -v`; `gh` does this automatically inside the clone.

## Pull requests as a triage surface

**PRs as a request surface: no.**

## Wayfinding operations

- **Map**: one issue labelled `wayfinder:map`, containing the destination, notes, decisions, fog, and scope boundaries.
- **Child ticket**: create an issue labelled `wayfinder:research`, `wayfinder:prototype`, `wayfinder:grilling`, or `wayfinder:task`, then link it to the map as a GitHub sub-issue. If sub-issues are unavailable, add `Part of #<map>` to the child body and list it in the map.
- **Blocking**: use GitHub native issue dependencies. If unavailable, use a `Blocked by: #<n>` line in the child body.
- **Frontier**: open map children with neither an open blocker nor an assignee; first in map order wins.
- **Claim**: assign the issue to the driving developer before work begins.
- **Resolve**: post the resolution as a comment, close the ticket, then add one context pointer to the map's Decisions so far section.
