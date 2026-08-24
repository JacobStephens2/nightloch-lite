// Command nightloch runs the Nightloch Lite control plane.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/config"
	"github.com/JacobStephens2/nightloch-lite/internal/database"
	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
	"github.com/JacobStephens2/nightloch-lite/internal/webapp"
)

// version is overridden at link time in CI and releases.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nightloch:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	switch command {
	case "version", "-version", "--version":
		fmt.Println(version)
		return nil
	case "migrate":
		if len(args) != 1 {
			return errors.New("usage: nightloch migrate")
		}
		return runMigrate()
	case "backup":
		if len(args) != 2 {
			return errors.New("usage: nightloch backup DESTINATION")
		}
		return runBackup(args[1])
	case "serve":
		if len(args) > 1 {
			return errors.New("usage: nightloch serve")
		}
		return runServe()
	case "help", "-h", "--help":
		fmt.Println("usage: nightloch [serve|migrate|backup DESTINATION|version]")
		return nil
	default:
		return fmt.Errorf("unknown command %q (use serve, migrate, or version)", command)
	}
}

func runBackup(destination string) error {
	path, err := config.DatabasePath()
	if err != nil {
		return err
	}
	db, err := database.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := database.Snapshot(ctx, db, destination); err != nil {
		return err
	}
	fmt.Println("database snapshot created at", destination)
	return nil
}

func runMigrate() error {
	path, err := config.DatabasePath()
	if err != nil {
		return err
	}
	db, err := database.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.Migrate(ctx, db); err != nil {
		return err
	}
	fmt.Println("database migrations applied")
	return nil
}

func runServe() error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Open(settings.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := database.CheckCurrent(ctx, db); err != nil {
		cancel()
		return err
	}
	cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	externalHTTP := &http.Client{Timeout: 10 * time.Second}
	store := auth.NewStore(db)
	authService := auth.NewService(
		store,
		&mailmessage.ResendSender{APIKey: settings.ResendAPIKey, From: settings.ResendFrom, Client: externalHTTP},
		&auth.TurnstileVerifier{
			Secret: settings.TurnstileSecret, Hostname: settings.BaseURL.Hostname(),
			Action: "sign-in", Client: externalHTTP,
		},
		settings.BaseURL,
	)
	partnerships := partnership.NewService(db, settings.BaseURL)
	app, err := webapp.New(webapp.Options{
		Auth: authService, DB: db, Logger: logger, Partnerships: partnerships,
		TurnstileSite: settings.TurnstileSite, Version: version,
		DeliveryWebhookSecret: settings.ResendWebhookSecret,
	})
	if err != nil {
		return err
	}

	// Partnership mail is committed durably and transmitted afterwards, so the
	// dispatcher both drains new work and resumes whatever a previous process
	// left queued.
	dispatchCtx, stopDispatch := context.WithCancel(context.Background())
	defer stopDispatch()
	go (&partnership.Dispatcher{
		Service: partnerships,
		Sender: &mailmessage.ResendSender{
			APIKey: settings.ResendAPIKey, From: settings.ResendFrom, Client: externalHTTP,
		},
		Logger: logger,
	}).Run(dispatchCtx)

	server := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("Nightloch listening", "address", settings.HTTPAddr, "base_url", settings.BaseURL.String(), "version", version)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case received := <-stop:
		logger.Info("shutting down", "signal", received.String())
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Keep the standard log package configured without timestamps in the rare
// event a dependency writes through http.Server's legacy logger path.
func init() {
	log.SetFlags(0)
}
