// Package helpdesk is the domain model of a small help centre: knowledge-base articles and support tickets.
// It knows nothing about MCP or HTTP; the server exposes it to agents through a Store.
package helpdesk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Audience controls who may read an article. Admin-only articles are hidden from everyone else,
// including from search results.
type Audience string

const (
	AudienceAll   Audience = "all"
	AudienceAdmin Audience = "admin"
)

type Article struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Category  string    `json:"category"`
	Audience  Audience  `json:"audience"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ArticleHit is a search result: the article without its full body, plus a relevance score.
type ArticleHit struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Snippet  string  `json:"snippet"`
	Score    float64 `json:"score"`
}

type Status string

const (
	StatusOpen      Status = "open"
	StatusPending   Status = "pending"
	StatusEscalated Status = "escalated"
	StatusClosed    Status = "closed"
)

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

func (p Priority) Valid() bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
		return true
	}
	return false
}

func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusPending, StatusEscalated, StatusClosed:
		return true
	}
	return false
}

type Ticket struct {
	ID        int64     `json:"id"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Status    Status    `json:"status"`
	Priority  Priority  `json:"priority"`
	Requester string    `json:"requester"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Comments  []Comment `json:"comments,omitempty"`
}

type Comment struct {
	ID        int64     `json:"id"`
	TicketID  int64     `json:"ticket_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// NewTicket is the input for creating a ticket.
type NewTicket struct {
	Subject   string
	Body      string
	Priority  Priority
	Requester string
}

// TicketFilter narrows ListTickets. A zero Status means any status.
type TicketFilter struct {
	Status Status
	Limit  int
}

// AuditEntry records one tool call: who called what, with which arguments, and how it ended.
type AuditEntry struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Principal  string    `json:"principal"`
	Role       string    `json:"role"`
	Tool       string    `json:"tool"`
	Args       string    `json:"args"`
	Outcome    string    `json:"outcome"` // ok, denied, invalid, rate_limited, error
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
}

// APIKey is a stored credential. Only the SHA-256 hash of the key is kept.
type APIKey struct {
	Name    string
	Role    string
	KeyHash string
}

var (
	ErrNotFound      = errors.New("not found")
	ErrInvalidInput  = errors.New("invalid input")
	ErrInvalidChange = errors.New("invalid state change")
)

// Store is everything the MCP tools need. memstore and postgres implement it.
type Store interface {
	SearchArticles(ctx context.Context, query string, includeAdmin bool, limit int) ([]ArticleHit, error)
	GetArticle(ctx context.Context, id int64, includeAdmin bool) (Article, error)

	ListTickets(ctx context.Context, f TicketFilter) ([]Ticket, error)
	GetTicket(ctx context.Context, id int64) (Ticket, error)
	CreateTicket(ctx context.Context, t NewTicket) (Ticket, error)
	AddComment(ctx context.Context, ticketID int64, author, body string) (Comment, error)
	SetStatus(ctx context.Context, ticketID int64, to Status, author, note string) (Ticket, error)

	RecordAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)

	APIKeyByHash(ctx context.Context, hash string) (APIKey, error)
	CreateAPIKey(ctx context.Context, k APIKey) error
}

// Field limits, enforced before anything reaches a store.
const (
	MaxSubject = 200
	MaxBody    = 5000
	MaxQuery   = 200
	MaxLimit   = 50
)

func (t NewTicket) Validate() error {
	switch {
	case strings.TrimSpace(t.Subject) == "":
		return fmt.Errorf("%w: subject is required", ErrInvalidInput)
	case len(t.Subject) > MaxSubject:
		return fmt.Errorf("%w: subject is longer than %d characters", ErrInvalidInput, MaxSubject)
	case strings.TrimSpace(t.Body) == "":
		return fmt.Errorf("%w: body is required", ErrInvalidInput)
	case len(t.Body) > MaxBody:
		return fmt.Errorf("%w: body is longer than %d characters", ErrInvalidInput, MaxBody)
	case !t.Priority.Valid():
		return fmt.Errorf("%w: priority must be low, normal, high or urgent", ErrInvalidInput)
	}
	return nil
}

// CanTransition encodes the ticket lifecycle. Closed tickets are final; escalation and closing need a reason.
func CanTransition(from, to Status) bool {
	if from == StatusClosed || from == to {
		return false
	}
	switch to {
	case StatusPending, StatusEscalated, StatusClosed, StatusOpen:
		return true
	}
	return false
}

// ClampLimit applies the default and the ceiling to a caller-supplied limit.
func ClampLimit(limit, def int) int {
	if limit <= 0 {
		return def
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}
