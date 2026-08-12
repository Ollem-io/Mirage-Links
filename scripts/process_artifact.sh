#!/bin/bash
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd); tmp=$(mktemp -d); pids=""
cleanup() { [ -z "$pids" ] || kill -KILL $pids 2>/dev/null || true; rm -rf "$tmp"; }; trap cleanup EXIT INT TERM
cd "$root"; go build -o "$tmp/service" ./internal/adapters/outbound/process/testdata/service
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
wait_port() { i=0; while [ "$i" -lt 100 ]; do (echo >/dev/tcp/127.0.0.1/$1) 2>/dev/null && return; i=$((i+1)); sleep .01; done; return 1; }
base_fd=$(find /proc/$$/fd -mindepth 1 -maxdepth 1 | wc -l); base_tasks=$(find /proc/$$/task -mindepth 1 -maxdepth 1 | wc -l)
p=$(free_port); PORT=$p "$tmp/service" healthy >/dev/null 2>&1 & pid=$!; pids=$pid; wait_port "$p"; [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$p/)" = 204 ]; kill -TERM $pid; wait $pid 2>/dev/null || true; pids=""
p=$(free_port); PORT=$p "$tmp/service" never-healthy >/dev/null 2>&1 & pid=$!; pids=$pid; wait_port "$p"; [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$p/)" = 503 ]; kill -KILL $pid 2>/dev/null || true; wait $pid 2>/dev/null || true; pids=""
p=$(free_port); PORT=$p "$tmp/service" never-bind >/dev/null 2>&1 & pid=$!; pids=$pid; sleep .05; ! (echo >/dev/tcp/127.0.0.1/$p) 2>/dev/null; kill -KILL $pid 2>/dev/null || true; wait $pid 2>/dev/null || true; pids=""
set +e; PORT=$(free_port) "$tmp/service" crash; code=$?; set -e; [ "$code" = 17 ]
setsid env PORT=$(free_port) "$tmp/service" fork >"$tmp/fork.out" 2>&1 & leader=$!; pids=$leader
for _ in $(seq 1 100); do [ -s "$tmp/fork.out" ] && break; kill -0 "$leader" 2>/dev/null || break; sleep .01; done
child=$(awk '/child/{print $2; exit}' "$tmp/fork.out" 2>/dev/null || true); [ -n "$child" ]
kill -KILL -- -"$leader" 2>/dev/null || true; wait "$leader" 2>/dev/null || true; sleep .02
! kill -0 "$child" 2>/dev/null || [ "$(awk '{print $3}' /proc/$child/stat 2>/dev/null || echo X)" = Z ]; pids=""
setsid env PORT=$(free_port) "$tmp/service" ignore >/dev/null 2>&1 & pid=$!; pids=$pid
for _ in $(seq 1 100); do kill -0 "$pid" 2>/dev/null && break; sleep .01; done
kill -TERM -- -"$pid" 2>/dev/null || true; sleep .03; kill -0 "$pid" 2>/dev/null
kill -KILL -- -"$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; pids=""
PORT=$(free_port) LINES=200 "$tmp/service" noisy >"$tmp/noisy.out" 2>"$tmp/noisy.err"; grep -q stdout "$tmp/noisy.out"; grep -q stderr "$tmp/noisy.err"
MIRAGE_TEST_SERVICE="$tmp/service" go test -count=1 ./internal/adapters/outbound/process ./internal/adapters/outbound/health ./internal/adapters/outbound/logs
after_fd=$(find /proc/$$/fd -mindepth 1 -maxdepth 1 | wc -l); after_tasks=$(find /proc/$$/task -mindepth 1 -maxdepth 1 | wc -l); [ "$after_fd" -le "$base_fd" ]; [ "$after_tasks" -le "$base_tasks" ]
printf 'MIR-05 process artifact PASS (process/port/goroutine/fd checks)\n'
