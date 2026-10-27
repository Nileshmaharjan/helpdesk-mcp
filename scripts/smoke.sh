#!/usr/bin/env bash
# End-to-end smoke test of a running server over streamable HTTP with plain curl:
#   scripts/smoke.sh http://localhost:8080/mcp "$HELPDESK_API_KEY"
set -euo pipefail
url=${1:-http://localhost:8080/mcp}; key=${2:?usage: smoke.sh URL API_KEY}
sid=""
rpc() {
  local args=(-s -X POST "$url" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream'
              -H "Authorization: Bearer $key" -H 'Mcp-Protocol-Version: 2025-06-18')
  [[ -n $sid ]] && args+=(-H "Mcp-Session-Id: $sid")
  curl "${args[@]}" "$@"
}
sid=$(rpc -D - -o /dev/null -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
      | awk 'tolower($1)=="mcp-session-id:"{print $2}' | tr -d '\r')
[[ -n $sid ]] || { echo "initialize failed (check the key)"; exit 1; }
rpc -o /dev/null -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
echo "tools:  $(rpc -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | grep -o '"name":"[a-z_]*"' | sed 's/"name"://; s/"//g' | tr '\n' ' ')"
echo "search: $(rpc -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_articles","arguments":{"query":"kiosk frozen"}}}' | grep -o '"title":"[^"]*"' | head -1)"
