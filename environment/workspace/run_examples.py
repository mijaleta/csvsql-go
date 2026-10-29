#!/usr/bin/env python3
"""Builds csvsql from /app and runs the public example cases.

    python3 /app/run_examples.py        # summary
    python3 /app/run_examples.py -v     # also show every failing case

Each case runs the binary with its args from inside examples/data and passes
when stdout matches byte for byte and the exit status matches. A case that
expects a failure also has a "control" command line, which must succeed too.
"""
import json
import os
import subprocess
import sys

APP = os.path.dirname(os.path.abspath(__file__))
DATA = os.path.join(APP, "examples", "data")
CASES = os.path.join(APP, "examples", "cases.jsonl")
BINARY = os.path.join(APP, "csvsql")


def check(run, case, is_control, verbose):
    try:
        p = subprocess.run([BINARY] + run["args"], cwd=DATA, capture_output=True, timeout=10)
        ok = p.stdout == run["stdout"].encode() and p.returncode == run["exit_code"]
    except subprocess.TimeoutExpired:
        p, ok = None, False
    if not ok and verbose:
        label = "control of " if is_control else ""
        print(f"--- {label}{case['id']} ({case['category']}) args={run['args']!r}")
        print(f"expected exit {run['exit_code']}, stdout:\n{run['stdout']}")
        if p is None:
            print("got: timed out after 10 s")
        else:
            print(f"got exit {p.returncode}, stdout:\n{p.stdout.decode(errors='replace')}")
            if p.stderr:
                print(f"stderr:\n{p.stderr.decode(errors='replace')[:2000]}")
    return ok


def main():
    verbose = "-v" in sys.argv[1:]
    build = subprocess.run(["go", "build", "-o", BINARY, "."], cwd=APP, capture_output=True, text=True)
    if build.returncode != 0:
        print("build failed:\n" + build.stderr)
        return 1
    with open(CASES, encoding="utf-8") as f:
        cases = [json.loads(line) for line in f if line.strip()]
    passed = 0
    for case in cases:
        runs = [case] + ([case["control"]] if "control" in case else [])
        ok = all(check(run, case, run is not case, verbose) for run in runs)
        passed += ok
    print(f"{passed}/{len(cases)} example cases pass")
    return 0


if __name__ == "__main__":
    sys.exit(main())
