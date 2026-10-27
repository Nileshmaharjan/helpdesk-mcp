# helpdesk-mcp

An [MCP](https://modelcontextprotocol.io) server in Go that exposes a help centre (a knowledge base and support
tickets) to AI agents, with the controls a real deployment needs: API-key authentication, role-scoped tools,
per-caller rate limits, input validation and an audit log of every call. It is the forward-deployed pattern of
wrapping an existing business system so agents such as Claude Code or a LangGraph agent can use it safely.

```
 Claude Code / LangGraph agent
            │  MCP (stdio, or streamable HTTP + Bearer API key)
            ▼
 ┌─────────────────────────── helpdesk-mcp ───────────────────────────┐
 │ auth: API key → principal (name, role)                             │
 │ per-role server: tools the role may not use are never listed        │
 │ guard on every call: permission → rate limit → validate → run → audit │
 └─────────────┬──────────────────────────────────────────────────────┘
               ▼
   Postgres: articles (full-text search), tickets, comments, audit_log, api_keys
```

Built on the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) and `pgx`.

## Tools and roles

| Tool | viewer | agent | admin | What it does |
|---|:-:|:-:|:-:|---|
| `search_articles` | ✓ | ✓ | ✓ | Full-text search; ranked ids, titles and highlighted snippets |
| `get_article` | ✓ | ✓ | ✓ | One article in full |
| `list_tickets` |  | ✓ | ✓ | Tickets, newest first, filtered by status |
| `get_ticket` |  | ✓ | ✓ | One ticket with its comment history |
| `create_ticket` |  | ✓ | ✓ | Open a ticket (requester defaults to the caller) |
| `add_comment` |  | ✓ | ✓ | Comment on an open ticket |
| `escalate_ticket` |  | ✓ | ✓ | Escalate to on-call, with a reason recorded on the ticket |
| `close_ticket` |  |  | ✓ | Close a ticket; closed is final |
| `get_audit_log` |  |  | ✓ | Recent tool calls: who, what, outcome, duration |

Admin-only articles (for example the data-incident escalation policy) are filtered out of search and reads for
everyone else, so role-based access applies to the knowledge the agent retrieves, not only to the tools.

Every tool has typed input and output schemas generated from Go structs, plus MCP annotations (read-only,
destructive, idempotent) so clients can decide what needs user confirmation.

## Production touches

- **Authentication:** API keys (`hd_…`) are random, shown once, and stored only as SHA-256 hashes. Over HTTP each
  request carries `Authorization: Bearer <key>`; over stdio the key is checked once at startup.
- **Least privilege:** each role gets its own MCP server, so forbidden tools never appear in `tools/list`. The
  permission is checked again on every call.
- **Rate limits:** a token bucket per caller (`-rate`, `-burst`), so one runaway agent cannot starve the others.
- **Validation:** schema validation from the SDK, then domain rules (length limits, enums, positive ids, legal
  status changes) before anything reaches the database.
- **Audit log:** every call is recorded with principal, role, tool, arguments, outcome (`ok`, `denied`, `invalid`,
  `rate_limited`, `error`) and duration, written even when the call fails.
- **Safe errors:** not-found and invalid-input errors reach the agent so it can correct itself; anything else
  (such as a database error) returns a generic message, with the detail kept in the audit log.
- **Consistency:** ticket changes lock the row (`SELECT … FOR UPDATE`); a test races ten closes of the same
  ticket and checks exactly one succeeds.

## Run it

In-memory demo, no database (prints one key per role):

```bash
go run ./cmd/helpdesk-mcp serve -transport http -dev-keys
scripts/smoke.sh http://localhost:8080/mcp <agent key>
```

With Postgres (schema and demo data are applied on start):

```bash
docker compose up -d                      # Postgres on :5434 and the server on :8080
export DATABASE_URL=postgres://helpdesk:helpdesk@localhost:5434/helpdesk
go run ./cmd/helpdesk-mcp keys create -name claude-code -role agent   # prints the key once
```

### Use it from Claude Code

Over HTTP:

```bash
claude mcp add --transport http helpdesk http://localhost:8080/mcp --header "Authorization: Bearer hd_…"
```

Or over stdio (Claude Code starts the server itself):

```json
{
  "mcpServers": {
    "helpdesk": {
      "command": "go",
      "args": ["run", "./cmd/helpdesk-mcp", "serve", "-transport", "stdio"],
      "env": { "DATABASE_URL": "postgres://helpdesk:helpdesk@localhost:5434/helpdesk", "HELPDESK_API_KEY": "hd_…" }
    }
  }
}
```

Then ask, for example: *"A clinic says appointment reminders stopped. Check the help centre, and open a high
priority ticket if the article doesn't cover it."*

## Tests

```bash
go test ./...                                                                   # unit + in-memory MCP + HTTP auth
TEST_DATABASE_URL=postgres://helpdesk:helpdesk@localhost:5434/helpdesk go test -race ./...   # adds Postgres
```

The MCP tests drive the server through a real MCP client over an in-memory transport, and the HTTP tests through
the streamable HTTP transport with bearer keys. They cover: each role's tool list, a support flow (search → read →
create → escalate), validation and domain errors as tool errors, admin-only article filtering, rate limiting with
the resulting audit trail, internal errors being hidden from the agent but kept in the audit log, and refusal of
calls without a verified caller. CI runs everything against Postgres with the race detector and builds the image.

## Layout

```
cmd/helpdesk-mcp/     serve (stdio | http) and keys create
internal/helpdesk/    domain model, validation, ticket lifecycle, demo data
internal/authz/       roles, permissions, API keys
internal/mcpserver/   tools, the per-call guard, rate limiter, HTTP + auth
internal/memstore/    in-memory store (tests, demo)
internal/postgres/    Postgres store, schema with full-text search
```

## Roadmap

- [x] Role-scoped MCP tools over stdio and HTTP, API keys, rate limits, audit log, Postgres full-text search
- [ ] LangGraph agent client evaluated with the [AI Travel Agents](https://github.com/Nileshmaharjan/ai-travel-agent) eval harness
- [ ] OAuth 2.1 resource-server mode (the SDK's `auth` package) alongside API keys
- [ ] Hybrid search (pgvector embeddings + full-text) for the knowledge base
# helpdesk-mcp
