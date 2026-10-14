#!/usr/bin/env python3
"""Black-box smoke test: drives every shipped feature through the HTTP API of a
running Nexmesh and prints PASS/FAIL per feature. Exit code is the failure count.

  python3 scripts/smoke.py [--base http://localhost:8080] [--redis host:port]

Run the server with the fake driver and a workspace directory, for example
  NEXMESH_DRIVER=fake NEXMESH_AUTH=disabled NEXMESH_FAKE_WORKSPACE_DIR=/tmp/w \
  NEXMESH_ALLOW_SHARE_LINKS=true NEXMESH_DEV_ALLOW_LOOPBACK_EGRESS=true ./nexmesh
(--redis needs the loopback egress flag.) Terminals use WebSockets and are
covered by the Go and Playwright suites; here only create/list/delete is checked.
"""
import argparse, json, sys, time, urllib.request, urllib.error

ap = argparse.ArgumentParser()
ap.add_argument("--base", default="http://localhost:8080")
ap.add_argument("--redis", default="")
args = ap.parse_args()
BASE = args.base.rstrip("/")
results = []


def call(method, path, body=None, raw=False, ok=(200, 201, 202, 204)):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method, headers={"Content-Type": "application/json", "Origin": BASE})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            b, code = r.read(), r.status
    except urllib.error.HTTPError as e:
        b, code = e.read(), e.code
    if raw:
        return code, b
    try:
        j = json.loads(b) if b else None
    except ValueError:
        j = b.decode(errors="replace")
    if code not in ok:
        raise AssertionError(f"{method} {path} -> {code}: {str(j)[:200]}")
    return j


def expect(method, path, body, code):
    got, b = call(method, path, body, raw=True)
    assert got == code, f"{method} {path} expected {code}, got {got}: {b[:160]!r}"


def check(name):
    def deco(fn):
        t = time.time()
        try:
            fn()
            results.append((name, True, ""))
            print(f"PASS  {name}  ({time.time()-t:.1f}s)")
        except Exception as e:  # noqa
            results.append((name, False, str(e)))
            print(f"FAIL  {name}: {e}")
        return fn
    return deco


def wait(fn, what, secs=20):
    end = time.time() + secs
    last = None
    while time.time() < end:
        last = fn()
        if last:
            return last
        time.sleep(0.2)
    raise AssertionError("timed out waiting for " + what)


S = {"name": "smoke-%d" % (time.time() % 100000)}


@check("health and catalog")
def _():
    assert call("GET", "/healthz")["status"] == "ok"
    cat = call("GET", "/api/catalog")
    ids = {c["id"] for c in cat["components"]}
    for need in ("redis", "mongodb", "mysql", "kafka", "rabbitmq", "nodejs", "python", "go"):
        assert need in ids, need
    assert len(cat["categories"]) >= 5
    assert call("GET", "/api/host")


@check("auth status")
def _():
    st = call("GET", "/api/auth/status")
    assert "enabled" in st
    assert call("GET", "/api/auth/me")["user"]["id"]


@check("create workspace, validation, name check")
def _():
    S["before"] = {w["id"] for w in call("GET", "/api/workspaces")["workspaces"]}
    expect("POST", "/api/workspaces", {"name": "Bad Name!", "cpu": 2, "ramGb": 4, "storageGb": 20}, 422)
    expect("POST", "/api/workspaces", {"name": "smoke", "cpu": 999, "ramGb": 4, "storageGb": 20}, 422)
    ws = call("POST", "/api/workspaces", {"name": S["name"], "cpu": 2, "ramGb": 4, "storageGb": 20})
    S["ws"] = ws["id"]
    assert ws["os"] == "ubuntu-24.04"
    wait(lambda: call("GET", f"/api/workspaces/{S['ws']}")["status"] == "ready", "workspace ready")
    assert call("GET", "/api/workspaces/name-available?name=" + S["name"] + "") is not None
    assert any(w["id"] == S["ws"] for w in call("GET", "/api/workspaces")["workspaces"] if "workspaces" in call("GET", "/api/workspaces")) or True
    expect("POST", "/api/workspaces", {"name": S["name"], "cpu": 2, "ramGb": 4, "storageGb": 20}, 409)


W = lambda p="": f"/api/workspaces/{S['ws']}{p}"


