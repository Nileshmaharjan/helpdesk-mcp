// Package memstore is an in-memory helpdesk.Store for tests and for running the server without Postgres.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

type Store struct {
	mu       sync.Mutex
	now      func() time.Time
	articles []helpdesk.Article
	tickets  map[int64]*helpdesk.Ticket
	nextID   int64
	audit    []helpdesk.AuditEntry
	keys     map[string]helpdesk.APIKey
}

// New returns an empty store. Seed fills it with the demo help centre.
func New() *Store {
	return &Store{now: time.Now, tickets: map[int64]*helpdesk.Ticket{}, keys: map[string]helpdesk.APIKey{}}
}

func (s *Store) Seed(ctx context.Context) error {
	s.mu.Lock()
	for i, a := range helpdesk.SeedArticles {
		a.ID = int64(i + 1)
		a.UpdatedAt = s.now()
		s.articles = append(s.articles, a)
	}
	s.mu.Unlock()
	for _, t := range helpdesk.SeedTickets {
		if _, err := s.CreateTicket(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) id() int64 { s.nextID++; return s.nextID }

func visible(a helpdesk.Article, includeAdmin bool) bool {
	return includeAdmin || a.Audience != helpdesk.AudienceAdmin
}

// SearchArticles ranks by how many query terms appear, title matches counting double. Postgres uses
// full-text search instead; both return the same shape.
func (s *Store) SearchArticles(_ context.Context, query string, includeAdmin bool, limit int) ([]helpdesk.ArticleHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	terms := strings.Fields(strings.ToLower(query))
	var hits []helpdesk.ArticleHit
	for _, a := range s.articles {
		if !visible(a, includeAdmin) {
			continue
		}
		title, body := strings.ToLower(a.Title), strings.ToLower(a.Body)
		score := 0.0
		for _, t := range terms {
			if strings.Contains(title, t) {
				score += 2
			}
			if strings.Contains(body, t) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, helpdesk.ArticleHit{ID: a.ID, Title: a.Title, Category: a.Category, Snippet: snippet(a.Body), Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func snippet(body string) string {
	if len(body) <= 160 {
		return body
	}
	return body[:157] + "..."
}

func (s *Store) GetArticle(_ context.Context, id int64, includeAdmin bool) (helpdesk.Article, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.articles {
		if a.ID == id && visible(a, includeAdmin) {
			return a, nil
		}
	}
	return helpdesk.Article{}, fmt.Errorf("article %d: %w", id, helpdesk.ErrNotFound)
}

func (s *Store) ListTickets(_ context.Context, f helpdesk.TicketFilter) ([]helpdesk.Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []helpdesk.Ticket
	for _, t := range s.tickets {
		if f.Status == "" || t.Status == f.Status {
			c := *t
			c.Comments = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) GetTicket(_ context.Context, id int64) (helpdesk.Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok {
		return helpdesk.Ticket{}, fmt.Errorf("ticket %d: %w", id, helpdesk.ErrNotFound)
	}
	c := *t
	c.Comments = append([]helpdesk.Comment(nil), t.Comments...)
	return c, nil
}

func (s *Store) CreateTicket(_ context.Context, n helpdesk.NewTicket) (helpdesk.Ticket, error) {
	if err := n.Validate(); err != nil {
		return helpdesk.Ticket{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	t := &helpdesk.Ticket{ID: s.id(), Subject: n.Subject, Body: n.Body, Status: helpdesk.StatusOpen,
		Priority: n.Priority, Requester: n.Requester, CreatedAt: now, UpdatedAt: now}
	s.tickets[t.ID] = t
	return *t, nil
}

func (s *Store) AddComment(_ context.Context, ticketID int64, author, body string) (helpdesk.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[ticketID]
	if !ok {
		return helpdesk.Comment{}, fmt.Errorf("ticket %d: %w", ticketID, helpdesk.ErrNotFound)
	}
	if t.Status == helpdesk.StatusClosed {
		return helpdesk.Comment{}, fmt.Errorf("ticket %d is closed: %w", ticketID, helpdesk.ErrInvalidChange)
	}
	c := helpdesk.Comment{ID: s.id(), TicketID: ticketID, Author: author, Body: body, CreatedAt: s.now()}
	t.Comments = append(t.Comments, c)
	t.UpdatedAt = c.CreatedAt
	return c, nil
}

func (s *Store) SetStatus(ctx context.Context, ticketID int64, to helpdesk.Status, author, note string) (helpdesk.Ticket, error) {
	s.mu.Lock()
	t, ok := s.tickets[ticketID]
	if !ok {
		s.mu.Unlock()
		return helpdesk.Ticket{}, fmt.Errorf("ticket %d: %w", ticketID, helpdesk.ErrNotFound)
	}
	if !helpdesk.CanTransition(t.Status, to) {
		from := t.Status
		s.mu.Unlock()
		return helpdesk.Ticket{}, fmt.Errorf("ticket %d cannot go from %s to %s: %w", ticketID, from, to, helpdesk.ErrInvalidChange)
	}
	t.Status = to
	t.UpdatedAt = s.now()
	t.Comments = append(t.Comments, helpdesk.Comment{ID: s.id(), TicketID: ticketID, Author: author,
		Body: fmt.Sprintf("Status changed to %s: %s", to, note), CreatedAt: t.UpdatedAt})
	s.mu.Unlock()
	return s.GetTicket(ctx, ticketID)
}

func (s *Store) RecordAudit(_ context.Context, e helpdesk.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.ID = s.id()
	if e.At.IsZero() {
		e.At = s.now()
	}
	s.audit = append(s.audit, e)
	return nil
}

func (s *Store) ListAudit(_ context.Context, limit int) ([]helpdesk.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]helpdesk.AuditEntry, 0, limit)
	for i := len(s.audit) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, s.audit[i])
	}
	return out, nil
}

func (s *Store) APIKeyByHash(_ context.Context, hash string) (helpdesk.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[hash]
	if !ok {
		return helpdesk.APIKey{}, helpdesk.ErrNotFound
	}
	return k, nil
}

func (s *Store) CreateAPIKey(_ context.Context, k helpdesk.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[k.KeyHash] = k
	return nil
}

var _ helpdesk.Store = (*Store)(nil)
