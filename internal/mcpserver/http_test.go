package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

type bearer struct{ key string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(r)
}

func httpSession(t *testing.T, url, key string) (*mcp.ClientSession, error) {
	t.Helper()
	tr := &mcp.StreamableClientTransport{Endpoint: url + "/mcp", HTTPClient: &http.Client{Transport: bearer{key}}, MaxRetries: -1}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "0"}, nil).Connect(context.Background(), tr, nil)
	if err == nil {
		t.Cleanup(func() { cs.Close() })
	}
	return cs, err
}

func TestHTTPRequiresAValidKeyAndScopesToolsByRole(t *testing.T) {
	ctx := context.Background()
	store := seededStore(t)
	viewerKey, _ := authz.Issue(ctx, store, "kiosk-bot", authz.Viewer)
	adminKey, _ := authz.Issue(ctx, store, "ops-lead", authz.Admin)
	srv := httptest.NewServer(HTTPHandler(store, Options{}))
	t.Cleanup(srv.Close) // registered first, so it runs after the client sessions close

	for _, key := range []string{"", "hd_wrong"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("key %q: status %d, want 401", key, resp.StatusCode)
		}
	}

	viewer, err := httpSession(t, srv.URL, viewerKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(t, viewer); !slices.Equal(got, []string{"get_article", "search_articles"}) {
		t.Fatalf("viewer tools over HTTP = %v", got)
	}

	admin, err := httpSession(t, srv.URL, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	tk := decode[helpdesk.Ticket](t, call(t, admin, "create_ticket", map[string]any{"subject": "Over HTTP", "body": "b"}))
	if tk.Requester != "ops-lead" {
		t.Fatalf("requester should come from the verified key, got %q", tk.Requester)
	}
	audit := decode[AuditOut](t, call(t, admin, "get_audit_log", map[string]any{"limit": 5}))
	if len(audit.Entries) == 0 || audit.Entries[0].Principal != "ops-lead" {
		t.Fatalf("audit = %+v", audit.Entries)
	}

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", resp, err)
	}
}