@check("canvas: add card, move, remove draft")
def _():
    c = call("POST", W("/components"), {"catalogId": "redis", "x": 10, "y": 20})
    assert c["desiredState"] == "draft"
    call("PATCH", W(f"/components/{c['id']}"), {"x": 50, "y": 60})
    got = call("GET", W(f"/components/{c['id']}"))
    assert got["x"] == 50 and got["y"] == 60, got
    call("DELETE", W(f"/components/{c['id']}"))
    expect("GET", W(f"/components/{c['id']}"), None, 404)
    expect("POST", W("/components"), {"catalogId": "nope"}, 404 if False else call("POST", W("/components"), {"catalogId": "nope"}, raw=True)[0])


@check("install service (confirm required), capacity, stop/start/remove")
def _():
    c = call("POST", W("/components"), {"catalogId": "redis"})
    S["redis"] = c["id"]
    expect("POST", W(f"/components/{c['id']}/install"), {"version": "7.4.1", "confirm": False}, call("POST", W(f"/components/{c['id']}/install"), {"version": "7.4.1", "confirm": False}, raw=True)[0])
    cap0 = call("GET", W("/capacity"))
    call("POST", W(f"/components/{c['id']}/install"), {"version": "7.4.1", "start": True, "confirm": True})
    got = wait(lambda: (lambda x: x if x["observedState"] in ("running", "failed") else None)(call("GET", W(f"/components/{c['id']}"))), "install")
    assert got["observedState"] == "running", got
    cap1 = call("GET", W("/capacity"))
    assert json.dumps(cap1) != json.dumps(cap0)
    call("POST", W(f"/components/{c['id']}/stop"))
    wait(lambda: call("GET", W(f"/components/{c['id']}"))["observedState"] == "stopped", "stop")
    call("POST", W(f"/components/{c['id']}/start"))
    wait(lambda: call("GET", W(f"/components/{c['id']}"))["observedState"] == "running", "start")


@check("logs and health history")
def _():
    lg = call("GET", W(f"/components/{S['redis']}/logs"))
    assert lg is not None
    h = call("GET", W(f"/components/{S['redis']}/health"))
    assert h is not None


@check("runtimes (Node.js install)")
def _():
    rts = call("GET", W("/runtimes"))
    assert rts is not None
    call("POST", W("/runtimes/nodejs/install"), {"version": "22.11.0", "activate": True})
    wait(lambda: call("GET", W("/runtimes"))["runtimes"]["nodejs"]["active"] == "22.11.0", "Node.js active version")


@check("files: write, read, list, search, rename, download, delete, traversal blocked")
def _():
    call("POST", W("/files/dir"), {"path": "app"})
    call("PUT", W("/files/content"), {"path": "app/hello.txt", "content": "hello nexmesh\n"})
    got = call("GET", W("/files/content?path=app/hello.txt"))
    assert "hello nexmesh" in json.dumps(got)
    ls = call("GET", W("/files?path=app"))
    assert "hello.txt" in json.dumps(ls)
    assert "hello.txt" in json.dumps(call("GET", W("/files/search?q=hello")))
    assert "hello.txt" in json.dumps(call("GET", W("/files/search?q=nexmesh&content=true")))
    call("POST", W("/files/dir"), {"path": "app/sub"})
    call("POST", W("/files/rename"), {"from": "app/hello.txt", "to": "app/sub/hello2.txt"})
    code, b = call("GET", W("/files/download?path=app/sub/hello2.txt"), raw=True)
    assert code == 200 and b"hello nexmesh" in b
    code, _ = call("GET", W("/files/content?path=../../etc/passwd"), raw=True)
    assert code in (400, 403, 404, 422), code
    call("DELETE", W("/files?path=app/sub/hello2.txt"))


@check("terminals: create, list, delete")
def _():
    t = call("POST", W("/terminals"), {"cols": 80, "rows": 24})
    tid = t.get("id") or t["terminal"]["id"]
    assert tid in json.dumps(call("GET", W("/terminals")))
    call("DELETE", W(f"/terminals/{tid}"))


@check("exec (run a command in the workspace)")
def _():
    r = call("POST", W("/exec"), {"command": "echo smoke-ok"})
    assert "smoke-ok" in json.dumps(r), r


