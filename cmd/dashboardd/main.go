// Command dashboardd is the server dashboard: HTTP server, collectors, and the
// CLI used to bootstrap authentication.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/action"
	"github.com/olivermarcusson/dashboard/internal/api"
	"github.com/olivermarcusson/dashboard/internal/auth"
	"github.com/olivermarcusson/dashboard/internal/collect"
	collectdocker "github.com/olivermarcusson/dashboard/internal/collect/docker"
	collectgames "github.com/olivermarcusson/dashboard/internal/collect/games"
	collecthost "github.com/olivermarcusson/dashboard/internal/collect/host"
	"github.com/olivermarcusson/dashboard/internal/collect/probe"
	collectsystemd "github.com/olivermarcusson/dashboard/internal/collect/systemd"
	"github.com/olivermarcusson/dashboard/internal/config"
	"github.com/olivermarcusson/dashboard/internal/hub"
	"github.com/olivermarcusson/dashboard/internal/legacy"
	"github.com/olivermarcusson/dashboard/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "enroll":
		err = enroll()
	case "import-legacy":
		err = importLegacy(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		err = fmt.Errorf("unknown command %q", cmd)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `dashboardd — server dashboard

  dashboardd serve                    run the dashboard (default)
  dashboardd enroll                   print a one-time passkey enrollment code
  dashboardd import-legacy <db-path>  import passkeys from the old Rust dashboard

Configuration comes from the environment:
  DASHBOARD_ADDR      listen address           (127.0.0.1:13000)
  DASHBOARD_DB        sqlite file              (/var/lib/dashboard/dashboard.sqlite)
  DASHBOARD_ORIGIN    public origin            (https://dash.marcusson.dev)
  DASHBOARD_RP_ID     webauthn relying party   (host of the origin)
  DASHBOARD_USER      account name             (oliver)
`)
}

// open resolves config and brings up the database, creating its directory.
func open(ctx context.Context) (config.Config, *store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, err
	}
	if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return cfg, nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	db, err := store.Open(ctx, cfg.DBPath)
	return cfg, db, err
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, db, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	authSvc, err := auth.New(ctx, db, cfg)
	if err != nil {
		return err
	}

	h := hub.New()

	// Collectors run independently. One failing to start — Docker down, D-Bus
	// unavailable — must not stop the dashboard from serving the rest.
	var collectors []collect.Collector
	collectors = append(collectors, collecthost.New(h, db, 2*time.Second))
	collectors = append(collectors, collectsystemd.New(h, 5*time.Second))

	var runner *action.Runner
	var dockerClient *client.Client
	if dockerCollector, err := collectdocker.New(h, 5*time.Second); err != nil {
		slog.Warn("docker collector unavailable", "err", err)
		runner = action.NewRunner(db, h, nil)
	} else {
		defer dockerCollector.Close()
		collectors = append(collectors, dockerCollector)
		dockerClient = dockerCollector.Client()
		runner = action.NewRunner(db, h, dockerClient)
	}

	gameCollector := collectgames.New(h, dockerClient, 15*time.Second)
	collectors = append(collectors, gameCollector)

	// Probes ask rather than watch, so they run on long intervals.
	collectors = append(collectors,
		probe.NewStorage(h, dockerClient, 15*time.Minute),
		probe.NewEdge(h, os.Getenv("DASHBOARD_CADDY_ADMIN"), time.Hour),
		probe.NewUpdates(h, dockerClient, time.Hour),
	)

	for _, c := range collectors {
		go func(c collect.Collector) {
			slog.Info("collector started", "name", c.Name())
			if err := c.Run(ctx); err != nil {
				slog.Error("collector stopped", "name", c.Name(), "err", err)
			}
		}(c)
	}

	// Fold raw samples into coarser tiers and prune what has aged out.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := db.Rollup(ctx); err != nil {
					slog.Warn("rollup failed", "err", err)
				}
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(db, authSvc, h, runner, dockerClient, gameCollector).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Expired sessions and ceremony states are swept in the background rather
	// than on every request.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := db.SweepExpired(ctx); err != nil {
					slog.Warn("sweep failed", "err", err)
				}
			}
		}
	}()

	errc := make(chan error, 1)
	go func() {
		slog.Info("dashboard listening",
			"addr", cfg.Addr, "rp_id", cfg.RPID, "origin", cfg.RPOrigins[0], "db", cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func enroll() error {
	ctx := context.Background()
	cfg, db, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	authSvc, err := auth.New(ctx, db, cfg)
	if err != nil {
		return err
	}
	code, expires, err := authSvc.CreateEnrollmentCode(ctx)
	if err != nil {
		return err
	}
	db.Audit(ctx, "passkey.code_issued", "cli", "{}")

	fmt.Printf("\n  Enrollment code   %s\n", code)
	fmt.Printf("  Valid until       %s (%s)\n", expires.Local().Format("15:04:05"), time.Until(expires).Round(time.Minute))
	fmt.Printf("  Open              %s/enroll\n\n", cfg.RPOrigins[0])
	fmt.Print("  The code only authorizes registration. Your password manager creates and keeps the passkey.\n\n")
	return nil
}

func importLegacy(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: dashboardd import-legacy <path-to-old-dashboard.sqlite>")
	}
	ctx := context.Background()

	creds, err := legacy.Read(ctx, args[0])
	if err != nil {
		return err
	}
	if len(creds) == 0 {
		return errors.New("no active passkeys found in that database")
	}

	cfg, db, err := open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	authSvc, err := auth.New(ctx, db, cfg)
	if err != nil {
		return err
	}
	userID, err := authSvc.UserID(ctx)
	if err != nil {
		return err
	}

	// Adopt the identity the passkeys were registered against before importing
	// them. Without this the credentials arrive intact but attached to a user
	// handle no authenticator has ever seen, and every sign-in is rejected.
	handle, err := legacy.UserHandle(ctx, args[0])
	if err != nil {
		return err
	}
	if err := authSvc.SetUserHandle(ctx, handle); err != nil {
		return err
	}
	fmt.Printf("  user handle adopted from the old dashboard (%x)\n", handle)

	imported := 0
	for _, c := range creds {
		if err := authSvc.SaveCredential(ctx, userID, c.Name, &c.Cred); err != nil {
			// Already present: re-running the import to repair the handle must
			// not be an error.
			fmt.Printf("  present  %-20s (already imported)\n", c.Name)
			continue
		}
		fmt.Printf("  imported %-20s sign count %d\n", c.Name, c.Cred.Authenticator.SignCount)
		imported++
	}
	db.Audit(ctx, "passkey.imported", "cli", fmt.Sprintf(`{"count":%d}`, imported))

	fmt.Printf("\n%d of %d passkeys imported into %s\n", imported, len(creds), cfg.DBPath)
	if imported > 0 {
		fmt.Printf("Sign in at %s to confirm before decommissioning the old dashboard.\n", cfg.RPOrigins[0])
	}
	return nil
}
