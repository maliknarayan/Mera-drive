// Command server runs the SangamDrive API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/config"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/logging"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/server"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store/sqlite"
)

// Stamped at build time:
//
//	go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD)"
var (
	version = "dev"
	commit  = "none"
	built   = "unknown"
)

// shutdownTimeout bounds how long in-flight requests may finish. Uploads and
// downloads can be long, so this is generous.
const shutdownTimeout = 30 * time.Second

// sessionPruneInterval controls how often expired sessions are swept.
const sessionPruneInterval = time.Hour

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sangamdrive: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.LogLevel, cfg.Env.IsProduction())
	log.Info("starting sangamdrive api",
		slog.String("version", version),
		slog.String("commit", commit),
		slog.String("env", string(cfg.Env)),
	)

	box, err := cryptobox.New(cfg.EncryptionKey, cfg.SessionSecret)
	if err != nil {
		return fmt.Errorf("initialise crypto: %w", err)
	}

	st, err := sqlite.Open(cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing store", slog.String("error", err.Error()))
		}
	}()

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMigrate()
	if err := st.Migrate(migrateCtx); err != nil {
		return err
	}
	log.Info("schema up to date", slog.String("path", cfg.SQLitePath))

	authService := auth.NewService(st, box, auth.CookieOptions{
		Domain: cfg.CookieDomain,
		Secure: cfg.CookieSecure,
	}, cfg.SessionTTL)

	googleAuth := google.NewAuthenticator(
		cfg.GoogleClientID,
		cfg.GoogleClientSecret,
		cfg.GoogleRedirectURL(),
	)

	srv := server.New(server.Deps{
		Config: cfg,
		Logger: log,
		Store:  st,
		Crypto: box,
		Auth:   authService,
		Google: googleAuth,
		Build:  server.BuildInfo{Version: version, Commit: commit, Built: built},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go pruneSessions(ctx, log, st)

	serverErr := make(chan error, 1)
	go func() {
		addr := fmt.Sprintf(":%d", cfg.APIPort)
		log.Info("listening", slog.String("addr", addr))
		serverErr <- srv.App().Listen(addr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Info("shutdown signal received", slog.Duration("grace", shutdownTimeout))
		if err := srv.App().ShutdownWithTimeout(shutdownTimeout); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		log.Info("stopped cleanly")
		return nil
	}
}

// pruneSessions periodically deletes expired session rows.
func pruneSessions(ctx context.Context, log *slog.Logger, st *sqlite.Store) {
	ticker := time.NewTicker(sessionPruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			n, err := st.DeleteExpiredSessions(pruneCtx, time.Now())
			cancel()

			switch {
			case err != nil && !errors.Is(err, context.Canceled):
				log.Warn("pruning sessions failed", slog.String("error", err.Error()))
			case n > 0:
				log.Info("pruned expired sessions", slog.Int64("count", n))
			}
		}
	}
}
