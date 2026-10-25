#!/usr/bin/env bash
set -euo pipefail

# Builds the demo sandbox: an isolated HOME where the real supatree runs
# against a fake GitHub (demo/fakegh, as `gh`) and scripted agents
# (demo/actor, as `claude`), with a GitHub-shaped stack — acme/api, acme/web,
# acme/docs — whose remotes are local bare repos, reached through a fake ssh.
#
#   demo/setup.sh <scenario>        wipe and rebuild the sandbox for a tape
#   eval "$(demo/setup.sh env)"     point this shell at it
#
# <scenario> picks the agents' scripts (demo/actors/<scenario>/, over
# demo/actors/common/) and any extra state the tape starts from.

REPO=$(cd "$(dirname "$0")/.." && pwd)
DEMO_DIR=${DEMO_DIR:-/tmp/supatree-demo}
DEMO_DIR=${DEMO_DIR%/}
# Short and fixed: a unix socket path is capped near 104 bytes.
DEMO_SOCKETS=${DEMO_SOCKETS:-/tmp/stdemo.$(id -u)}

env_lines() {
    cat <<EOF
export HOME="$DEMO_DIR/home"
export XDG_CONFIG_HOME="$DEMO_DIR/home/.config" XDG_STATE_HOME="$DEMO_DIR/home/.local/state" XDG_CACHE_HOME="$DEMO_DIR/home/.cache"
export ZELLIJ_SOCKET_DIR="$DEMO_SOCKETS"
export PATH="$DEMO_DIR/home/.local/bin:\$PATH"
export TERM="\${TERM:-xterm-256color}"
export NONO_AUTO_MIGRATE=1
unset ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID SUPATREE SUPATREE_PM SUPATREE_NAME SUPATREE_ROOT SUPATREE_AGENT
# An agent's own git settings (a supatree agent's hardening rides in
# GIT_CONFIG_*) must not leak into the sandbox's git.
for v in \$(env | sed -n 's/^\(GIT_CONFIG_[A-Z_0-9]*\)=.*/\1/p'); do unset "\$v"; done
unset GIT_NO_REPLACE_OBJECTS
EOF
}

if [ "${1:-}" = env ]; then env_lines; exit 0; fi
SCENARIO=${1:?usage: demo/setup.sh <scenario> | env}
[ -d "$REPO/demo/actors/$SCENARIO" ] || { echo "no scenario $SCENARIO (demo/actors/$SCENARIO)" >&2; exit 2; }

# Build before touching HOME: the Go toolchain lives in the real one.
mkdir -p "$DEMO_DIR"
BUILD=$(mktemp -d "$DEMO_DIR/build.XXXXXX")
(cd "$REPO" && GOWORK=off go build -o "$BUILD/supatree" ./cmd/supatree \
    && GOWORK=off go build -o "$BUILD/gh" ./demo/fakegh \
    && GOWORK=off go build -o "$BUILD/claude" ./demo/actor)

# Wipe the old sandbox, its zellij session included.
if [ -d "$DEMO_DIR/home" ]; then
    (eval "$(env_lines)"
        zellij kill-all-sessions -y >/dev/null 2>&1 || true
        zellij delete-all-sessions -y -f >/dev/null 2>&1 || true)
