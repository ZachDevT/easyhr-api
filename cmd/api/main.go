package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/easyhr/easyhr-be/internal/auth"
	"github.com/easyhr/easyhr-be/internal/config"
	"github.com/easyhr/easyhr-be/internal/database"
	"github.com/easyhr/easyhr-be/internal/documents"
	"github.com/easyhr/easyhr-be/internal/httpapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	authentication, err := auth.NewClerkMiddleware(auth.ClerkConfig{
		SecretKey:         cfg.Clerk.SecretKey,
		Issuer:            cfg.Clerk.Issuer,
		AuthorizedParties: cfg.Clerk.AuthorizedParties,
		RequiredAudiences: cfg.Clerk.RequiredAudiences,
	})
	if err != nil {
		slog.Error("invalid authentication configuration", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	documentStorage, err := documents.NewS3Storage(ctx, documents.S3Config{
		Bucket: cfg.Documents.Bucket, Region: cfg.Documents.Region, Endpoint: cfg.Documents.Endpoint,
		AccessKeyID: cfg.Documents.AccessKeyID, SecretAccessKey: cfg.Documents.SecretAccessKey, UsePathStyle: cfg.Documents.UsePathStyle,
	})
	if err != nil {
		slog.Error("invalid document storage configuration", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpapi.NewWithDocumentStorage(pool, cfg.AllowedOrigins, authentication, cfg.Clerk.SecretKey, documentStorage).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("EasyHR API listening", "address", cfg.HTTPAddress, "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()

	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}
