package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/duynhlab/pkg/authmw"
	"github.com/duynhlab/pkg/grpcx"
	"github.com/duynhlab/pkg/httpmw"
	"github.com/duynhlab/pkg/logger/slogx"
	"github.com/duynhlab/pkg/migratex"
	"github.com/duynhlab/pkg/obsx"
	reviewv1 "github.com/duynhlab/pkg/proto/review/v1"
	"github.com/duynhlab/review-service/config"
	migrations "github.com/duynhlab/review-service/db/migrations"
	seed "github.com/duynhlab/review-service/db/seed"
	database "github.com/duynhlab/review-service/internal/core"
	"github.com/duynhlab/review-service/internal/core/repository"
	grpcv1 "github.com/duynhlab/review-service/internal/grpc/v1"
	logicv1 "github.com/duynhlab/review-service/internal/logic/v1"
	v1 "github.com/duynhlab/review-service/internal/web/v1"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()

	logger := slogx.New(slogx.Config{Level: os.Getenv("LOG_LEVEL")})
	slogx.SetDefault(logger)

	// Subcommands (`migrate`, `seed`) run an embedded SQL set and exit; no args
	// serves the app.
	if len(os.Args) > 1 && runSubcommand(os.Args[1], cfg, logger) {
		return
	}

	if err := cfg.Validate(); err != nil {
		panic("Configuration validation failed: " + err.Error())
	}

	logger.Info(ctx, "Service starting",
		slog.String("service.version", cfg.Service.Version),
		slog.String("deployment.environment.name", cfg.Service.Env),
		slog.String("port", cfg.Service.Port),
	)

	// RFC-0014: single OTel wiring point — traces per TRACING_ENABLED, OTLP
	// metrics (the only pipeline since the P3 cutover; OTEL_METRICS_ENABLED
	// defaults on, =false is a kill switch), logs behind OTEL_LOGS_ENABLED.
	// The config is built once so the tracer scope name and the startup log
	// reflect the values obsx actually uses.
	otelCfg := obsx.ConfigFromEnv()
	var tp interface{ Shutdown(context.Context) error }
	obs, err := obsx.SetupObservability(context.Background(), otelCfg)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize OpenTelemetry", slogx.Err(err))
	} else {
		tp = obs
		// The facade reaches OTLP through the global logger provider obsx
		// installed; rebuilding it only wires Flush, so a Fatal record is
		// exported before the process exits.
		logger = slogx.New(slogx.Config{Level: os.Getenv("LOG_LEVEL"), Flush: obs.ForceFlush})
		slogx.SetDefault(logger)
		logger.Info(ctx, "OpenTelemetry initialized",
			slog.Bool("traces", obs.Enabled().Traces),
			slog.Bool("otlp_metrics", obs.Enabled().Metrics),
			slog.Bool("otlp_logs", obs.Enabled().Logs),
			slog.String("endpoint", otelCfg.Endpoint),
			slog.Float64("sample_rate", otelCfg.SampleRate),
		)
	}

	shutdownProfiling := initProfiling(cfg, logger)
	defer func() {
		if shutdownProfiling != nil {
			if err := shutdownProfiling(context.Background()); err != nil {
				logger.Error(ctx, "Profiling shutdown error", slogx.Err(err))
			}
		}
	}()

	pool, err := database.Connect(context.Background(), cfg)
	if err != nil {
		logger.Error(ctx, "Failed to connect to database", slogx.Err(err))
		return
	}
	defer pool.Close()
	logger.Info(ctx, "Database connection pool established")

	var isShuttingDown atomic.Bool

	// Dependency Injection
	repo := repository.NewReviewRepository(pool)
	service := logicv1.NewReviewService(repo)
	handler := v1.NewReviewHandler(service)

	// Local JWT verification against the Keycloak realm JWKS — the only auth
	// path (no gRPC fallback), so a verifier failure is fatal.
	verifier, err := authmw.NewVerifier(authmw.Config{
		Issuer:   cfg.OIDCIssuer,
		Audience: cfg.OIDCAudience,
		JWKSURL:  cfg.OIDCJWKSURL,
	})
	if err != nil {
		logger.Fatal(ctx, "Failed to initialize JWT verifier", slogx.Err(err))
	}

	// Internal gRPC server (east-west). HTTP :8080 is unaffected.
	grpcSrv := startGRPC(cfg, logger, service)

	srv := setupServer(cfg, obsx.ConfigFromEnv().ServiceName, logger, verifier, &isShuttingDown, handler)
	runGracefulShutdown(cfg, srv, grpcSrv, tp, pool, logger, &isShuttingDown)
}

