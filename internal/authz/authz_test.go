package authz

import (
	"context"
	"strings"
	"testing"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/memstore"
)

func TestRolePermissions(t *testing.T) {
	if Viewer.Can(ReadTickets) || Agent.Can(CloseTickets) || Agent.Can(ReadAdminArticles) || Agent.Can(ReadAudit) {
		t.Fatal("a role has a permission it should not")
	}
	for _, p := range []Permission{ReadArticles, ReadAdminArticles, ReadTickets, WriteTickets, CloseTickets, ReadAudit} {
		if !Admin.Can(p) {
			t.Errorf("admin lacks %s", p)
		}
	}
	if _, err := ParseRole("superuser"); err == nil {
		t.Fatal("unknown role accepted")
	}
	if r, _ := ParseRole(" Agent "); r != Agent {
		t.Fatalf("ParseRole should normalise case and spaces, got %q", r)
	}
}

func TestIssueAndAuthenticate(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	key, err := Issue(ctx, store, "claude-code", Agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "hd_") || len(key) < 40 {
		t.Fatalf("unexpected key shape %q", key)
	}
	if _, err := store.APIKeyByHash(ctx, key); err == nil {
		t.Fatal("the plaintext key must not be stored")
	}

	p, err := Authenticate(ctx, store, key)
	if err != nil || p != (Principal{Name: "claude-code", Role: Agent}) {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}
	for _, bad := range []string{"", "hd_nope", "sk-something", key + "x"} {
		if _, err := Authenticate(ctx, store, bad); err != ErrUnauthenticated {
			t.Errorf("key %q: want ErrUnauthenticated, got %v", bad, err)
		}
	}
	if _, err := Issue(ctx, store, " ", Viewer); err == nil {
		t.Fatal("blank name accepted")
	}
}
