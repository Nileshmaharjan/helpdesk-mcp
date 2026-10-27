package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

// Runs against a real database: TEST_DATABASE_URL=postgres://helpdesk:helpdesk@localhost:5434/helpdesk go test ./...
// The schema is dropped and recreated, so never point this at data you care about.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.pool.Exec(ctx, `DROP TABLE IF EXISTS comments, tickets, articles, audit_log, api_keys`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil { // idempotent
		t.Fatalf("second migrate: %v", err)
	}
	if err := s.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFullTextSearchAndAudience(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	hits, err := s.SearchArticles(ctx, "reset password", false, 5)
	if err != nil || len(hits) == 0 || hits[0].Title != "Resetting a staff member's password" {
		t.Fatalf("hits = %+v, %v", hits, err)
	}
	// Stemming: "refunds" matches "Refunding a payment".
	if hits, _ := s.SearchArticles(ctx, "refunds", false, 5); len(hits) == 0 || !strings.Contains(hits[0].Title, "Refund") {
		t.Fatalf("stemmed search = %+v", hits)
	}
	// Free text with operators must never be a syntax error.
	if _, err := s.SearchArticles(ctx, `"kiosk" OR -frozen ) (`, false, 5); err != nil {
		t.Fatalf("websearch syntax: %v", err)
	}
	for _, h := range mustSearch(t, s, "restore deleted patient record", false) {
		if strings.Contains(h.Title, "Restoring") {
			t.Fatal("admin-only article returned to a non-admin")
		}
	}
	if hits := mustSearch(t, s, "restore deleted patient record", true); len(hits) == 0 || !strings.Contains(hits[0].Title, "Restoring") {
		t.Fatalf("admin search = %+v", hits)
	}
}

func mustSearch(t *testing.T, s *Store, q string, admin bool) []helpdesk.ArticleHit {
	t.Helper()
	hits, err := s.SearchArticles(context.Background(), q, admin, 10)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestTicketLifecycleAndConcurrentClose(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	tk, err := s.CreateTicket(ctx, helpdesk.NewTicket{Subject: "SMS down", Body: "No reminders", Priority: helpdesk.PriorityHigh, Requester: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, tk.ID, "agent", "checking"); err != nil {
		t.Fatal(err)
	}

	// Ten agents race to close the same ticket: the row lock lets exactly one win.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SetStatus(ctx, tk.ID, helpdesk.StatusClosed, "admin", "done"); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, helpdesk.ErrInvalidChange) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d closes succeeded, want exactly 1", wins)
	}

	got, err := s.GetTicket(ctx, tk.ID)
	if err != nil || got.Status != helpdesk.StatusClosed || len(got.Comments) != 2 {
		t.Fatalf("ticket = %+v, %v", got, err)
	}
	if _, err := s.AddComment(ctx, tk.ID, "agent", "late"); !errors.Is(err, helpdesk.ErrInvalidChange) {
		t.Fatalf("comment on closed: %v", err)
	}
	if _, err := s.GetTicket(ctx, 999999); !errors.Is(err, helpdesk.ErrNotFound) {
		t.Fatalf("missing ticket: %v", err)
	}
}

func TestAuditAndKeys(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.RecordAudit(ctx, helpdesk.AuditEntry{Principal: "p", Role: "agent", Tool: "get_ticket", Args: `{"id":1}`, Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	es, err := s.ListAudit(ctx, 5)
	if err != nil || len(es) != 1 || es[0].Tool != "get_ticket" {
		t.Fatalf("audit = %+v, %v", es, err)
	}
	if err := s.CreateAPIKey(ctx, helpdesk.APIKey{Name: "bot", Role: "agent", KeyHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if k, err := s.APIKeyByHash(ctx, "abc"); err != nil || k.Name != "bot" {
		t.Fatalf("key = %+v, %v", k, err)
	}
	if _, err := s.APIKeyByHash(ctx, "nope"); !errors.Is(err, helpdesk.ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}