// runSubcommand handles the `migrate` and `seed` subcommands. It returns true
// when a subcommand was recognised and executed (the caller then exits), or
// false to fall through to serving the app.
//
// `migrate` applies the versioned schema migrations and runs in every
// environment (init container, direct DB host). `seed` applies DEV-ONLY demo
// data and is invoked explicitly — never by `migrate` or the serve path — so
// production databases are never seeded.
func runSubcommand(cmd string, cfg *config.Config, logger *slogx.Logger) bool {
	ctx := context.Background()
	switch cmd {
	case "migrate":
		if err := migratex.Run(migrations.FS, "sql", cfg.Database.BuildDSN()); err != nil {
			logger.Fatal(ctx, "Schema migration failed", slogx.Err(err))
		}
		logger.Info(ctx, "Schema migrations applied")
		return true
	case "seed":
		// Demo data is DEV-ONLY; refuse to seed a production database.
		if cfg.IsProduction() {
			logger.Fatal(ctx, "seed refused in production — demo data is dev-only")
		}
		if err := applySeed(cfg); err != nil {
			logger.Fatal(ctx, "Demo seed failed", slogx.Err(err))
		}
		logger.Info(ctx, "Demo seed data applied")
		return true
	default:
		return false
	}
}

// applySeed executes the embedded dev-only seed SQL directly against the database.
// It does NOT use golang-migrate: seeds are idempotent (ON CONFLICT) and must not
// share the schema_migrations version table with the schema migrations. Simple
// query protocol lets each multi-statement seed file run in one Exec.
func applySeed(cfg *config.Config) error {
	ctx := context.Background()

	poolCfg, err := pgxpool.ParseConfig(cfg.Database.BuildDSN())
	if err != nil {
		return fmt.Errorf("parse seed DSN: %w", err)
	}
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("connect for seed: %w", err)
	}
	defer pool.Close()

	entries, err := fs.ReadDir(seed.FS, "sql")
	if err != nil {
		return fmt.Errorf("read seed dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		b, readErr := fs.ReadFile(seed.FS, "sql/"+name)
		if readErr != nil {
			return fmt.Errorf("read seed %s: %w", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(b)); execErr != nil {
			return fmt.Errorf("apply seed %s: %w", name, execErr)
		}
	}
	return nil
}

// startGRPC starts the internal gRPC server on cfg.GRPC.Port, serving
// ReviewService alongside the HTTP listener (dual-port). gRPC is the official
// east-west transport, so it always runs; it returns nil only if the listener
// can't bind. The server uses the shared grpcx bootstrap (OpenTelemetry, health,
// reflection).
func startGRPC(cfg *config.Config, logger *slogx.Logger, svc *logicv1.ReviewService) *grpc.Server {
	ctx := context.Background()
	lc := net.ListenConfig{}
	lis, err := lc.Listen(context.Background(), "tcp", ":"+cfg.GRPC.Port)
	if err != nil {
		logger.Error(ctx, "Failed to listen for gRPC", slog.String("port", cfg.GRPC.Port), slogx.Err(err))
		return nil
	}

	grpcSrv, _ := grpcx.NewServer(logger.Slog())
	reviewv1.RegisterReviewServiceServer(grpcSrv, grpcv1.NewServer(svc, logger))

	go func() {
		logger.Info(ctx, "Starting gRPC server", slog.String("port", cfg.GRPC.Port))
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Error(ctx, "gRPC server error", slogx.Err(err))
		}
	}()

	return grpcSrv
}

