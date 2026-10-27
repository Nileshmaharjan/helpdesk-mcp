// Package mcpserver exposes the help centre to AI agents as MCP tools.
//
// Every tool call goes through the same guard: resolve the caller, check the role's permission, apply the
// per-caller rate limit, validate the input, run the handler, and write an audit entry, whatever happened.
// Tools a role may not use are not registered on that role's server at all, so an agent never sees them in
// tools/list; the permission check at call time is a second line of defence.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

const Version = "0.1.0"

type Options struct {
	// RatePerSec and Burst size each caller's token bucket. RatePerSec <= 0 disables rate limiting.
	RatePerSec float64
	Burst      int
	Logger     *slog.Logger
	// Now is the clock used for audit timings; tests replace it.
	Now func() time.Time
}

type deps struct {
	store   helpdesk.Store
	limiter *limiter
	log     *slog.Logger
	now     func() time.Time
	// fallback is the caller for transports without per-request auth (stdio), where the key is checked once
	// at startup. Over HTTP the caller comes from the verified bearer token on each request.
	fallback *authz.Principal
}

// Servers holds one MCP server per role, sharing a store and a rate limiter.
type Servers struct {
	byRole map[authz.Role]*mcp.Server
}

func (s *Servers) For(role authz.Role) *mcp.Server { return s.byRole[role] }

// New builds the per-role servers used by the HTTP transport.
func New(store helpdesk.Store, opts Options) *Servers {
	d := newDeps(store, opts, nil)
	out := &Servers{byRole: map[authz.Role]*mcp.Server{}}
	for _, r := range []authz.Role{authz.Viewer, authz.Agent, authz.Admin} {
		out.byRole[r] = build(d, r)
	}
	return out
}

// NewForPrincipal builds a single server bound to one caller, for stdio.
func NewForPrincipal(store helpdesk.Store, p authz.Principal, opts Options) *mcp.Server {
	return build(newDeps(store, opts, &p), p.Role)
}

func newDeps(store helpdesk.Store, opts Options, fallback *authz.Principal) *deps {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &deps{store: store, limiter: newLimiter(opts.RatePerSec, opts.Burst), log: opts.Logger, now: opts.Now, fallback: fallback}
}

const instructions = `Help-centre tools for ClinicDesk support. Search the knowledge base before answering a product
question and cite the article id you used. Create a ticket only when the knowledge base does not resolve the
issue. Escalate data incidents (patient data visible to the wrong clinic) immediately.`

func build(d *deps, role authz.Role) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "helpdesk-mcp", Version: Version},
		&mcp.ServerOptions{Instructions: instructions})
	registerArticleTools(s, d, role)
	registerTicketTools(s, d, role)
	registerAdminTools(s, d, role)
	return s
}

func (d *deps) principal(req *mcp.CallToolRequest) (authz.Principal, error) {
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil {
		return PrincipalFromToken(req.Extra.TokenInfo)
	}
	if d.fallback != nil {
		return *d.fallback, nil
	}
	return authz.Principal{}, authz.ErrUnauthenticated
}

// PrincipalFromToken reads the principal that the HTTP auth middleware stored in the token info.
func PrincipalFromToken(ti *auth.TokenInfo) (authz.Principal, error) {
	role, err := authz.ParseRole(fmt.Sprint(ti.Extra["role"]))
	if err != nil || ti.UserID == "" {
		return authz.Principal{}, authz.ErrUnauthenticated
	}
	return authz.Principal{Name: ti.UserID, Role: role}, nil
}

type validator interface{ validate() error }

var (
	errDenied      = errors.New("permission denied")
	errRateLimited = errors.New("rate limit exceeded; retry shortly")
)

// register adds a tool to the server if the role may use it, wrapped in the guard described above.
func register[In, Out any](s *mcp.Server, d *deps, role authz.Role, perm authz.Permission, tool *mcp.Tool,
	h func(ctx context.Context, p authz.Principal, in In) (Out, error)) {
	if !role.Can(perm) {
		return
	}
	mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		start := d.now()
		p, err := d.principal(req)
		outcome := "ok"
		defer func() {
			d.audit(ctx, p, tool.Name, in, outcome, err, d.now().Sub(start))
		}()

		switch {
		case err != nil:
			outcome = "denied"
			return nil, zero, err
		case !p.Role.Can(perm):
			outcome, err = "denied", fmt.Errorf("%w: %s needs %s", errDenied, tool.Name, perm)
			return nil, zero, err
		case !d.limiter.allow(p.Name):
			outcome, err = "rate_limited", errRateLimited
			return nil, zero, err
		}
		if v, ok := any(in).(validator); ok {
			if err = v.validate(); err != nil {
				outcome = "invalid"
				return nil, zero, err
			}
		}
		out, herr := h(ctx, p, in)
		if herr != nil {
			err = herr
			outcome = "error"
			if errors.Is(herr, helpdesk.ErrInvalidInput) || errors.Is(herr, helpdesk.ErrInvalidChange) {
				outcome = "invalid"
			}
			return nil, zero, publicError(herr)
		}
		return nil, out, nil
	})
}

// publicError keeps domain errors (not found, invalid change) readable for the agent and hides anything else,
// such as database errors, behind a generic message; the audit log keeps the detail.
func publicError(err error) error {
	for _, known := range []error{helpdesk.ErrNotFound, helpdesk.ErrInvalidInput, helpdesk.ErrInvalidChange} {
		if errors.Is(err, known) {
			return err
		}
	}
	return errors.New("internal error; the failure was logged")
}

const maxAuditArgs = 1000

func (d *deps) audit(ctx context.Context, p authz.Principal, tool string, in any, outcome string, err error, dur time.Duration) {
	args, _ := json.Marshal(in)
	s := string(args)
	if len(s) > maxAuditArgs {
		s = s[:maxAuditArgs] + "…"
	}
	e := helpdesk.AuditEntry{At: d.now().UTC(), Principal: p.Name, Role: string(p.Role), Tool: tool, Args: s,
		Outcome: outcome, DurationMS: dur.Milliseconds()}
	if err != nil {
		e.Error = err.Error()
	}
	// The audit write must not depend on the caller's context, which may already be cancelled.
	if aerr := d.store.RecordAudit(context.WithoutCancel(ctx), e); aerr != nil {
		d.log.Error("audit write failed", "tool", tool, "err", aerr)
	}
	d.log.Info("tool call", "principal", p.Name, "role", p.Role, "tool", tool, "outcome", outcome, "ms", e.DurationMS)
}

func ptr[T any](v T) *T { return &v }

func required(field, v string, max int) error {
	switch {
	case strings.TrimSpace(v) == "":
		return fmt.Errorf("%w: %s is required", helpdesk.ErrInvalidInput, field)
	case len(v) > max:
		return fmt.Errorf("%w: %s is longer than %d characters", helpdesk.ErrInvalidInput, field, max)
	}
	return nil
}

func positive(field string, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: %s must be a positive integer", helpdesk.ErrInvalidInput, field)
	}
	return nil
}
