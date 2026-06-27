package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"pricewatcher/internal/config"
	"pricewatcher/internal/db"
	"pricewatcher/internal/runner"
	"pricewatcher/internal/web"
)

func main() {
	// 1. Setup in-memory ring buffer for Web logs
	logBuffer := web.NewLogRingBuffer(500)

	// 2. Configure slog to output JSON to stdout and the ring buffer
	multiWriter := io.MultiWriter(os.Stdout, logBuffer)
	jsonHandler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(jsonHandler)
	slog.SetDefault(logger)

	slog.Info("Starting PriceWatcher rewrite in Go...")

	// 3. Load configurations
	cfg := config.LoadConfig()

	// 4. Initialize Database
	database, err := db.NewDB(cfg.DBPath)
	if err != nil {
		slog.Error("Database initialization failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	slog.Info("SQLite database initialized", "path", cfg.DBPath)

	// 5. Legacy alert migration
	if cfg.LegacyAlertFile != "" {
		err := database.MigrateLegacyAlerts(cfg.LegacyAlertFile)
		if err != nil {
			if strings.HasPrefix(err.Error(), "MIGRATED_OK:") {
				countStr := strings.TrimPrefix(err.Error(), "MIGRATED_OK:")
				slog.Info("migrated " + countStr + " alert entries from legacy JSON file")
			} else {
				slog.Error("Legacy alert migration failed", "error", err)
			}
		}
	}

	// 6. Setup Cron Scheduler
	scheduler := cron.New(cron.WithParser(cron.NewParser(
		cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)))

	_, err = scheduler.AddFunc(cfg.CronSchedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		_ = runner.RunOnce(ctx, database, cfg)
	})
	if err != nil {
		slog.Error("Failed to add cron schedule", "schedule", cfg.CronSchedule, "error", err)
		os.Exit(1)
	}

	scheduler.Start()
	defer scheduler.Stop()

	// Log next scheduled time
	if entries := scheduler.Entries(); len(entries) > 0 {
		nextTime := entries[0].Next.Format(time.RFC3339)
		slog.Info("cron scheduler started", "schedule", cfg.CronSchedule, "next_run", nextTime)
	}

	// 7. Setup Web Server
	srv := web.NewServer(database, cfg, scheduler, logBuffer)
	httpServer := &http.Server{
		Addr:    cfg.WebPort,
		Handler: srv.Routes(),
	}

	// Channel to catch interrupt/shutdown signals
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Web UI server listening", "port", cfg.WebPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Web server error", "error", err)
			os.Exit(1)
		}
	}()

	// Block until signal is received
	sig := <-shutdownChan
	slog.Info("Shutting down gracefully...", "signal", sig.String())

	// Shutdown HTTP Server
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("PriceWatcher stopped successfully.")
}