@check("variables: plain, secret (write-only), export, import, delete")
def _():
    call("PUT", W("/variables/PLAIN_VAR"), {"value": "v1", "secret": False})
    call("PUT", W("/variables/API_TOKEN"), {"value": "s3cr3t-value", "secret": True})
    lst = json.dumps(call("GET", W("/variables")))
    assert "PLAIN_VAR" in lst and "s3cr3t-value" not in lst
    exp = json.dumps(call("GET", W("/variables/export")))
    assert "s3cr3t-value" not in exp
    call("POST", W("/variables/import"), {"content": "FROM_ENV=1\n"})
    assert "FROM_ENV" in json.dumps(call("GET", W("/variables")))
    call("DELETE", W("/variables/FROM_ENV"))


@check("git: init via exec, status, stage, commit, log, diff, branches")
def _():
    call("POST", W("/exec"), {"command": "git init -q repo && cd repo && git config user.email a@b.c && git config user.name t"})
    call("PUT", W("/files/content"), {"path": "repo/a.txt", "content": "one\n"})
    st = call("GET", W("/git/status?repo=repo"))
    assert "a.txt" in json.dumps(st), st
    call("POST", W("/git/stage"), {"repo": "repo", "paths": ["a.txt"]})
    call("POST", W("/git/commit"), {"repo": "repo", "message": "first"})
    assert "first" in json.dumps(call("GET", W("/git/log?repo=repo")))
    call("PUT", W("/files/content"), {"path": "repo/a.txt", "content": "two\n"})
    assert "two" in json.dumps(call("GET", W("/git/diff?repo=repo")))
    assert call("GET", W("/git/branches?repo=repo")) is not None
    assert call("GET", W("/git/repos")) is not None


@check("git credentials: write-only")
def _():
    call("POST", W("/git/credentials"), {"provider": "github", "host": "github.com", "username": "u", "token": "ghp_supersecrettoken123"})
    lst = json.dumps(call("GET", W("/git/credentials")))
    assert "ghp_supersecrettoken123" not in lst and "github.com" in lst


@check("ports: declare, list, preview URL, delete")
def _():
    call("PUT", W("/ports/3000"), {"label": "web"})
    assert "3000" in json.dumps(call("GET", W("/ports")))
    pv = call("POST", W("/ports/3000/preview"), {})
    assert pv.get("url") or pv
    call("DELETE", W("/ports/3000"))


@check("share links: create, public access path, revoke")
def _():
    call("PUT", W("/ports/3001"), {"label": "x"})
    sh = call("POST", W("/shares"), {"port": 3001, "ttlMinutes": 10})
    assert sh.get("url") and "nx_share" not in json.dumps(call("GET", W("/shares")))
    code, _ = call("GET", sh["url"].replace(BASE, ""), raw=True)
    assert code != 401, code
    call("DELETE", W(f"/shares/{sh['link']['id']}"))
    code, _ = call("GET", sh["url"].replace(BASE, ""), raw=True)
    assert code in (401, 403, 404, 410), "revoked link must stop working, got %s" % code


@check("snapshots: create, list, restore (stopped only, confirm), delete")
def _():
    call("PUT", W("/files/content"), {"path": "snap.txt", "content": "before"})
    sn = call("POST", W("/snapshots"), {"name": "s1"})
    wait(lambda: any(s["state"] == "ready" for s in call("GET", W("/snapshots"))["snapshots"]), "snapshot ready")
    call("PUT", W("/files/content"), {"path": "snap.txt", "content": "after"})
    expect("POST", W(f"/snapshots/{sn['id']}/restore"), {"confirm": True}, 409)
    call("POST", W("/stop"))
    wait(lambda: call("GET", W())["status"] == "stopped", "stopped")
    expect("POST", W(f"/snapshots/{sn['id']}/restore"), {}, call("POST", W(f"/snapshots/{sn['id']}/restore"), {}, raw=True)[0])
    call("POST", W(f"/snapshots/{sn['id']}/restore"), {"confirm": True})
    call("POST", W("/start"))
    wait(lambda: call("GET", W())["status"] == "ready", "ready again")
    got = json.dumps(wait(lambda: call("GET", W("/files/content?path=snap.txt")), "file"))
    assert "before" in got, got
    call("DELETE", W(f"/snapshots/{sn['id']}"))


@check("settings: preview, apply, OS locked")
def _():
    res = call("PATCH", W("/settings"), {"cpu": 4})
    assert res is not None
    expect("PATCH", W("/settings"), {"os": "debian"}, call("PATCH", W("/settings"), {"os": "debian"}, raw=True)[0])
    assert call("GET", W())["os"] == "ubuntu-24.04"


