package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

// ── Knowledge base ──────────────────────────────────────────────────────────

type SearchArticlesIn struct {
	Query string `json:"query" jsonschema:"what the user is asking about, in plain words"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results, 1-50 (default 5)"`
}

func (in SearchArticlesIn) validate() error { return required("query", in.Query, helpdesk.MaxQuery) }

type SearchArticlesOut struct {
	Hits []helpdesk.ArticleHit `json:"hits"`
}

type GetArticleIn struct {
	ID int64 `json:"id" jsonschema:"article id from search_articles"`
}

func (in GetArticleIn) validate() error { return positive("id", in.ID) }

func registerArticleTools(s *mcp.Server, d *deps, role authz.Role) {
	register(s, d, role, authz.ReadArticles, &mcp.Tool{
		Name:        "search_articles",
		Description: "Search the help centre. Returns ranked article ids, titles and snippets; fetch the full text with get_article.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in SearchArticlesIn) (SearchArticlesOut, error) {
		hits, err := d.store.SearchArticles(ctx, in.Query, p.Role.Can(authz.ReadAdminArticles), helpdesk.ClampLimit(in.Limit, 5))
		if hits == nil {
			hits = []helpdesk.ArticleHit{}
		}
		return SearchArticlesOut{Hits: hits}, err
	})

	register(s, d, role, authz.ReadArticles, &mcp.Tool{
		Name:        "get_article",
		Description: "Read one help-centre article in full.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in GetArticleIn) (helpdesk.Article, error) {
		return d.store.GetArticle(ctx, in.ID, p.Role.Can(authz.ReadAdminArticles))
	})
}

// ── Tickets ─────────────────────────────────────────────────────────────────

type ListTicketsIn struct {
	Status string `json:"status,omitempty" jsonschema:"open, pending, escalated or closed; omit for all"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum results, 1-50 (default 20)"`
}

func (in ListTicketsIn) validate() error {
	if in.Status != "" && !helpdesk.Status(in.Status).Valid() {
		return fmt.Errorf("%w: status must be open, pending, escalated or closed", helpdesk.ErrInvalidInput)
	}
	return nil
}

type ListTicketsOut struct {
	Tickets []helpdesk.Ticket `json:"tickets"`
}

type TicketIn struct {
	ID int64 `json:"id" jsonschema:"ticket id"`
}

func (in TicketIn) validate() error { return positive("id", in.ID) }

type CreateTicketIn struct {
	Subject   string `json:"subject" jsonschema:"one-line summary, at most 200 characters"`
	Body      string `json:"body" jsonschema:"what happened, steps tried, and who is affected"`
	Priority  string `json:"priority,omitempty" jsonschema:"low, normal, high or urgent (default normal)"`
	Requester string `json:"requester,omitempty" jsonschema:"who reported it; defaults to the caller"`
}

type CommentIn struct {
	TicketID int64  `json:"ticket_id" jsonschema:"ticket id"`
	Body     string `json:"body" jsonschema:"the comment"`
}

func (in CommentIn) validate() error {
	if err := positive("ticket_id", in.TicketID); err != nil {
		return err
	}
	return required("body", in.Body, helpdesk.MaxBody)
}

type StatusChangeIn struct {
	TicketID int64  `json:"ticket_id" jsonschema:"ticket id"`
	Reason   string `json:"reason" jsonschema:"why; recorded on the ticket"`
}

func (in StatusChangeIn) validate() error {
	if err := positive("ticket_id", in.TicketID); err != nil {
		return err
	}
	return required("reason", in.Reason, helpdesk.MaxBody)
}

func registerTicketTools(s *mcp.Server, d *deps, role authz.Role) {
	register(s, d, role, authz.ReadTickets, &mcp.Tool{
		Name:        "list_tickets",
		Description: "List support tickets, newest first, optionally filtered by status.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ authz.Principal, in ListTicketsIn) (ListTicketsOut, error) {
		ts, err := d.store.ListTickets(ctx, helpdesk.TicketFilter{Status: helpdesk.Status(in.Status), Limit: helpdesk.ClampLimit(in.Limit, 20)})
		if ts == nil {
			ts = []helpdesk.Ticket{}
		}
		return ListTicketsOut{Tickets: ts}, err
	})

	register(s, d, role, authz.ReadTickets, &mcp.Tool{
		Name:        "get_ticket",
		Description: "Read one ticket with its full comment history.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ authz.Principal, in TicketIn) (helpdesk.Ticket, error) {
		return d.store.GetTicket(ctx, in.ID)
	})

	register(s, d, role, authz.WriteTickets, &mcp.Tool{
		Name:        "create_ticket",
		Description: "Open a support ticket. Search the knowledge base first; create a ticket only when it does not resolve the issue.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in CreateTicketIn) (helpdesk.Ticket, error) {
		n := helpdesk.NewTicket{Subject: in.Subject, Body: in.Body, Priority: helpdesk.Priority(in.Priority), Requester: in.Requester}
		if n.Priority == "" {
			n.Priority = helpdesk.PriorityNormal
		}
		if n.Requester == "" {
			n.Requester = p.Name
		}
		if err := n.Validate(); err != nil {
			return helpdesk.Ticket{}, err
		}
		return d.store.CreateTicket(ctx, n)
	})

	register(s, d, role, authz.WriteTickets, &mcp.Tool{
		Name:        "add_comment",
		Description: "Add a comment to an open ticket.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in CommentIn) (helpdesk.Comment, error) {
		return d.store.AddComment(ctx, in.TicketID, p.Name, in.Body)
	})

	register(s, d, role, authz.WriteTickets, &mcp.Tool{
		Name:        "escalate_ticket",
		Description: "Escalate a ticket to the on-call engineer. Use for data incidents and outages.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in StatusChangeIn) (helpdesk.Ticket, error) {
		return d.store.SetStatus(ctx, in.TicketID, helpdesk.StatusEscalated, p.Name, in.Reason)
	})
}

// ── Admin ───────────────────────────────────────────────────────────────────

type AuditIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum entries, 1-50 (default 20)"`
}

type AuditOut struct {
	Entries []helpdesk.AuditEntry `json:"entries"`
}

func registerAdminTools(s *mcp.Server, d *deps, role authz.Role) {
	register(s, d, role, authz.CloseTickets, &mcp.Tool{
		Name:        "close_ticket",
		Description: "Close a resolved ticket. Closed tickets cannot be reopened or commented on.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, p authz.Principal, in StatusChangeIn) (helpdesk.Ticket, error) {
		return d.store.SetStatus(ctx, in.TicketID, helpdesk.StatusClosed, p.Name, in.Reason)
	})

	register(s, d, role, authz.ReadAudit, &mcp.Tool{
		Name:        "get_audit_log",
		Description: "Recent tool calls: who called what, the outcome and how long it took.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)},
	}, func(ctx context.Context, _ authz.Principal, in AuditIn) (AuditOut, error) {
		es, err := d.store.ListAudit(ctx, helpdesk.ClampLimit(in.Limit, 20))
		if es == nil {
			es = []helpdesk.AuditEntry{}
		}
		return AuditOut{Entries: es}, err
	})
}
