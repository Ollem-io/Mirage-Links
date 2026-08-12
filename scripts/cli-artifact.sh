#!/bin/bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd); tmp=$(mktemp -d); trap 'kill ${start_pid:-} ${follow_pid:-} ${admin_pid:-} ${shape_pid:-} ${server_pid:-} 2>/dev/null || true; rm -rf "$tmp"' EXIT
cd "$root"; go build -o "$tmp/mirage" ./cmd/mirage
cat >"$tmp/fake.py" <<'PY'
import http.server,json,sys
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def respond(self):
  if '/logs' in self.path and 'follow=true' in self.path:
   self.send_response(200); self.end_headers(); self.wfile.write(b'line\n'); self.wfile.flush(); import time; time.sleep(30); return
  if self.path=='/api/v1/spaces' and self.command=='POST': x={'space':{'alias':'calm','expires_at':'soon'},'token':'mir_once'}
  elif self.path.startswith('/api/v1/spaces/calm'): x={'space':{'alias':'calm','expires_at':'soon'}}
  elif self.path=='/api/v1/spaces': x={'spaces':[]}
  elif self.path=='/api/v1/links' and self.command=='POST': x={'link':{'name':'api','url':'http://api','status':'active'}}
  elif self.path=='/api/v1/links': x={'links':[]}
  elif '/logs' in self.path and 'follow=true' in self.path:
   self.send_response(200); self.end_headers(); self.wfile.write(b'line\n'); self.wfile.flush()
   import time
   while True: time.sleep(1)
  elif '/logs' in self.path: x={'logs':[]}
  else: x={}
  b=json.dumps(x).encode(); self.send_response(200); self.send_header('content-type','application/json'); self.send_header('content-length',len(b)); self.end_headers(); self.wfile.write(b)
 do_GET=do_POST=do_DELETE=respond
s=http.server.HTTPServer(('127.0.0.1',0),H); print(s.server_port,flush=True); s.serve_forever()
PY
python3 "$tmp/fake.py" >"$tmp/port" & server_pid=$!
for _ in $(seq 1 100); do [ -s "$tmp/port" ] && break; sleep .01; done
url=http://127.0.0.1:$(cat "$tmp/port")
"$tmp/mirage" --server "$url" space create | grep -q 'Token: mir_once'
"$tmp/mirage" --server "$url" space create --json | python3 -c 'import json,sys; assert json.load(sys.stdin)["token"]=="mir_once"'
"$tmp/mirage" --server "$url" space list >/dev/null
mkdir "$tmp/cwd"; printf tok >"$tmp/cwd/.mirage_token"; chmod 600 "$tmp/cwd/.mirage_token"
(cd "$tmp/cwd"; "$tmp/mirage" --server "$url" link list >/dev/null)
mkdir "$tmp/cwd/child"; if (cd "$tmp/cwd/child"; env -u MIRAGE_TOKEN "$tmp/mirage" --server "$url" link list >/dev/null 2>&1); then echo parent token searched >&2; exit 1; fi

# Generated Cobra help is the executing tree's contract.
"$tmp/mirage" --help | grep -q 'Manage spaces'
"$tmp/mirage" space list --help | grep -q 'list spaces'
# Object/items and bare-array list compatibility in machine mode.
cat >"$tmp/shapes.py" <<'PY'
import http.server,sys
shape=sys.argv[1].encode()
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def do_GET(self):
  self.send_response(200); self.send_header('content-length',len(shape)); self.end_headers(); self.wfile.write(shape)
s=http.server.HTTPServer(('127.0.0.1',0),H); print(s.server_port,flush=True); s.serve_forever()
PY
for shape in '{"items":[{"name":"api"}]}' '[{"name":"api"}]'; do
  : >"$tmp/shapeport"; python3 "$tmp/shapes.py" "$shape" >"$tmp/shapeport" & shape_pid=$!
  for _ in $(seq 1 100); do [ -s "$tmp/shapeport" ] && break; sleep .01; done
  "$tmp/mirage" --server "http://127.0.0.1:$(cat "$tmp/shapeport")" --token t --json link list | python3 -c 'import json,sys; assert json.load(sys.stdin)["links"][0]["name"]=="api"'
  kill "$shape_pid"; wait "$shape_pid" 2>/dev/null || true
done
# Token diagnostics stay on stderr, JSON stays on stdout, and secrets are redacted.
printf 'super-secret-token' >"$tmp/cwd/.mirage_token"; chmod 644 "$tmp/cwd/.mirage_token"
(cd "$tmp/cwd"; "$tmp/mirage" --server "$url" --json link list >"$tmp/json" 2>"$tmp/diag")
python3 -c 'import json; json.load(open("'$tmp'/json"))'
grep -q 'group/world-readable' "$tmp/diag"
if grep -R -q 'super-secret-token' "$tmp/json" "$tmp/diag"; then echo token leaked >&2; exit 1; fi
# SIGINT cancels a compiled logs --follow request cleanly.
"$tmp/mirage" --server "$url" --token super-secret-token link logs api --follow >"$tmp/follow.out" 2>"$tmp/follow.err" & follow_pid=$!
for _ in $(seq 1 100); do grep -q line "$tmp/follow.out" 2>/dev/null && break; sleep .01; done
kill -INT "$follow_pid"; wait "$follow_pid"
if grep -R -q 'super-secret-token' "$tmp/follow.out" "$tmp/follow.err"; then echo follow token leaked >&2; exit 1; fi

# Start the real composed MIR-07 management server with an external fake Caddy
# Admin API, prove readiness/API traffic, and stop the foreground process by SIGINT.
cat >"$tmp/admin.py" <<'PY'
import http.server,json,sys
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def do_GET(self):
  b=b'[]'; self.send_response(200); self.send_header('content-length',len(b)); self.end_headers(); self.wfile.write(b)
 def mutation(self): self.send_response(200); self.end_headers()
 do_POST=do_PUT=do_DELETE=mutation
s=http.server.HTTPServer(('127.0.0.1',0),H); print(s.server_port,flush=True); s.serve_forever()
PY
python3 "$tmp/admin.py" >"$tmp/adminport" & admin_pid=$!
for _ in $(seq 1 100); do [ -s "$tmp/adminport" ] && break; sleep .01; done
private=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
public=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
cat >"$tmp/config.yaml" <<EOF
base_host: example.com
public_address: 127.0.0.1:$public
private_address: 127.0.0.1:$private
data_path: $tmp/real.db
caddy:
  admin_url: http://127.0.0.1:$(cat "$tmp/adminport")
  managed: false
EOF
"$tmp/mirage" start --config "$tmp/config.yaml" >"$tmp/start.out" 2>"$tmp/start.err" & start_pid=$!
for _ in $(seq 1 300); do grep -q 'Mirage ready' "$tmp/start.out" 2>/dev/null && break; sleep .01; done
grep -q 'Mirage ready' "$tmp/start.out"
"$tmp/mirage" --server "http://127.0.0.1:$private" --json space create | python3 -c 'import json,sys; x=json.load(sys.stdin); assert x["token"]'
kill -INT "$start_pid"; wait "$start_pid"
[ ! -s "$tmp/start.err" ]
kill "$admin_pid"; wait "$admin_pid" 2>/dev/null || true

printf 'MIR-09 compiled CLI artifact PASS\n'