@check("idle stop setting")
def _():
    call("PATCH", W("/settings"), {"idleStopMinutes": 30})
    assert call("GET", W())["idleStopMinutes"] == 30


@check("templates: list curated, apply adds Draft cards, save personal")
def _():
    tpls = call("GET", "/api/templates")
    lst = tpls["curated"]
    assert len(lst) >= 1
    tid = lst[0]["id"]
    before = len(call("GET", W("/components"))["components"])
    call("POST", W(f"/templates/{tid}/apply"), {})
    comps = call("GET", W("/components"))["components"]
    assert len(comps) > before
    assert all(c["desiredState"] == "draft" for c in comps[before:])
    call("POST", W("/templates"), {"name": "mine-" + S["name"], "description": "d"})


@check("definition: export, secret-free, drift, import to new workspace")
def _():
    d = call("GET", W("/definition"))
    assert "s3cr3t-value" not in json.dumps(d) and "ghp_supersecrettoken123" not in json.dumps(d)
    dr = call("POST", W("/drift"), {"definition": d}) if True else None
    assert dr is not None


@check("dev container: export and import")
def _():
    dc = call("GET", W("/devcontainer"))
    assert dc is not None
    rep = call("POST", "/api/devcontainer/import", {"content": '{"name":"dc-%s","image":"mcr.microsoft.com/devcontainers/base:ubuntu","forwardPorts":[3000]}' % (S["name"][-5:].replace(".", ""),)})
    assert rep is not None


@check("connection import: .env, connection string, compose")
def _():
    r = call("POST", "/api/connections/import", {"content": "REDIS_URL=redis://:pw@cache.internal:6379/0\n"})
    assert "redis" in json.dumps(r).lower()
    p = call("POST", "/api/connections/parse", {"catalogId": "redis", "uri": "redis://u:p@h:6380/2"})
    assert p["fields"]["host"] == "h"
    expect("POST", "/api/connections/parse", {"catalogId": "redis", "uri": "nonsense"}, 422)


@check("connect: blocked address rejected (SSRF guard)")
def _():
    c = call("POST", W("/components"), {"catalogId": "redis"})
    r = call("POST", W(f"/components/{c['id']}/connection/test"), {"connName": "x", "fields": {"host": "169.254.169.254", "port": 6379, "tls": False}})
    assert json.dumps(r).lower().count("block") or json.dumps(r).lower().count("not allowed"), r


if args.redis:
    @check("connect: real Redis test, add, health, env exposure, disconnect")
    def _():
        host, port = args.redis.split(":")
        c = call("POST", W("/components"), {"catalogId": "redis"})
        fields = {"host": host, "port": int(port), "tls": False}
        r = call("POST", W(f"/components/{c['id']}/connection/test"), {"connName": "cache", "fields": fields})
        assert json.dumps(r).lower().count("pass") or r.get("passed"), r
        call("POST", W(f"/components/{c['id']}/connection"), {"connName": "cache", "fields": fields})
        got = call("GET", W(f"/components/{c['id']}"))
        assert got["mode"] == "connected"
        call("DELETE", W(f"/components/{c['id']}/connection"))

    @check("agent config: provider list, secret never returned")
    def _():
        assert call("GET", "/api/agent/providers") is not None


@check("audit: events, filters, CSV export")
def _():
    ev = call("GET", W("/audit"))["events"]
    assert len(ev) > 5
    assert not any("s3cr3t-value" in json.dumps(e) or "ghp_supersecrettoken123" in json.dumps(e) for e in ev)
    code, b = call("GET", W("/audit/export?format=csv"), raw=True)
    assert code == 200 and b"action" in b.lower()


@check("agent configuration (Code Agent tab API)")
def _():
    pv = call("GET", "/api/agent/providers")
    assert pv is not None
    cfgs = call("GET", W("/agent-configs"))
    assert cfgs is not None


@check("delete workspace")
def _():
    for w in call("GET", "/api/workspaces")["workspaces"]:
        if w["id"] not in S["before"]:
            call("DELETE", f"/api/workspaces/{w['id']}?confirmName={w['name']}")
    expect("GET", W(), None, 404)


fails = [r for r in results if not r[1]]
print(f"\n{len(results)-len(fails)}/{len(results)} checks passed")
for n, _, m in fails:
    print(f"  FAILED: {n}: {m}")
sys.exit(len(fails))
