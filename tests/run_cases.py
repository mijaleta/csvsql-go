#!/usr/bin/env python3
"""Hidden verifier for the csvsql task.

1. Stops any process still running as the agent user and makes this directory
   root-only, so neither the build nor the candidate binary can read the cases.
2. Copies the hidden tables to a root-owned, read-only directory.
3. Copies /app (without .git/, symlinks kept as links) to a fresh directory and
   runs `go build` there as the agent user, offline, with a fresh build cache.
4. Runs every case in cases.jsonl against the built binary, again as the agent
   user, from inside the table directory. A case passes when stdout matches
   byte for byte and the exit status matches. stderr is ignored. A case that
   expects a failure also has a control command line that must succeed, so a
   program that rejects everything gets no credit for rejecting bad input.
5. Writes passed / total to /logs/verifier/reward.txt.
"""
import json
import os
import pwd
import shutil
import subprocess
import sys
from collections import defaultdict

TESTS_DIR = os.path.dirname(os.path.abspath(__file__))
APP_DIR = "/app"
ROOT = "/tmp/csvsql-verify"
BUILD_DIR = ROOT + "/src"
BUILD_HOME = ROOT + "/home"
OUT_DIR = ROOT + "/out"
BIN_DIR = ROOT + "/bin"
DATA_DIR = ROOT + "/data"
REWARD = "/logs/verifier/reward.txt"
DETAILS = "/logs/verifier/details.json"
BUILD_TIMEOUT = 900
CASE_TIMEOUT = 10


def write_reward(score, details):
    with open(REWARD, "w") as f:
        f.write(f"{score:.4f}\n")
    with open(DETAILS, "w") as f:
        json.dump(details, f, indent=2)
    print(f"reward: {score:.4f}")


def chown_tree(path, uid, gid):
    os.lchown(path, uid, gid)
    for root, dirs, files in os.walk(path):
        for name in dirs + files:
            os.lchown(os.path.join(root, name), uid, gid)


def as_agent(agent, **kwargs):
    return dict(user=agent.pw_uid, group=agent.pw_gid, extra_groups=[], **kwargs)


def main():
    agent = pwd.getpwnam("agent")
    subprocess.run(["pkill", "-9", "-u", "agent"], check=False)
    os.chmod(TESTS_DIR, 0o700)

    with open(os.path.join(TESTS_DIR, "cases.jsonl"), encoding="utf-8") as f:
        cases = [json.loads(line) for line in f if line.strip()]

    shutil.rmtree(ROOT, ignore_errors=True)
    os.makedirs(ROOT)
    os.chmod(ROOT, 0o755)
    shutil.copytree(os.path.join(TESTS_DIR, "data"), DATA_DIR)
    for root, dirs, files in os.walk(DATA_DIR):
        os.chmod(root, 0o755)
        for name in files:
            os.chmod(os.path.join(root, name), 0o444)

    if not os.path.isfile(os.path.join(APP_DIR, "go.mod")):
        print("no go.mod in /app")
        write_reward(0.0, {"build": "missing go.mod"})
        return
    shutil.copytree(APP_DIR, BUILD_DIR, symlinks=True, ignore=shutil.ignore_patterns(".git"))
    os.makedirs(BUILD_HOME)
    os.makedirs(OUT_DIR)
    for d in (BUILD_DIR, BUILD_HOME, OUT_DIR):
        chown_tree(d, agent.pw_uid, agent.pw_gid)

    env = {
        "PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
        "HOME": BUILD_HOME,
        "GOPATH": os.path.join(BUILD_HOME, "go"),
        "GOCACHE": os.path.join(BUILD_HOME, "gocache"),
        "GOTOOLCHAIN": "local",
        "GOPROXY": "off",
        "GOFLAGS": "-mod=mod",
        "GOTELEMETRY": "off",
        "CGO_ENABLED": "0",
        "LANG": "C.UTF-8",
    }
    built = os.path.join(OUT_DIR, "csvsql")
    try:
        build = subprocess.run(
            ["go", "build", "-o", built, "."],
            **as_agent(agent, cwd=BUILD_DIR, env=env, capture_output=True, timeout=BUILD_TIMEOUT),
        )
        build_ok = build.returncode == 0
        build_log = build.stderr.decode(errors="replace")[-4000:]
    except subprocess.TimeoutExpired:
        build_ok, build_log = False, "build timed out"
    if not build_ok or not os.path.isfile(built):
        print("build failed:\n" + build_log)
        write_reward(0.0, {"build": "failed", "log": build_log})
        return

    # Run a root-owned copy so a candidate cannot rewrite its own binary mid-suite.
    os.makedirs(BIN_DIR)
    binary = os.path.join(BIN_DIR, "csvsql")
    shutil.copy(built, binary)
    os.chmod(binary, 0o755)

    run_env = {"PATH": "/usr/bin:/bin", "HOME": BUILD_HOME, "LANG": "C.UTF-8"}

    def check(run):
        try:
            p = subprocess.run(
                [binary] + run["args"],
                **as_agent(agent, input=b"", cwd=DATA_DIR, env=run_env, capture_output=True,
                           timeout=CASE_TIMEOUT),
            )
            return p.stdout == run["stdout"].encode() and p.returncode == run["exit_code"]
        except subprocess.TimeoutExpired:
            return False

    passed = 0
    by_category = defaultdict(lambda: [0, 0])
    failures = []
    for case in cases:
        ok = check(case) and ("control" not in case or check(case["control"]))
        passed += ok
        by_category[case["category"]][0] += ok
        by_category[case["category"]][1] += 1
        if not ok:
            failures.append(case["id"])

    subprocess.run(["pkill", "-9", "-u", "agent"], check=False)
    for cat, (good, total) in sorted(by_category.items()):
        print(f"{cat:12s} {good:3d}/{total}")
    print(f"total        {passed:3d}/{len(cases)}")
    write_reward(
        passed / len(cases),
        {"passed": passed, "total": len(cases), "by_category": dict(by_category), "failed_ids": failures},
    )


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # never leave a stale reward behind
        print(f"verifier error: {exc!r}", file=sys.stderr)
        write_reward(0.0, {"verifier_error": repr(exc)})
