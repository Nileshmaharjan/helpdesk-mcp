// Command helpdesk-mcp serves the help centre to AI agents over MCP.
//
//	helpdesk-mcp serve -transport http -addr :8080              # Postgres from DATABASE_URL
//	helpdesk-mcp serve -transport stdio                         # key from HELPDESK_API_KEY
//	helpdesk-mcp serve -transport http -dev-keys                # in-memory demo; prints one key per role
//	helpdesk-mcp keys create -name claude-code -role agent      # mint a key (shown once)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Nileshmaharjan/helpdesk-mcp/internal/authz"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/helpdesk"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/mcpserver"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/memstore"
	"github.com/Nileshmaharjan/helpdesk-mcp/internal/postgres"
)

func main() {
	// stdout belongs to the MCP protocol on stdio, so all logs go to stderr.
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, log, os.Args[2:])
	case "keys":
		err = keys(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: helpdesk-mcp serve [flags] | helpdesk-mcp keys create -name NAME -role viewer|agent|admin")
	os.Exit(2)
}

func openStore(ctx context.Context, dbURL string, seed bool) (helpdesk.Store, func(), error) {
	if dbURL == "" {
		m := memstore.New()
		return m, func() {}, m.Seed(ctx)
	}
	pg, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return nil, nil, err
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	if seed {
		if err := pg.Seed(ctx); err != nil {
			pg.Close()
			return nil, nil, fmt.Errorf("seed: %w", err)
		}
	}
	return pg, pg.Close, nil
}

func serve(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	transport := fs.String("transport", "stdio", "stdio or http")
	addr := fs.String("addr", ":8080", "listen address for -transport http")
	dbURL := fs.String("db", os.Getenv("DATABASE_URL"), "Postgres URL; empty runs an in-memory demo store")
	seed := fs.Bool("seed", true, "load demo articles and tickets into an empty database")
	rps := fs.Float64("rate", 5, "tool calls per second allowed per caller (0 disables the limit)")
	burst := fs.Int("burst", 10, "rate-limit burst per caller")
	devKeys := fs.Bool("dev-keys", false, "in-memory mode only: mint and print one key per role")
	devRole := fs.String("dev-role", "", "in-memory stdio only: act as this role without a key")
	fs.Parse(args)

	store, closeStore, err := openStore(ctx, *dbURL, *seed)
	if err != nil {
		return err
	}
	defer closeStore()
	opts := mcpserver.Options{RatePerSec: *rps, Burst: *burst, Logger: log}

	if (*devKeys || *devRole != "") && *dbURL != "" {
		return errors.New("-dev-keys and -dev-role only work with the in-memory store")
	}
	if *devKeys {
		for _, r := range []authz.Role{authz.Viewer, authz.Agent, authz.Admin} {
			key, err := authz.Issue(ctx, store, "dev-"+string(r), r)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "dev key for %-6s %s\n", r, key)
		}
	}

	switch *transport {
	case "stdio":
		p, err := stdioPrincipal(ctx, store, *devRole)
		if err != nil {
			return err
		}
		log.Info("serving MCP on stdio", "principal", p.Name, "role", p.Role)
		return mcpserver.NewForPrincipal(store, p, opts).Run(ctx, &mcp.StdioTransport{})
	case "http":
		srv := &http.Server{Addr: *addr, Handler: mcpserver.HTTPHandler(store, opts), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(shutdown)
		}()
		log.Info("serving MCP over HTTP", "addr", *addr, "endpoint", "/mcp")
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown transport %q", *transport)
	}
}

func stdioPrincipal(ctx context.Context, store helpdesk.Store, devRole string) (authz.Principal, error) {
	if devRole != "" {
		role, err := authz.ParseRole(devRole)
		return authz.Principal{Name: "dev-" + devRole, Role: role}, err
	}
	key := os.Getenv("HELPDESK_API_KEY")
	if key == "" {
		return authz.Principal{}, errors.New("set HELPDESK_API_KEY (create one with: helpdesk-mcp keys create)")
	}
	return authz.Authenticate(ctx, store, key)
}

func keys(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		usage()
	}
	fs := flag.NewFlagSet("keys create", flag.ExitOnError)
	name := fs.String("name", "", "who the key is for, e.g. claude-code")
	roleName := fs.String("role", "agent", "viewer, agent or admin")
	dbURL := fs.String("db", os.Getenv("DATABASE_URL"), "Postgres URL")
	fs.Parse(args[1:])
	if *dbURL == "" {
		return errors.New("keys are stored in Postgres: set DATABASE_URL or -db")
	}
	role, err := authz.ParseRole(*roleName)
	if err != nil {
		return err
	}
	store, closeStore, err := openStore(ctx, *dbURL, false)
	if err != nil {
		return err
	}
	defer closeStore()
	key, err := authz.Issue(ctx, store, *name, role)
	if err != nil {
		return err
	}
	fmt.Println(key)
	fmt.Fprintf(os.Stderr, "Created %s key for %q. It is shown only once.\n", role, *name)
	return nil
}
