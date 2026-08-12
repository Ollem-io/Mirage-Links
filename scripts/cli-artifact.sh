#!/bin/bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd); tmp=$(mktemp -d); trap 'kill ${server_pid:-} 2>/dev/null || true; rm -rf "$tmp"' EXIT
cd "$root"; go build -o "$tmp/mirage" ./cmd/mirage
cat >"$tmp/fake.py" <<'PY'
import http.server,json,sys
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a): pass
 def respond(self):
  if self.path=='/api/v1/spaces' and self.command=='POST': x={'space':{'alias':'calm','expires_at':'soon'},'token':'mir_once'}
  elif self.path.startswith('/api/v1/spaces/calm'): x={'space':{'alias':'calm','expires_at':'soon'}}
  elif self.path=='/api/v1/spaces': x={'spaces':[]}
  elif self.path=='/api/v1/links' and self.command=='POST': x={'link':{'name':'api','url':'http://api','status':'active'}}
  elif self.path=='/api/v1/links': x={'links':[]}
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
printf 'MIR-09 compiled CLI artifact PASS
'
