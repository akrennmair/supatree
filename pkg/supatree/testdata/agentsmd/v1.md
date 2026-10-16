# Supatree agent guide

This is a supatree: a set of git worktrees, one per repo, for a single
cross-repo issue. When opened, you run at the supatree root and each member repo
is checked out under `repos/<alias>/`.

Read `.supatree/info.md` for the current member list, branches, and merge order.

## Conventions

- Commit in each repo's worktree separately (`cd repos/<alias>`).
- Give the shared branch slug a meaningful name before any PR:
  `rename_branches` MCP tool, or `supatree rename-branch <slug>`.
- Open PRs with the `create_pr` / `create_prs` MCP tools (dependency-ordered,
  cross-linked) — not with bare `gh pr create`.
- To change the repo set, edit `supatree.yml` then run the `sync` tool.

Add your issue-specific instructions below.