fi
rm -rf "$DEMO_DIR/home"
mkdir -p "$DEMO_DIR/home/.local/bin" "$DEMO_SOCKETS"
mv "$BUILD"/* "$DEMO_DIR/home/.local/bin/"
rmdir "$BUILD"
eval "$(env_lines)"

D="$HOME/demo"
REMOTES="$D/remotes/acme"
mkdir -p "$REMOTES" "$D/gh" "$D/actors" "$XDG_CONFIG_HOME/zellij"

git config --global user.name "You"
git config --global user.email you@acme.dev
git config --global init.defaultBranch main
git config --global advice.detachedHead false
# The remotes keep their GitHub URLs everywhere — supatree reads them back
# from git, so an insteadOf rewrite would show — and git reaches them through
# an ssh that serves the local bare repos. Real ssh still answers `ssh -G`.
cat > "$HOME/.local/bin/demo-ssh" <<EOF
#!/bin/sh
# git runs: demo-ssh [options] git@github.com "git-upload-pack 'acme/api.git'"
for last; do :; done
exec sh -c "\$(printf '%s' "\$last" | sed "s#'acme/#'$REMOTES/#")"
EOF
chmod +x "$HOME/.local/bin/demo-ssh"
git config --global core.sshCommand "$HOME/.local/bin/demo-ssh"
git config --global ssh.variant simple

cat > "$XDG_CONFIG_HOME/zellij/config.kdl" <<'EOF'
show_startup_tips false
show_release_notes false
simplified_ui true
EOF

# ---------------------------------------------------------------- fixtures

WORK="$D/work"
mkdir -p "$WORK"

# commit_as <name> <email> <message>, in the current directory.
commit_as() {
    git add -A
    GIT_AUTHOR_NAME=$1 GIT_AUTHOR_EMAIL=$2 GIT_COMMITTER_NAME=$1 GIT_COMMITTER_EMAIL=$2 \
        git commit -qm "$3"
}
MIRA=("Mira Chen" mira@acme.dev)
JONAS=("Jonas Weber" jonas@acme.dev)
PRIYA=("Priya Patel" priya@acme.dev)

new_remote() {
    git init -q --bare "$REMOTES/$1.git"
    git clone -q "git@github.com:acme/$1.git" "$WORK/$1" 2>/dev/null
}

# push_pr <repo> <branch> <number>: publish a branch and the pull ref GitHub
# would make for it, so review trees can fetch the PR's head.
push_pr() {
    git -C "$WORK/$1" push -q origin "$2"
    git -C "$WORK/$1" push -q origin "$2:refs/pull/$3/head"
}

new_remote api
cd "$WORK/api"
mkdir -p cmd/api internal/orders internal/http
cat > go.mod <<'EOF'
module github.com/acme/api

go 1.27
EOF
cat > cmd/api/main.go <<'EOF'
package main

import (
	"log"
	"net/http"

	"github.com/acme/api/internal/orders"
)

func main() {
	mux := http.NewServeMux()
	orders.Register(mux, orders.NewStore())
	log.Fatal(http.ListenAndServe(":8080", mux))
}
EOF
cat > internal/orders/orders.go <<'EOF'
// Package orders serves the /v1/orders API.
package orders

import (
	"encoding/json"
	"net/http"
)

type Order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Total  int    `json:"total_cents"`
}

func Register(mux *http.ServeMux, s *Store) {
	mux.HandleFunc("GET /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(s.List())
	})
	mux.HandleFunc("POST /v1/orders/{id}/checkout", func(w http.ResponseWriter, r *http.Request) {
		o, err := s.Checkout(r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	})
}
EOF
cat > internal/orders/store.go <<'EOF'
package orders

import (
	"errors"
	"sync"
)

type Store struct {
	mu     sync.Mutex
	orders map[string]*Order
}

func NewStore() *Store { return &Store{orders: map[string]*Order{}} }

func (s *Store) List() []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Order, 0, len(s.orders))
	for _, o := range s.orders {
		out = append(out, *o)
	}
	return out
}

func (s *Store) Checkout(id string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return nil, errors.New("no such order")
	}
	o.Status = "paid"
	return o, nil
}
EOF
printf '# acme/api\n\nThe orders backend. `go run ./cmd/api`\n' > README.md
commit_as "${JONAS[@]}" "orders: list and checkout endpoints"
git push -q origin main

git checkout -qb jonas/rate-limit
cat > internal/http/ratelimit.go <<'EOF'
// Package http holds middleware shared by every API handler.
package http

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimit allows each client limit requests per window, answering 429 with
// Retry-After once it is spent.
func RateLimit(next http.Handler, limit int, window time.Duration) http.Handler {
	var mu sync.Mutex
	seen := map[string][]time.Time{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Api-Key")
		now := time.Now()
		mu.Lock()
		recent := seen[key][:0]
		for _, t := range seen[key] {
			if now.Sub(t) < window {
				recent = append(recent, t)
			}
		}
		if len(recent) >= limit {
			mu.Unlock()
			w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		seen[key] = append(recent, now)
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
EOF
commit_as "${JONAS[@]}" "http: per-key rate limiting middleware"
sed -i.bak 's|log.Fatal(http.ListenAndServe(":8080", mux))|log.Fatal(http.ListenAndServe(":8080", apihttp.RateLimit(mux, 60, time.Minute)))|' cmd/api/main.go
sed -i.bak 's|"net/http"|"net/http"\n\t"time"\n\n\tapihttp "github.com/acme/api/internal/http"|' cmd/api/main.go
rm -f cmd/api/main.go.bak
commit_as "${JONAS[@]}" "api: rate limit every route at 60/min per key"
push_pr api jonas/rate-limit 17
git checkout -q main

new_remote web
cd "$WORK/web"
mkdir -p src/checkout src/api
cat > package.json <<'EOF'
{
  "name": "@acme/web",
  "private": true,
  "scripts": { "dev": "vite", "build": "vite build", "test": "vitest" },
  "dependencies": { "react": "^19.1.0", "react-dom": "^19.1.0" },
  "devDependencies": { "vite": "^7.0.0", "vitest": "^3.2.0", "typescript": "^5.9.0" }
}
EOF
cat > src/api/client.ts <<'EOF'
export type Order = { id: string; status: string; total_cents: number };

export async function checkout(id: string): Promise<Order> {
  const res = await fetch(`/v1/orders/${id}/checkout`, { method: "POST" });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}
EOF
cat > src/checkout/Checkout.tsx <<'EOF'
import { useState } from "react";
import { checkout } from "../api/client";

export function Checkout({ orderId }: { orderId: string }) {
  const [error, setError] = useState<string | null>(null);
  const [paid, setPaid] = useState(false);

  async function pay() {
    try {
      await checkout(orderId);
      setPaid(true);
    } catch (e) {
      setError(String(e));
    }
  }

  if (paid) return <p className="ok">Thanks — your order is on its way.</p>;
  return (
    <div>
      <button onClick={pay}>Pay now</button>
      {error && <p className="error">{error}</p>}
    </div>
  );
}
EOF
printf '# acme/web\n\nThe storefront. `npm run dev`\n' > README.md
commit_as "${MIRA[@]}" "checkout: pay button and order client"
git push -q origin main

git checkout -qb mira/rate-limit-errors
cat > src/api/client.ts <<'EOF'
export type Order = { id: string; status: string; total_cents: number };

export class RateLimited extends Error {
  constructor(public retryAfter: number) {
    super(`Too many requests — try again in ${retryAfter}s`);
  }
}

export async function checkout(id: string): Promise<Order> {
  const res = await fetch(`/v1/orders/${id}/checkout`, { method: "POST" });
  if (res.status === 429) {
    throw new RateLimited(Number(res.headers.get("Retry-After") ?? 60));
  }
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}
EOF
commit_as "${MIRA[@]}" "client: surface 429 as RateLimited with Retry-After"
sed -i.bak 's|      setError(String(e));|      setError(e instanceof RateLimited ? e.message : "Payment failed, please retry.");|; s|import { checkout } from "../api/client";|import { checkout, RateLimited } from "../api/client";|' src/checkout/Checkout.tsx
rm -f src/checkout/Checkout.tsx.bak
commit_as "${MIRA[@]}" "checkout: friendly message when rate limited"
push_pr web mira/rate-limit-errors 42
git checkout -q main

new_remote docs
cd "$WORK/docs"
mkdir -p api guides
printf '# Acme developer docs\n\n- [Orders API](api/orders.md)\n- [Getting started](guides/getting-started.md)\n' > README.md
cat > api/orders.md <<'EOF'
# Orders API

## GET /v1/orders

Lists the caller's orders.

## POST /v1/orders/{id}/checkout

Pays for an order. Answers `409` if the order cannot be paid.
EOF
printf '# Getting started\n\nCreate an API key in the dashboard and send it as `X-Api-Key`.\n' > guides/getting-started.md
commit_as "${PRIYA[@]}" "docs: orders API and getting started"
git push -q origin main
cd "$D"

# ---------------------------------------------------------------- supatree

supatree init >/dev/null
# Agents, and the git supatree runs for trees, use the demo's own profile. It
# builds on nono's built-in go-dev rather than supatree-agent, whose base is
# Claude Code's registry pack: the actors are not Claude, and a fresh HOME has
# no packs. Beyond the base it grants what supatree-agent would (supatree's
# config, its agent cache) plus the fakes, the git config that routes ssh to
# the fake remotes, and the fake remotes and GitHub themselves.
# git aborts when it cannot read a system config that exists (CI runners have
# one), so grant it where it is there.
SYSTEM_GITCONFIG=""
[ -f /etc/gitconfig ] && SYSTEM_GITCONFIG=', "/etc/gitconfig"'
cat > "$HOME/.config/nono/profiles/supatree-demo.json" <<EOF
{
  "extends": ["go-dev"],
  "meta": { "name": "supatree-demo", "description": "supatree demo agents: scripted actors, fake remotes and GitHub" },
  "filesystem": {
    "allow": ["$D", "$XDG_CACHE_HOME/supatree/agent"],
    "read": ["$HOME/.local/bin", "$XDG_CONFIG_HOME/supatree"],
    "read_file": ["$HOME/.gitconfig"$SYSTEM_GITCONFIG]
  }
}
EOF
CONFIG="$XDG_CONFIG_HOME/supatree/config.yml"
sed -i.bak 's/nono_profile: supatree-agent/nono_profile: supatree-demo/' "$CONFIG"
rm -f "$CONFIG.bak"
# A wider sidebar than the default 25%, so PR numbers are not cut off.
cat >> "$CONFIG" <<'EOF'
watch_interval: 5s
sidebar_width: 30%
EOF

supatree scaffold acme --repos="api=git@github.com:acme/api.git,web=git@github.com:acme/web.git,docs=git@github.com:acme/docs.git" >/dev/null
STACK="$HOME/supatree/stacks/acme"
cat > "$STACK/supatree.yml" <<'EOF'
members:
    api: git@github.com:acme/api.git
    docs: git@github.com:acme/docs.git
    web: git@github.com:acme/web.git
deps:
    docs: [api]
    web: [api]
EOF
git -C "$STACK" commit -qam "web and docs build on api"

NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
PRS=()
# pr <repo> <number> <author> <head> <sha> <title> <timeline json>
pr() {
    PRS+=("{\"repo\":\"acme/$1\",\"number\":$2,\"author\":\"$3\",\"head\":\"$4\",\"base\":\"main\",\"head_sha\":\"$5\",\"title\":\"$6\",\"created\":\"$NOW\",\"timeline\":$7}")
}
sha() { git -C "$1" rev-parse HEAD; }

# tree <name>: a tree named for its work, so its branches are st/<name>/<alias>.
tree() {
    supatree new --stack acme --name "$1" >/dev/null
}
# change <tree> <member> <file> <line> <message>: commit and push a change.
change() {
    local wt="$HOME/supatree/trees/$1/repos/$2"
    mkdir -p "$(dirname "$wt/$3")"
    echo "$4" >> "$wt/$3"
    git -C "$wt" add -A
    git -C "$wt" commit -qm "$5"
    git -C "$wt" push -q origin HEAD
}

tree checkout-retry
change checkout-retry api internal/orders/store.go "// Checkout is idempotent: paying a paid order is not an error." "orders: idempotent checkout"
change checkout-retry web src/checkout/Checkout.tsx "// Retries once on a network error." "checkout: retry once on network errors"
pr api 15 you st/checkout-retry/api "$(sha "$HOME/supatree/trees/checkout-retry/repos/api")" "Idempotent checkout" '[{"at":0,"checks":"success","review":"APPROVED"}]'
pr web 39 you st/checkout-retry/web "$(sha "$HOME/supatree/trees/checkout-retry/repos/web")" "Retry checkout on network errors" '[{"at":0,"checks":"success","review":"APPROVED"}]'

tree order-export
change order-export api internal/orders/export.go "package orders // CSV export of a merchant's orders." "orders: CSV export"
pr api 16 you st/order-export/api "$(sha "$HOME/supatree/trees/order-export/repos/api")" "CSV export of orders" '[{"at":0,"checks":"success","review":"APPROVED"}]'

tree search-filters
change search-filters web src/search/Filters.tsx "export const filters = ['status', 'date', 'total'];" "search: status, date and total filters"
change search-filters docs guides/search.md "# Searching orders" "docs: searching orders"
pr web 41 you st/search-filters/web "$(sha "$HOME/supatree/trees/search-filters/repos/web")" "Search filters" '[{"at":0,"checks":"pending","review":"REVIEW_REQUIRED"}]'
pr docs 8 you st/search-filters/docs "$(sha "$HOME/supatree/trees/search-filters/repos/docs")" "Document order search" '[{"at":0,"checks":"success","review":"REVIEW_REQUIRED"}]'

supatree new --stack acme --name lisbon >/dev/null

# The pull requests the PM is asked to review.
pr api 17 jonas jonas/rate-limit "$(git -C "$WORK/api" rev-parse jonas/rate-limit)" "Rate limit every route at 60/min per key" '[{"at":0,"checks":"success","review":"REVIEW_REQUIRED"}]'
pr web 42 mira mira/rate-limit-errors "$(git -C "$WORK/web" rev-parse mira/rate-limit-errors)" "Friendly checkout errors when rate limited" '[{"at":0,"checks":"success","review":"REVIEW_REQUIRED"}]'

{
    printf '{\n  "new_pr_timeline": [{"at":0,"checks":"pending","review":"REVIEW_REQUIRED"},{"at":20,"checks":"success"},{"at":40,"review":"APPROVED"}],\n  "prs": [\n'
    for i in "${!PRS[@]}"; do
        [ "$i" -gt 0 ] && printf ',\n'
        printf '    %s' "${PRS[$i]}"
    done
    printf '\n  ]\n}\n'
} > "$D/gh/state.json"

# The watcher's ledger is what `history` and the dashboard's feed read: give
# it a round with checkout-retry approved, then one after its PRs merged.
supatree watch --once >/dev/null 2>&1 || true
gh demo merge acme/api 15
gh demo merge acme/web 39
supatree watch --once >/dev/null 2>&1 || true

# ---------------------------------------------------------------- scenario

cp "$REPO"/demo/actors/common/*.yml "$D/actors/" 2>/dev/null || true
cp "$REPO/demo/actors/$SCENARIO"/*.yml "$D/actors/" 2>/dev/null || true
if [ -x "$REPO/demo/actors/$SCENARIO/setup.sh" ]; then
    (cd "$REPO" && "demo/actors/$SCENARIO/setup.sh")
fi

echo "demo sandbox ready: $DEMO_DIR ($SCENARIO)"
