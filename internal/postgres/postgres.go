// Package postgres is the production helpdesk.Store: Postgres with full-text search over the knowledge base.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

//go:embed schema.sql
var schema string

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Migrate applies the idempotent schema.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Seed loads the demo articles and tickets into an empty database; it does nothing if articles exist.
func (s *Store) Seed(ctx context.Context) error {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM articles`).Scan(&n); err != nil || n > 0 {
		return err
	}
	for _, a := range helpdesk.SeedArticles {
		if _, err := s.pool.Exec(ctx, `INSERT INTO articles (title, body, category, audience) VALUES ($1, $2, $3, $4)`,
			a.Title, a.Body, a.Category, a.Audience); err != nil {
			return err
		}
	}
	for _, t := range helpdesk.SeedTickets {
		if _, err := s.CreateTicket(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SearchArticles(ctx context.Context, query string, includeAdmin bool, limit int) ([]helpdesk.ArticleHit, error) {
	// websearch_to_tsquery accepts free text safely (quotes, OR, -exclusions) and never raises a syntax error.
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, category,
		       ts_headline('english', body, q, 'MaxWords=30, MinWords=10') AS snippet,
		       ts_rank(search, q) AS score
		FROM articles, websearch_to_tsquery('english', $1) q
		WHERE search @@ q AND (audience = 'all' OR $2)
		ORDER BY score DESC, id
		LIMIT $3`, query, includeAdmin, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (helpdesk.ArticleHit, error) {
		var h helpdesk.ArticleHit
		var score float32
		err := r.Scan(&h.ID, &h.Title, &h.Category, &h.Snippet, &score)
		h.Score = float64(score)
		return h, err
	})
}

func (s *Store) GetArticle(ctx context.Context, id int64, includeAdmin bool) (helpdesk.Article, error) {
	var a helpdesk.Article
	err := s.pool.QueryRow(ctx, `
		SELECT id, title, body, category, audience, updated_at FROM articles
		WHERE id = $1 AND (audience = 'all' OR $2)`, id, includeAdmin).
		Scan(&a.ID, &a.Title, &a.Body, &a.Category, &a.Audience, &a.UpdatedAt)
	return a, notFound(err, "article", id)
}

const ticketCols = `id, subject, body, status, priority, requester, created_at, updated_at`

func scanTicket(r pgx.Row) (helpdesk.Ticket, error) {
	var t helpdesk.Ticket
	err := r.Scan(&t.ID, &t.Subject, &t.Body, &t.Status, &t.Priority, &t.Requester, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) ListTickets(ctx context.Context, f helpdesk.TicketFilter) ([]helpdesk.Ticket, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ticketCols+` FROM tickets
		WHERE ($1 = '' OR status = $1) ORDER BY id DESC LIMIT $2`, string(f.Status), f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (helpdesk.Ticket, error) { return scanTicket(r) })
}

func (s *Store) GetTicket(ctx context.Context, id int64) (helpdesk.Ticket, error) {
	t, err := scanTicket(s.pool.QueryRow(ctx, `SELECT `+ticketCols+` FROM tickets WHERE id = $1`, id))
	if err != nil {
		return t, notFound(err, "ticket", id)
	}
	rows, err := s.pool.Query(ctx, `SELECT id, ticket_id, author, body, created_at FROM comments
		WHERE ticket_id = $1 ORDER BY id`, id)
	if err != nil {
		return t, err
	}
	t.Comments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (helpdesk.Comment, error) {
		var c helpdesk.Comment
		return c, r.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &c.CreatedAt)
	})
	return t, err
}

func (s *Store) CreateTicket(ctx context.Context, n helpdesk.NewTicket) (helpdesk.Ticket, error) {
	if err := n.Validate(); err != nil {
		return helpdesk.Ticket{}, err
	}
	return scanTicket(s.pool.QueryRow(ctx, `
		INSERT INTO tickets (subject, body, status, priority, requester) VALUES ($1, $2, 'open', $3, $4)
		RETURNING `+ticketCols, n.Subject, n.Body, n.Priority, n.Requester))
}

func (s *Store) AddComment(ctx context.Context, ticketID int64, author, body string) (helpdesk.Comment, error) {
	var c helpdesk.Comment
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		// Lock the ticket row so a comment cannot race a close.
		if err := tx.QueryRow(ctx, `SELECT status FROM tickets WHERE id = $1 FOR UPDATE`, ticketID).Scan(&status); err != nil {
			return notFound(err, "ticket", ticketID)
		}
		if status == string(helpdesk.StatusClosed) {
			return fmt.Errorf("ticket %d is closed: %w", ticketID, helpdesk.ErrInvalidChange)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO comments (ticket_id, author, body) VALUES ($1, $2, $3)
			RETURNING id, ticket_id, author, body, created_at`, ticketID, author, body).
			Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE tickets SET updated_at = now() WHERE id = $1`, ticketID)
		return err
	})
	return c, err
}

func (s *Store) SetStatus(ctx context.Context, ticketID int64, to helpdesk.Status, author, note string) (helpdesk.Ticket, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var from helpdesk.Status
		if err := tx.QueryRow(ctx, `SELECT status FROM tickets WHERE id = $1 FOR UPDATE`, ticketID).Scan(&from); err != nil {
			return notFound(err, "ticket", ticketID)
		}
		if !helpdesk.CanTransition(from, to) {
			return fmt.Errorf("ticket %d cannot go from %s to %s: %w", ticketID, from, to, helpdesk.ErrInvalidChange)
		}
		if _, err := tx.Exec(ctx, `UPDATE tickets SET status = $2, updated_at = now() WHERE id = $1`, ticketID, to); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO comments (ticket_id, author, body) VALUES ($1, $2, $3)`,
			ticketID, author, fmt.Sprintf("Status changed to %s: %s", to, note))
		return err
	})
	if err != nil {
		return helpdesk.Ticket{}, err
	}
	return s.GetTicket(ctx, ticketID)
}

func (s *Store) RecordAudit(ctx context.Context, e helpdesk.AuditEntry) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_log (at, principal, role, tool, args, outcome, error, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, e.At, e.Principal, e.Role, e.Tool, e.Args, e.Outcome, e.Error, e.DurationMS)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]helpdesk.AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, at, principal, role, tool, args, outcome, error, duration_ms
		FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (helpdesk.AuditEntry, error) {
		var e helpdesk.AuditEntry
		return e, r.Scan(&e.ID, &e.At, &e.Principal, &e.Role, &e.Tool, &e.Args, &e.Outcome, &e.Error, &e.DurationMS)
	})
}

func (s *Store) APIKeyByHash(ctx context.Context, hash string) (helpdesk.APIKey, error) {
	k := helpdesk.APIKey{KeyHash: hash}
	err := s.pool.QueryRow(ctx, `SELECT name, role FROM api_keys WHERE key_hash = $1`, hash).Scan(&k.Name, &k.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, helpdesk.ErrNotFound
	}
	return k, err
}

func (s *Store) CreateAPIKey(ctx context.Context, k helpdesk.APIKey) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO api_keys (key_hash, name, role) VALUES ($1, $2, $3)`, k.KeyHash, k.Name, k.Role)
	return err
}

func notFound(err error, what string, id int64) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s %d: %w", what, id, helpdesk.ErrNotFound)
	}
	return err
}

var _ helpdesk.Store = (*Store)(nil)
