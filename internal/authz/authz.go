// Package authz maps API keys to principals and principals to the tools they may use.
package authz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
)

type Role string

const (
	Viewer Role = "viewer" // reads the public knowledge base
	Agent  Role = "agent"  // also works tickets
	Admin  Role = "admin"  // also reads admin-only articles, closes tickets and reads the audit log
)

// Permission is a capability a tool requires.
type Permission string

const (
	ReadArticles      Permission = "articles:read"
	ReadAdminArticles Permission = "articles:read_admin"
	ReadTickets       Permission = "tickets:read"
	WriteTickets      Permission = "tickets:write"
	CloseTickets      Permission = "tickets:close"
	ReadAudit         Permission = "audit:read"
)

var grants = map[Role][]Permission{
	Viewer: {ReadArticles},
	Agent:  {ReadArticles, ReadTickets, WriteTickets},
	Admin:  {ReadArticles, ReadAdminArticles, ReadTickets, WriteTickets, CloseTickets, ReadAudit},
}

func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if _, ok := grants[r]; !ok {
		return "", fmt.Errorf("unknown role %q (want viewer, agent or admin)", s)
	}
	return r, nil
}

func (r Role) Can(p Permission) bool {
	for _, g := range grants[r] {
		if g == p {
			return true
		}
	}
	return false
}

// Principal is the authenticated caller.
type Principal struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}

const keyPrefix = "hd_"

// NewKey returns a random API key. Only its hash is stored; the key itself is shown once.
func NewKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return keyPrefix + hex.EncodeToString(b), nil
}

func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

var ErrUnauthenticated = errors.New("invalid or unknown API key")

// Authenticate resolves an API key to a principal. Keys are compared by hash, so a database leak does not
// expose usable keys.
func Authenticate(ctx context.Context, store helpdesk.Store, key string) (Principal, error) {
	if !strings.HasPrefix(key, keyPrefix) {
		return Principal{}, ErrUnauthenticated
	}
	k, err := store.APIKeyByHash(ctx, HashKey(key))
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	role, err := ParseRole(k.Role)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{Name: k.Name, Role: role}, nil
}

// Issue creates and stores a key for a named principal and returns the plaintext key.
func Issue(ctx context.Context, store helpdesk.Store, name string, role Role) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("name is required")
	}
	key, err := NewKey()
	if err != nil {
		return "", err
	}
	if err := store.CreateAPIKey(ctx, helpdesk.APIKey{Name: name, Role: string(role), KeyHash: HashKey(key)}); err != nil {
		return "", err
	}
	return key, nil
}
