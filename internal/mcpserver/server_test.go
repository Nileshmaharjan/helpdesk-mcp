package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/memstore"
)

func seededStore(t *testing.T) *memstore.Store {
	t.Helper()
	s := memstore.New()
	if err := s.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// connect runs a server and a client over an in-memory pipe, the same protocol a real agent speaks.
func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", text(res))
	}
	var out T
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestEachRoleSeesOnlyItsTools(t *testing.T) {
	store := seededStore(t)
	want := map[authz.Role][]string{
		authz.Viewer: {"get_article", "search_articles"},
		authz.Agent:  {"add_comment", "create_ticket", "escalate_ticket", "get_article", "get_ticket", "list_tickets", "search_articles"},
		authz.Admin:  {"add_comment", "close_ticket", "create_ticket", "escalate_ticket", "get_article", "get_audit_log", "get_ticket", "list_tickets", "search_articles"},
	}
	for role, tools := range want {
		cs := connect(t, NewForPrincipal(store, authz.Principal{Name: "u", Role: role}, Options{}))
		if got := toolNames(t, cs); !slices.Equal(got, tools) {
			t.Errorf("%s tools = %v, want %v", role, got, tools)
		}
	}

	// A tool missing from tools/list cannot be called either.
	viewer := connect(t, NewForPrincipal(store, authz.Principal{Name: "v", Role: authz.Viewer}, Options{}))
	if _, err := viewer.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_ticket", Arguments: map[string]any{}}); err == nil {
		t.Fatal("viewer called create_ticket")
	}
}

func TestAgentResolvesWithTheKnowledgeBaseThenEscalates(t *testing.T) {
	store := seededStore(t)
	cs := connect(t, NewForPrincipal(store, authz.Principal{Name: "support-bot", Role: authz.Agent}, Options{}))

	hits := decode[SearchArticlesOut](t, call(t, cs, "search_articles", map[string]any{"query": "appointment reminders not sent"}))
	if len(hits.Hits) == 0 || !strings.Contains(hits.Hits[0].Title, "reminders") {
		t.Fatalf("hits = %+v", hits.Hits)
	}
	art := decode[helpdesk.Article](t, call(t, cs, "get_article", map[string]any{"id": hits.Hits[0].ID}))
	if !strings.Contains(art.Body, "SMS credit") {
		t.Fatalf("article body = %q", art.Body)
	}

	tk := decode[helpdesk.Ticket](t, call(t, cs, "create_ticket", map[string]any{
		"subject": "Patient data visible to another clinic", "body": "Reported by a manager at 10:05", "priority": "urgent",
	}))
	if tk.Requester != "support-bot" || tk.Status != helpdesk.StatusOpen {
		t.Fatalf("ticket = %+v", tk)
	}
	esc := decode[helpdesk.Ticket](t, call(t, cs, "escalate_ticket", map[string]any{"ticket_id": tk.ID, "reason": "data incident"}))
	if esc.Status != helpdesk.StatusEscalated || len(esc.Comments) != 1 || esc.Comments[0].Author != "support-bot" {
		t.Fatalf("escalated = %+v", esc)
	}
}

func TestInputValidationAndDomainErrorsAreToolErrors(t *testing.T) {
	store := seededStore(t)
	cs := connect(t, NewForPrincipal(store, authz.Principal{Name: "a", Role: authz.Agent}, Options{}))

	for name, args := range map[string]map[string]any{
		"search_articles": {"query": "   "},
		"create_ticket":   {"subject": "x", "body": "y", "priority": "asap"},
		"list_tickets":    {"status": "lost"},
		"get_ticket":      {"id": -1},
	} {
		res := call(t, cs, name, args)
		if !res.IsError || !strings.Contains(text(res), "invalid input") {
			t.Errorf("%s: want an invalid-input tool error, got %q", name, text(res))
		}
	}
	res := call(t, cs, "get_ticket", map[string]any{"id": 4242})
	if !res.IsError || !strings.Contains(text(res), "not found") {
		t.Errorf("missing ticket: %q", text(res))
	}
}

