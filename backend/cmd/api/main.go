// Command cloud-notes-api is the Cloud Notes HTTP API.
//
// Usage: cloud-notes-api [serve|migrate|create-user|healthcheck|version] [flags]
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/database"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/handlers"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/observability"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/server"
)

// Injected at build time via -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "unknown"
)

// Process exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const sessionCleanupInterval = 15 * time.Minute

const usage = `Usage: cloud-notes-api <command> [flags]

Commands:
  serve        run the API (default)
  migrate      apply pending database migrations and exit
  create-user  create a user: --email E [--display-name N] [--if-not-exists] [--password-stdin]
               (password from NEW_USER_PASSWORD / NEW_USER_PASSWORD_FILE, or stdin)
  healthcheck  probe /readyz of the local server: [--url URL]
  version      print version information
`

func main() {
	// Local development convenience only: a missing .env file is fine.
	_ = godotenv.Load()
	slog.SetDefault(observability.NewLogger(os.Stdout, os.Getenv("LOG_LEVEL")))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := dispatch(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func dispatch(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		if len(args) > 0 {
			fmt.Fprint(stderr, usage)
			return exitUsage
		}
		return report(stderr, serve(ctx))
	case "migrate":
		if len(args) > 0 {
			fmt.Fprint(stderr, usage)
			return exitUsage
		}
		return report(stderr, migrateCmd(ctx))
	case "create-user":
		return createUser(ctx, args, os.Getenv, os.ReadFile, stdin, stdout, stderr, openCreator)
	case "healthcheck":
		return healthcheck(ctx, args, os.Getenv, stderr)
	case "version":
		fmt.Fprintf(stdout, "cloud-notes-api %s (commit %s, %s)\n", version, commit, runtime.Version())
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
}

func report(stderr io.Writer, err error) int {
	if err == nil {
		return exitOK
	}
	slog.Error("fatal", "error", err)
	// Config problems are multi-line; also print them plainly for humans.
	fmt.Fprintln(stderr, "error:", err)
	return exitError
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load(os.Getenv, os.ReadFile)
	if err != nil {
		return config.Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	slog.SetDefault(observability.NewLogger(os.Stdout, cfg.LogLevel))
	return cfg, nil
}

func migrateCmd(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := database.Migrate(ctx, cfg); err != nil {
		return err
	}
	slog.Info("migrations applied")
	return nil
}

func openCreator(ctx context.Context) (userCreator, func(), error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, nil, err
	}
	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	svc, err := auth.NewService(database.NewUserRepository(pool), database.NewSessionRepository(pool),
		auth.NewHasher(), auth.OptionsFromConfig(cfg), nil)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return svc, pool.Close, nil
}

func serve(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	logger := slog.Default()

	if cfg.DBMigrateOnStart {
		if err := database.Migrate(ctx, cfg); err != nil {
			return err
		}
	}
	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	metrics := observability.NewMetrics(version, commit)
	metrics.RegisterPool(pool.Stat)

	svc, err := auth.NewService(database.NewUserRepository(pool), database.NewSessionRepository(pool),
		auth.NewHasher(), auth.OptionsFromConfig(cfg), nil)
	if err != nil {
		return err
	}

	ready := &server.Readiness{}
	router, err := handlers.NewRouter(handlers.Deps{
		Config:   cfg,
		Version:  version,
		DB:       pool,
		Notes:    database.NewNoteRepository(pool),
		Auth:     svc,
		Recorder: metrics,
		Metrics:  metrics,
		Ready:    ready.Ready,
		Logger:   logger,
	})
	if err != nil {
		return err
	}

	logger.Info("starting", "version", version, "commit", commit, "http_addr", cfg.HTTPAddr,
		"metrics_addr", cfg.MetricsAddr, "signup_mode", cfg.SignupMode, "public_sharing", cfg.PublicSharing)
	return server.Run(ctx, server.Options{
		Addr:            cfg.HTTPAddr,
		MetricsAddr:     cfg.MetricsAddr,
		Handler:         router,
		MetricsHandler:  metrics.Handler(),
		Readiness:       ready,
		ShutdownDelay:   cfg.ShutdownDelay,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Jobs:            []func(context.Context){func(c context.Context) { svc.RunCleaner(c, sessionCleanupInterval) }},
		Logger:          logger,
	})
}
