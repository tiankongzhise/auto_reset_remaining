package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"auto_reset_remaining/internal/api"
	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/mailer"
	"auto_reset_remaining/internal/service"
	"auto_reset_remaining/internal/store"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	envPath := ".env"
	if value := os.Getenv("ENV_FILE"); value != "" {
		envPath = value
	}
	configPath := "config.toml"
	if value := os.Getenv("CONFIG_FILE"); value != "" {
		configPath = value
	}

	cfg, err := config.Load(envPath, configPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		logger.Fatalf("invalid config: %v", err)
	}

	db, err := sql.Open("postgres", cfg.PostgresConnString())
	if err != nil {
		logger.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dataStore := store.NewPostgres(db)
	if err := dataStore.Init(ctx); err != nil {
		logger.Fatalf("init postgres tables: %v", err)
	}

	apiClient := api.NewClient(api.Config{
		RayPlusBaseURL:  cfg.RayPlusBaseURL,
		RayPlusAPIKey:   cfg.RayPlusAPIKey,
		RayPlusEmail:    cfg.RayPlusEmail,
		RayPlusPassword: cfg.RayPlusPassword,
		CodexBaseURL:    cfg.CodexBaseURL,
		SubscriptionID:  cfg.SubscriptionID,
		BalanceJSONPath: cfg.BalanceJSONPath,
		UserAgent:       cfg.UserAgent,
	})
	sender := mailer.NewSMTPMailer(cfg.SMTP)
	queryLogger := service.NewQueryLogger(cfg.QueryLogDir)
	logRotator := service.NewLogRotator(cfg, queryLogger, logger)
	monitor := service.NewMonitor(cfg, envPath, apiClient, sender, dataStore, queryLogger, logger)
	if err := monitor.Initialize(ctx); err != nil {
		logger.Fatalf("initialize monitor: %v", err)
	}

	go monitor.Run(ctx)
	if logRotator.Enabled() {
		go logRotator.RunDaily(ctx)
		logger.Print("log rotation enabled at 02:00 local time")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           service.NewHTTPHandler(monitor, logRotator),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Printf("HTTP server listening on %s", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("http shutdown: %v", err)
	}
	logger.Print("service stopped")
}
