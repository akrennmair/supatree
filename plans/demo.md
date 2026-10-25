# A scripted demo, rebuilt on every change

Issue #48.

## Goal

A realistic visual demo of supatree: a stack of a frontend, a backend and a
docs repo, and the things people do with it — create a tree, run a PM, review
pull requests through the PM, open and close review trees. The data is fake,
the program is not: the demo is captured from scripts the way a Playwright run
captures a web app, so `make demo` rebuilds it after every change and it never
shows a supatree that no longer exists.

## Approach

Run the real `supatree` in an isolated sandbox where only the outside world is
faked — GitHub and the AI — and record a scripted terminal session of it.

| Real | Faked |
|---|---|
| `supatree`, its watcher, sidebar, dashboard, MCP server | `gh` (GitHub), from fixture JSON and a timeline |
| zellij, git, nono | the agent CLI (`claude`), as a scripted actor |
| the stack, trees, worktrees, mailboxes, launch queue | the repos' remotes, as local repos behind `url.insteadOf` |

Because the actors make real MCP tool calls, asking the PM for a review
really creates a review tree, the watcher really opens its tab, and the sidebar
updates on its own. A change to any of that changes the demo.

### What exists

- **`scripts/ux-env.sh`** builds supatree into an isolated HOME, XDG
  directories and zellij socket, seeds fixture repos, a stack with dependency
  edges and trees in a spread of states, installs a fake `claude`, and drives
  zellij (`keys`, `focus`, `shot`). The demo sandbox is this with different
  fixtures.
- **`plans/sidebar-ux-testing.md`** already plans fake binaries on PATH (`gh`,
  zellij, nono) for hermetic sidebar tests. The fake `gh` here is the same
  idea, grown a timeline.
- **`scripts/e2e-supatree.sh`** proves the whole lifecycle, nono included,
  runs on a Linux CI runner.

## Pieces

### 1. Recording — VHS

[VHS](https://github.com/charmbracelet/vhs) runs a terminal session in a
headless browser terminal (ttyd + xterm.js) from a `.tape` script — `Type`,
`Enter`, `Sleep`, `Wait /regex/`, `Screenshot` — and writes GIF, MP4 or WebM.
It also writes plain-text screens, so the same tapes can serve as golden
layout tests later. Dependencies: ttyd and ffmpeg.

Fallback, if zellij misbehaves under VHS: Playwright against ttyd directly.
More code, finer control.

### 2. Fixtures — a GitHub-shaped stack

```yaml
members:
  api:  git@github.com:acme/api.git    # backend
  web:  git@github.com:acme/web.git    # frontend
  docs: git@github.com:acme/docs.git
deps:
  web: [api]
  docs: [api]
```

The remotes are local repos under the sandbox, with realistic history and
code, reached through the sandbox's own git config:

```
[url "file:///…/demo/remotes/acme/"]
    insteadOf = git@github.com:acme/
```

Supatree keys its cache and matches review PRs by `host/owner/repo`, so it sees
`github.com/acme/web` and `acme/web#42` exactly as it would in real use. Each
fixture PR is a branch plus `refs/pull/<n>/head` in its remote, so review trees
fetch real heads.

### 3. Fake `gh`

A script on PATH that answers the calls supatree makes:

| Call | Where | Answer |
|---|---|---|
| `gh api -i repos/<o>/<r>/pulls?…` | workbench `pkg/github/poll.go` | the PR list, with ETag headers |
| `gh pr view <n> --repo … --json …` | `review.go` (`lookupPR`) | one PR |
| `gh pr create …` | `mcp.go` (`create_pr(s)`), `stack.go` | adds a PR to the fixture state, prints its URL |
| `gh api repos/<o>/<r>/pulls/<n>/reviews --method POST` | `post.go` (`review_post`) | records the review, answers with its URL |
| anything else | | fails loudly, so a new call is noticed |

PR state comes from `demo/fixtures/prs.json`, plus a **timeline**: each PR can
list states it moves through on a clock (checks pending → passing → approved
→ merged), so the sidebar visibly progresses during a tape.

### 4. Scripted actors

The fake `claude` becomes an actor that plays a script per agent
(`demo/actors/<tree>-<agent>.yml`, the PM's as `pm.yml`):

- **say** — prints a reply in the agent's pane at a readable typing speed;
- **wait** — for a line typed at it (the human's turn), or for mail;
- **tool** — makes a real MCP tool call by running `supatree mcp` with the
  agent's environment (`new_tree`, `start_agent`, `message_agent`,
  `rename_branches`, `create_prs`, `review_post`, …) and prints a short
  tool-call line, the way Claude Code shows one;
- **run** — a shell step in the tree, for edits and commits.

It still answers `claude mcp list` for `supatree init`. Actors call tools
themselves rather than having text typed into their panes, which avoids the
`SubmitToPane` timing that bit Codex (workbench#45).

### 5. Determinism

- Every tree is named explicitly (`--name`), never a random city.
- The fake `gh` timeline is driven by its own clock, started by the tape.
- Tapes `Wait` for text on screen, not fixed sleeps, so the watcher's 2s ticks
  and launch latency do not change what is captured.
- Relative times ("3m ago") vary between runs; accept that, or add a
  test-only clock override if it shows in diffs.

## Scenarios

One `.tape` each, under `demo/tapes/`:

| Tape | Shows |
|---|---|
| `tour` | the sidebar with trees in every state — fresh, work in progress, PRs open, CI passing, approved, merged — then `supatree dash` |
| `new-tree` | create a tree for a feature across `api` and `web`; its agent edits and commits in both, renames the branch, `create_prs` in dependency order; the sidebar shows the PRs move through CI to approved |
| `pm-review` | ask the PM to review `acme/api#17` and `acme/web#42`; it creates a review tree and starts a reviewer; the reviewer posts its review; the PM reports back |
| `new-pm` | `supatree pm new docs`, a second PM that owns the docs repo |
| `close-review` | close the review trees; `history` counts them as reviewed |

## Layout

```
demo/
  setup.sh           # builds the sandbox: remotes, stack, trees, fakes (reuses ux-env.sh helpers)
  bin/gh             # fake gh
  bin/claude         # actor
  fixtures/          # repo contents, prs.json, timeline
  actors/            # one script per agent and PM
  tapes/*.tape
  out/               # gitignored: gif, mp4, webm, png, txt
```

`make demo` runs every tape; `make demo TAPE=pm-review` runs one.

## Publishing

A `demo` workflow on pushes to `main` and on `v*` tags runs `make demo` on
Linux, uploads `demo/out/` as an artifact, attaches the videos to the release
on tags, and publishes them where the README can embed the current one (a
`demo` branch or GitHub Pages).

## Risks

- **zellij under VHS.** VHS handles full-screen TUIs, but zellij is heavier
  than most; the spike settles it, with Playwright + ttyd as the fallback.
- **nono reading fixture remotes.** Fetches run under nono; the sandbox profile
  must reach `demo/remotes`. `ux-env.sh` already clones from paths in its HOME,
  so this is expected to hold.
- **Nested sandboxes.** On macOS agent tabs cannot start inside another nono
  sandbox, so the demo is recorded from a normal terminal or CI, not by an
  agent working in a supatree.

## Order of work

1. **Spike: `pm-review` end to end** — VHS + zellij, fake `gh` with
   `insteadOf` remotes, one actor making a real MCP call, the watcher launching
   a tab. It exercises every risk above.
2. Fixtures and actors for the remaining tapes.
3. `make demo` and the CI workflow.
4. Embed the current demo in the README.
5. Later: plain-text outputs of the tapes as golden tests in CI.
