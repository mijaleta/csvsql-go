FROM golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac

ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8 \
    GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod GOTELEMETRY=off CGO_ENABLED=0

# The image already has python3 (for the example and verifier harnesses) and
# procps (pkill). SQLite is deliberately removed so the expected outputs have
# to come from the specification, not from running the reference engine.
RUN rm -rf /usr/lib/python3.11/sqlite3 \
    && rm -f /usr/lib/python3.11/lib-dynload/_sqlite3*.so \
    && rm -f /usr/lib/x86_64-linux-gnu/libsqlite3.so* \
    && ! command -v sqlite3

# Non-root agent user. The Go toolchain stays root-owned and read-only so the
# agent cannot change the compiler or standard library the verifier uses.
RUN useradd --create-home --shell /bin/bash agent \
    && chmod -R go-w /usr/local/go

WORKDIR /app
COPY workspace/ /app/
RUN chmod +x /app/run_examples.py && chown -R agent:agent /app

# The starting package must build offline.
RUN su agent -c "cd /app && /usr/local/go/bin/go build -o /tmp/csvsql-check . && rm /tmp/csvsql-check"
