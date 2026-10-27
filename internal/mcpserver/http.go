package mcpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

// HTTPHandler serves MCP over streamable HTTP at the returned handler. Every request must carry
// `Authorization: Bearer <api key>`; the key decides which role's server (and so which tools) the caller gets.
func HTTPHandler(store helpdesk.Store, opts Options) http.Handler {
	servers := New(store, opts)

	verify := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		p, err := authz.Authenticate(ctx, store, strings.TrimSpace(token))
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: p.Name, Scopes: []string{string(p.Role)}, Extra: map[string]any{"role": string(p.Role)}}, nil
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		ti := auth.TokenInfoFromContext(r.Context())
		if ti == nil {
			return nil
		}
		p, err := PrincipalFromToken(ti)
		if err != nil {
			return nil
		}
		return servers.For(p.Role)
	}, &mcp.StreamableHTTPOptions{Logger: opts.Logger})

	// API keys carry no expiry; revocation is done by deleting the key.
	protected := auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(mcpHandler)

	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}