func TestAdminOnlyArticlesNeedAdmin(t *testing.T) {
	store := seededStore(t)
	agent := connect(t, NewForPrincipal(store, authz.Principal{Name: "a", Role: authz.Agent}, Options{}))
	admin := connect(t, NewForPrincipal(store, authz.Principal{Name: "b", Role: authz.Admin}, Options{}))
	q := map[string]any{"query": "data incident escalation policy"}

	for _, h := range decode[SearchArticlesOut](t, call(t, agent, "search_articles", q)).Hits {
		if strings.Contains(h.Title, "Escalation policy") {
			t.Fatal("agent can see an admin-only article")
		}
	}
	hits := decode[SearchArticlesOut](t, call(t, admin, "search_articles", q)).Hits
	if len(hits) == 0 || !strings.Contains(hits[0].Title, "Escalation policy") {
		t.Fatalf("admin hits = %+v", hits)
	}
}

func TestRateLimitAndAuditTrail(t *testing.T) {
	store := seededStore(t)
	admin := authz.Principal{Name: "admin-1", Role: authz.Admin}
	cs := connect(t, NewForPrincipal(store, admin, Options{RatePerSec: 0.001, Burst: 2}))

	call(t, cs, "search_articles", map[string]any{"query": "refund"})
	call(t, cs, "search_articles", map[string]any{"query": "   "}) // invalid, still consumes a token
	limited := call(t, cs, "search_articles", map[string]any{"query": "refund"})
	if !limited.IsError || !strings.Contains(text(limited), "rate limit") {
		t.Fatalf("third call should be rate limited, got %q", text(limited))
	}

	entries, _ := store.ListAudit(context.Background(), 10)
	var outcomes []string
	for _, e := range entries {
		if e.Principal != "admin-1" || e.Tool != "search_articles" {
			t.Fatalf("unexpected audit entry %+v", e)
		}
		outcomes = append(outcomes, e.Outcome)
	}
	if !slices.Equal(outcomes, []string{"rate_limited", "invalid", "ok"}) { // newest first
		t.Fatalf("audit outcomes = %v", outcomes)
	}
	if !strings.Contains(entries[2].Args, `"refund"`) {
		t.Fatalf("audit args = %q", entries[2].Args)
	}
}

type failingStore struct{ *memstore.Store }

func (failingStore) ListTickets(context.Context, helpdesk.TicketFilter) ([]helpdesk.Ticket, error) {
	return nil, errors.New("pq: connection refused to 10.0.0.5")
}

func TestInternalErrorsAreHiddenButAudited(t *testing.T) {
	store := failingStore{seededStore(t)}
	cs := connect(t, NewForPrincipal(store, authz.Principal{Name: "a", Role: authz.Agent}, Options{}))
	res := call(t, cs, "list_tickets", nil)
	if !res.IsError || strings.Contains(text(res), "10.0.0.5") || !strings.Contains(text(res), "internal error") {
		t.Fatalf("leaked or missing error: %q", text(res))
	}
	entries, _ := store.ListAudit(context.Background(), 1)
	if entries[0].Outcome != "error" || !strings.Contains(entries[0].Error, "10.0.0.5") {
		t.Fatalf("audit should keep the detail: %+v", entries[0])
	}
}

func TestNoCallerMeansNoAccess(t *testing.T) {
	store := seededStore(t)
	// Per-role servers (HTTP) have no fallback principal: a call without a verified token is refused.
	cs := connect(t, New(store, Options{}).For(authz.Agent))
	res := call(t, cs, "list_tickets", nil)
	if !res.IsError || !strings.Contains(text(res), "invalid or unknown API key") {
		t.Fatalf("unauthenticated call: %q", text(res))
	}
}