func initProfiling(cfg *config.Config, logger *slogx.Logger) func(context.Context) error {
	ctx := context.Background()
	if !cfg.Profiling.Enabled {
		logger.Info(ctx, "Profiling disabled (PROFILING_ENABLED=false)")
		return nil
	}
	stopProfiling, err := obsx.SetupProfiling()
	if err != nil {
		logger.Warn(ctx, "Failed to initialize profiling", slogx.Err(err))
		return nil
	}
	logger.Info(ctx, "Profiling initialized", slog.String("endpoint", cfg.Profiling.Endpoint))
	return stopProfiling
}

func setupServer(cfg *config.Config, otelServiceName string, logger *slogx.Logger, verifier *authmw.Verifier, isShuttingDown *atomic.Bool, handler *v1.ReviewHandler) *http.Server {
	// gin.New, not gin.Default: Default installs gin's own logger and
	// recovery, which print the raw path and client address past the facade.
	r := gin.New()

	r.Use(httpmw.Tracing(otelServiceName))
	r.Use(httpmw.Logging(logger.Slog()))
	r.Use(httpmw.Recovery(logger.Slog()))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/ready", func(c *gin.Context) {
		if isShuttingDown.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "shutting_down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Review v1 routes — Variant A edge naming (see api-naming-convention.md)
	r.GET("/review/v1/public/reviews", handler.ListReviews)

	// Private routes require local JWT validation (JWKS).
	privateReviews := r.Group("/review/v1/private")
	privateReviews.Use(authmw.MiddlewareJWT(verifier))
	{
		privateReviews.POST("/reviews", handler.CreateReview)
	}

	return &http.Server{
		Addr:              ":" + cfg.Service.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func runGracefulShutdown(
	cfg *config.Config,
	srv *http.Server,
	grpcSrv *grpc.Server,
	tp interface{ Shutdown(context.Context) error },
	pool interface{ Close() },
	logger *slogx.Logger,
	isShuttingDown *atomic.Bool,
) {
	ctx := context.Background()
	go func() {
		logger.Info(ctx, "Starting review service", slog.String("port", cfg.Service.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(ctx, "Failed to start server", slogx.Err(err))
		}
	}()

	logger.ProcessStarted(ctx, slogx.ComponentAPI)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-sigCtx.Done()
	logger.Info(ctx, "Shutdown signal received")

	isShuttingDown.Store(true)
	drainDelay := cfg.GetReadinessDrainDelayDuration()
	if drainDelay > 0 {
		logger.Info(ctx, "Readiness drain delay started", slog.Duration("delay", drainDelay))
		time.Sleep(drainDelay)
	}

	shutdownTimeout := cfg.GetShutdownTimeoutDuration()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	logger.Info(ctx, "Shutting down server...", slog.Duration("timeout", shutdownTimeout))

	outcome := slogx.OutcomeGraceful
	if err := srv.Shutdown(shutdownCtx); err != nil {
		outcome = slogx.OutcomeError
		logger.Error(ctx, "HTTP server shutdown error", slogx.Err(err))
	} else {
		logger.Info(ctx, "HTTP server shutdown complete")
	}

	if grpcSrv != nil {
		grpcSrv.GracefulStop()
		logger.Info(ctx, "gRPC server shutdown complete")
	}

	pool.Close()
	logger.Info(ctx, "Database pool closed")

	// process.stopped goes out BEFORE the OTel SDK shuts down: a record
	// emitted after it is dropped rather than exported.
	logger.ProcessStopped(ctx, slogx.ComponentAPI, outcome)

	// Shutdown the OTel SDK — flushes pending spans plus any OTLP
	// metrics/logs providers built behind the RFC-0014 flags.
	if tp != nil {
		if err := tp.Shutdown(shutdownCtx); err != nil {
			logger.Error(ctx, "OpenTelemetry shutdown error", slogx.Err(err))
		} else {
			logger.Info(ctx, "OpenTelemetry shutdown complete")
		}
	}

	logger.Info(ctx, "Graceful shutdown complete")
}
