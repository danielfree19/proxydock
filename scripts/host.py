#!/usr/bin/env python3
"""Tiny CLI for the manager-api proxy-host endpoints.

Designed to be called from the top-level Makefile. Auth comes from
the ADMIN_TOKEN env var; the manager URL from MANAGER_URL.

Subcommands:
  add NAME --domain D --upstream U [--protocol http|tcp|udp]
                                   [--entry-points web,websecure]
                                   [--tls] [--forward-auth URL]
                                   [--no-publish]
  rm NAME [--no-publish]
  publish
  list
  hosts-sync       # write/refresh a managed block in /etc/hosts (needs root)
  hosts-clear      # remove the managed block from /etc/hosts (needs root)
  agent-config AGENT_ID [--raw]
                   # show what an agent receives from /config (post-label-filter)
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request

ETC_HOSTS = "/etc/hosts"
HOSTS_BEGIN = "# >>> proxydock demo (managed) >>>"
HOSTS_END = "# <<< proxydock demo (managed) <<<"


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, default)


def req(method: str, path: str, body: dict | None = None) -> dict | None:
    base = env("MANAGER_URL", "http://localhost:8090").rstrip("/")
    url = f"{base}{path}"
    headers = {"Authorization": f"Bearer {env('ADMIN_TOKEN', 'demo-admin')}"}
    data = None
    if body is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode()
    r = urllib.request.Request(url, method=method, headers=headers, data=data)
    try:
        with urllib.request.urlopen(r) as resp:
            payload = resp.read()
            if not payload:
                return None
            return json.loads(payload)
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"{method} {url} -> {e.code}\n")
        sys.stderr.write(e.read().decode(errors="replace") + "\n")
        sys.exit(1)


def cmd_add(args: argparse.Namespace) -> None:
    fleet = env("FLEET", "homelab")
    middlewares = []
    if args.forward_auth:
        # Default to oauth2-proxy's response-header convention. Users
        # can edit later via the UI if they're running vouch / TFA.
        middlewares.append({
            "name": "oidc",
            "type": "forwardAuth",
            "config": {
                "address": args.forward_auth,
                "trustForwardHeader": True,
                "authResponseHeaders": [
                    "X-Auth-Request-User",
                    "X-Auth-Request-Email",
                    "X-Auth-Request-Groups",
                ],
            },
        })
    body = {
        "name": args.name,
        "protocol": args.protocol,
        "domain": args.domain,
        "upstream_url": args.upstream,
        "entry_points": [s.strip() for s in args.entry_points.split(",") if s.strip()],
        "tls": args.tls,
        "enabled": True,
        "middlewares": middlewares,
    }
    res = req("POST", f"/api/v1/fleets/{fleet}/proxy_hosts", body)
    print(f"created host id={res['id']} name={res['name']} domain={res.get('domain')}")
    if not args.no_publish:
        rev = req("POST", f"/api/v1/fleets/{fleet}/revisions")
        print(f"published revision number={rev['number']} etag={rev['etag']}")


def cmd_rm(args: argparse.Namespace) -> None:
    fleet = env("FLEET", "homelab")
    hosts = req("GET", f"/api/v1/fleets/{fleet}/proxy_hosts").get("proxy_hosts", [])
    match = next((h for h in hosts if h["name"] == args.name), None)
    if not match:
        sys.stderr.write(f"no proxy host named {args.name!r} in fleet {fleet!r}\n")
        sys.exit(2)
    req("DELETE", f"/api/v1/fleets/{fleet}/proxy_hosts/{match['id']}")
    print(f"deleted host id={match['id']} name={match['name']}")
    if not args.no_publish:
        rev = req("POST", f"/api/v1/fleets/{fleet}/revisions")
        print(f"published revision number={rev['number']} etag={rev['etag']}")


def cmd_publish(_: argparse.Namespace) -> None:
    fleet = env("FLEET", "homelab")
    rev = req("POST", f"/api/v1/fleets/{fleet}/revisions")
    print(f"published revision number={rev['number']} etag={rev['etag']}")


def _rewrite_etc_hosts(new_block: str | None) -> bool:
    """Replace the managed block in /etc/hosts. None == remove the block.

    Returns True if /etc/hosts changed.
    """
    try:
        with open(ETC_HOSTS, "r", encoding="utf-8") as f:
            current = f.read()
    except FileNotFoundError:
        current = ""

    pattern = re.compile(
        r"\n*"
        + re.escape(HOSTS_BEGIN)
        + r".*?"
        + re.escape(HOSTS_END)
        + r"\n*",
        re.DOTALL,
    )
    stripped = pattern.sub("\n", current).rstrip() + "\n"
    if new_block:
        updated = stripped + "\n" + new_block.rstrip() + "\n"
    else:
        updated = stripped
    if updated == current:
        return False
    try:
        with open(ETC_HOSTS, "w", encoding="utf-8") as f:
            f.write(updated)
    except PermissionError:
        sys.stderr.write(f"need root to write {ETC_HOSTS} — re-run with sudo (or set NO_HOSTS=1 to skip)\n")
        sys.exit(3)
    return True


def _domains_from_manager() -> list[str]:
    fleet = env("FLEET", "homelab")
    try:
        hosts = req("GET", f"/api/v1/fleets/{fleet}/proxy_hosts").get("proxy_hosts", [])
    except SystemExit:
        # req() exits the process on HTTP errors; for hosts-sync we'd
        # rather no-op than abort the whole `make demo-up`. Re-raise
        # only in interactive use.
        raise
    seen: list[str] = []
    for h in hosts:
        d = (h.get("domain") or "").strip()
        if not d or d == "*" or d in seen:
            continue
        seen.append(d)
    return sorted(seen)


def cmd_hosts_sync(_: argparse.Namespace) -> None:
    domains = _domains_from_manager()
    if not domains:
        # Manager is reachable but no resolvable domains — still clear
        # any stale block so leftover entries from a previous run don't
        # mislead.
        if _rewrite_etc_hosts(None):
            print(f"cleared {ETC_HOSTS} managed block (no domains to add)")
        else:
            print("no domains to add and no managed block to clear")
        return
    block = "\n".join([
        HOSTS_BEGIN,
        "127.0.0.1 " + " ".join(domains),
        "::1 " + " ".join(domains),
        HOSTS_END,
    ])
    if _rewrite_etc_hosts(block):
        print(f"wrote {len(domains)} domain(s) to {ETC_HOSTS}: {' '.join(domains)}")
    else:
        print(f"{ETC_HOSTS} already up to date ({len(domains)} domain(s))")


def cmd_hosts_clear(_: argparse.Namespace) -> None:
    if _rewrite_etc_hosts(None):
        print(f"removed managed block from {ETC_HOSTS}")
    else:
        print(f"no managed block in {ETC_HOSTS}")


def _read_agent_token(agent_id: str) -> str:
    """Look for the per-agent token file in the demo's secrets dir.

    The /config endpoint requires the agent's own bearer token (a token
    minted for any other agent will 403). The compose demo bind-mounts
    secrets/agent-<n>-token files into Traefik containers; we read the
    same file here for symmetry.
    """
    candidates = [
        os.environ.get("AGENT_TOKEN_FILE", ""),
        f"deploy/docker-compose/secrets/{agent_id}-token",
        f"secrets/{agent_id}-token",
    ]
    # The demo seed mints tokens whose secret files are named by
    # ordinal (agent-1-token) while agent IDs use a friendlier prefix
    # (traefik-1). Extract any trailing numeric suffix and try the
    # `agent-<n>-token` shape too.
    m = re.search(r"(\d+)$", agent_id)
    if m:
        ord_n = m.group(1)
        candidates += [
            f"deploy/docker-compose/secrets/agent-{ord_n}-token",
            f"secrets/agent-{ord_n}-token",
        ]
    for path in candidates:
        if path and os.path.isfile(path):
            with open(path, "r", encoding="utf-8") as f:
                return f.read().strip()
    sys.stderr.write(
        f"could not locate token file for agent {agent_id!r}; "
        f"set AGENT_TOKEN_FILE=<path> or place secrets/{agent_id}-token\n"
    )
    sys.exit(2)


def cmd_agent_config(args: argparse.Namespace) -> None:
    fleet = env("FLEET", "homelab")
    base = env("MANAGER_URL", "http://localhost:8090").rstrip("/")
    token = _read_agent_token(args.agent_id)
    url = f"{base}/api/v1/agents/{args.agent_id}/config"
    r = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(r) as resp:
            etag = resp.headers.get("ETag", "")
            sig = resp.headers.get("X-Signature", "")
            payload = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"GET {url} -> {e.code}\n{e.read().decode(errors='replace')}\n")
        sys.exit(1)

    if args.raw:
        json.dump(payload, sys.stdout, indent=2, sort_keys=True)
        print()
        return

    print(f"agent={args.agent_id} fleet={fleet} etag={etag} sig={'present' if sig else '(none)'}")
    for proto in ("http", "tcp", "udp"):
        section = payload.get(proto)
        if not section:
            continue
        print(f"\n[{proto}]")
        routers = section.get("routers", {}) or {}
        services = section.get("services", {}) or {}
        middlewares = section.get("middlewares", {}) or {}
        if routers:
            print("  routers:")
            for name in sorted(routers):
                r = routers[name]
                rule = r.get("rule") or "(entrypoint-only)"
                eps = ",".join(r.get("entryPoints") or [])
                svc = r.get("service") or ""
                mws = ",".join(r.get("middlewares") or []) or "-"
                tls = " tls" if r.get("tls") is not None else ""
                print(f"    {name:<22} eps={eps:<14} rule={rule:<34} svc={svc:<14} mw={mws}{tls}")
        if services:
            print("  services:")
            for name in sorted(services):
                lb = (services[name].get("loadBalancer") or {})
                servers = lb.get("servers") or []
                addrs = ", ".join(s.get("url") or s.get("address") or "?" for s in servers)
                print(f"    {name:<22} -> {addrs}")
        if middlewares:
            print("  middlewares:")
            for name in sorted(middlewares):
                kinds = ",".join(k for k in middlewares[name].keys())
                print(f"    {name:<28} {kinds}")
    tls = payload.get("tls", {}) or {}
    if tls.get("certificates"):
        print(f"\n[tls] {len(tls['certificates'])} certificate(s) in pool")


def cmd_list(_: argparse.Namespace) -> None:
    fleet = env("FLEET", "homelab")
    hosts = req("GET", f"/api/v1/fleets/{fleet}/proxy_hosts").get("proxy_hosts", [])
    for h in hosts:
        mws = ",".join(mw["type"] for mw in h.get("middlewares", [])) or "-"
        print(f"  {h['name']:<16} {h['protocol']:<4} {h.get('domain',''):<28} -> {h['upstream_url']:<28} mw={mws}")


def main() -> None:
    p = argparse.ArgumentParser(prog="host.py")
    sub = p.add_subparsers(dest="cmd", required=True)

    add = sub.add_parser("add", help="create a proxy host")
    add.add_argument("name")
    add.add_argument("--domain", required=True)
    add.add_argument("--upstream", required=True)
    add.add_argument("--protocol", default="http", choices=["http", "tcp", "udp"])
    add.add_argument("--entry-points", default="web", help="comma-separated")
    add.add_argument("--tls", action="store_true")
    add.add_argument("--forward-auth", help="forwardAuth address (URL); attaches an OIDC-shaped middleware")
    add.add_argument("--no-publish", action="store_true")
    add.set_defaults(func=cmd_add)

    rm = sub.add_parser("rm", help="delete a proxy host by name")
    rm.add_argument("name")
    rm.add_argument("--no-publish", action="store_true")
    rm.set_defaults(func=cmd_rm)

    sub.add_parser("publish", help="publish a new revision").set_defaults(func=cmd_publish)
    sub.add_parser("list", help="list proxy hosts").set_defaults(func=cmd_list)
    sub.add_parser("hosts-sync", help="write demo domains into /etc/hosts (sudo)").set_defaults(func=cmd_hosts_sync)
    sub.add_parser("hosts-clear", help="remove managed block from /etc/hosts (sudo)").set_defaults(func=cmd_hosts_clear)

    ac = sub.add_parser("agent-config", help="show what an agent receives from /config")
    ac.add_argument("agent_id")
    ac.add_argument("--raw", action="store_true", help="dump the raw JSON instead of a summary")
    ac.set_defaults(func=cmd_agent_config)

    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
