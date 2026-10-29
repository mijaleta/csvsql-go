#!/bin/bash
# Reference solution: installs the complete csvsql implementation into /app.
set -euo pipefail
SOLUTION_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

rm -f /app/*.go
cp "$SOLUTION_DIR"/csvsql/*.go /app/
cp "$SOLUTION_DIR/csvsql/go.mod" /app/go.mod

cd /app
go build -o csvsql .
python3 run_examples.py
