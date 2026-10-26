# The scripted demo

A recorded demo of supatree that is rebuilt from scripts, so it never shows a
supatree that no longer exists (plan: `plans/demo.md`, issue #48). The real
`supatree` runs — watcher, sidebar, dashboard, MCP server, zellij, git, nono —
in an isolated sandbox where only the outside world is faked:

- **GitHub** is `fakegh/`, installed as `gh`. It answers the calls supatree
  makes from `~/demo/gh/state.json`, where each pull request has a timeline
  (checks pending → passing → approved → merged). Anything else fails and is
  logged to `unsupported.log`.
- **The agents** are `actor/`, installed as `claude`. Each plays a script from
  `actors/<scene>/` — say, think, wait for input, run a command, call a
  supatree MCP tool for real — so asking the PM for a review really creates
  the review tree and the watcher really opens the reviewer's tab.
- **The repos** are `acme/api`, `acme/web` and `acme/docs`, with
  `git@github.com:acme/…` URLs that a fake ssh (`core.sshCommand`) serves from
  local bare repos.

## Running it

```sh
make demo                 # every tape in tapes/, into demo/out/
make demo TAPE=pm-review  # one
```

This needs [VHS](https://github.com/charmbracelet/vhs) (with ttyd and
ffmpeg), zellij and nono, and must run from a normal terminal: on macOS the
agents' nono cannot start inside another nono sandbox, such as a supatree
agent's. CI records every tape on release tags (the `demo` workflow) and
attaches them to the release; to record from a branch, run the workflow by
hand (`gh workflow run demo.yml --ref <branch>`, optionally `-f tape=<name>`).
The recordings are the run's `demo` artifact.

To poke at a scene by hand:

```sh
demo/setup.sh pm-review
eval "$(demo/setup.sh env)"
supatree start demo
```

## Adding a scene

1. `actors/<scene>/` — a script per agent that speaks in it: `pm.yml` for the
   top PM, `pm-<name>.yml` for a named PM, `<tree>.<agent>.yml` for a tree
   agent. `actors/common/` holds scripts shared by several scenes; a scene's
   own script of the same name wins. Anything without a script idles. A
   `setup.sh` there runs after the sandbox is built, for state the scene
   starts from.
2. `tapes/<scene>.tape` — the VHS script. Wait on screen text
   (`Wait+Screen /…/`), never on fixed sleeps, so timing does not change what
   is captured.

Step types (`actor/main.go`): `say`, `think: 2s`, `input: true` (waits for a
line, kept as `${input}`), `tool: <name>` with `args` (result kept as
`${result}`), `run: <shell>` (sees `$input` and `$result` in its environment),
`sleep`, and `wait_request: true` (a PM waiting for its team).
