package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

func seeded(t *testing.T) *Store {
	t.Helper()
	s := New()
	if err := s.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSearchRanksAndHidesAdminArticles(t *testing.T) {
	ctx := context.Background()
	s := seeded(t)

	hits, _ := s.SearchArticles(ctx, "password reset", false, 5)
	if len(hits) == 0 || hits[0].Title != "Resetting a staff member's password" {
		t.Fatalf("best hit = %+v", hits)
	}

	staff, _ := s.SearchArticles(ctx, "deleted patient record restore", false, 10)
	for _, h := range staff {
		if h.Title == "Restoring a deleted patient record" {
			t.Fatal("admin-only article leaked into a non-admin search")
		}
	}
	admin, _ := s.SearchArticles(ctx, "deleted patient record restore", true, 10)
	if len(admin) == 0 || admin[0].Title != "Restoring a deleted patient record" {
		t.Fatalf("admin search = %+v", admin)
	}

	id := admin[0].ID
	if _, err := s.GetArticle(ctx, id, false); !errors.Is(err, helpdesk.ErrNotFound) {
		t.Fatalf("non-admin GetArticle of admin article: %v", err)
	}
}

func TestTicketLifecycle(t *testing.T) {
	ctx := context.Background()
	s := seeded(t)
	tk, err := s.CreateTicket(ctx, helpdesk.NewTicket{Subject: "SMS down", Body: "No reminders", Priority: helpdesk.PriorityUrgent, Requester: "r"})
	if err != nil || tk.Status != helpdesk.StatusOpen {
		t.Fatalf("create: %+v %v", tk, err)
	}
	if _, err := s.AddComment(ctx, tk.ID, "agent-1", "Looking into it"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, tk.ID, helpdesk.StatusEscalated, "agent-1", "provider outage"); err != nil {
		t.Fatal(err)
	}
	closed, err := s.SetStatus(ctx, tk.ID, helpdesk.StatusClosed, "admin-1", "provider recovered")
	if err != nil || closed.Status != helpdesk.StatusClosed || len(closed.Comments) != 3 {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, err := s.AddComment(ctx, tk.ID, "agent-1", "late"); !errors.Is(err, helpdesk.ErrInvalidChange) {
		t.Fatalf("comment on closed ticket: %v", err)
	}
	if _, err := s.SetStatus(ctx, tk.ID, helpdesk.StatusOpen, "agent-1", "reopen"); !errors.Is(err, helpdesk.ErrInvalidChange) {
		t.Fatalf("reopen closed ticket: %v", err)
	}
	if _, err := s.GetTicket(ctx, 9999); !errors.Is(err, helpdesk.ErrNotFound) {
		t.Fatalf("missing ticket: %v", err)
	}

	open, _ := s.ListTickets(ctx, helpdesk.TicketFilter{Status: helpdesk.StatusOpen, Limit: 50})
	for _, o := range open {
		if o.Status != helpdesk.StatusOpen {
			t.Fatalf("status filter leaked %s", o.Status)
		}
	}
}
