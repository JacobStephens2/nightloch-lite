// Command nightloch runs the Nightloch Lite control plane.
package main

import (
	"context"
	"database/sql"
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
	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
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
	case "mint-device":
		if len(args) != 3 {
			return errors.New("usage: nightloch mint-device PROTECTEE-EMAIL DEVICE-NAME")
		}
		return runMintDevice(args[1], args[2])
	case "devices":
		if len(args) != 1 {
			return errors.New("usage: nightloch devices")
		}
		return runDevices()
	case "serve":
		if len(args) > 1 {
			return errors.New("usage: nightloch serve")
		}
		return runServe()
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}
}

const usage = `usage: nightloch serve
       nightloch migrate
       nightloch backup DESTINATION
       nightloch mint-device PROTECTEE-EMAIL DEVICE-NAME
       nightloch devices
       nightloch version`

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

// openEnforcement builds the enforcement module for an Operator command. The
// schema is checked first for the same reason serve checks it: a binary that
// mints against an older schema than it was built for would be allocating
// addresses under rules the database does not hold.
func openEnforcement() (*enforcement.Service, *sql.DB, error) {
	path, err := config.DatabasePath()
	if err != nil {
		return nil, nil, err
	}
	tunnel, err := config.LoadTunnel()
	if err != nil {
		return nil, nil, err
	}
	db, err := database.Open(path)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.CheckCurrent(ctx, db); err != nil {
		db.Close()
		return nil, nil, err
	}
	service, err := enforcement.NewService(db, &enforcement.HelperTunnel{}, enforcement.TunnelSettings{
		EndpointHost: tunnel.EndpointHost,
		EndpointPort: tunnel.EndpointPort,
		PublicKey:    tunnel.PublicKey,
	})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return service, db, nil
}

// runMintDevice enrolls one Device for one Protectee. Minting is an Operator
// command in this slice and deliberately not a product action: the Enrollment
// allowlist that opens enrollment to Users is Slice 5.
//
// The configuration goes to stdout on its own so it can be piped into a file
// or a QR encoder; everything an Operator reads goes to stderr. The private
// key inside it is generated here and never stored, so this output is the only
// copy that will ever exist.
func runMintDevice(protecteeEmail, deviceName string) error {
	service, db, err := openEnforcement()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	minted, err := service.MintDevice(ctx, enforcement.MintCommand{
		ProtecteeEmail: protecteeEmail, DeviceName: deviceName,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "minted Device %q for %s at %s\n",
		minted.Device.Name, protecteeEmail, minted.Device.Address)
	fmt.Fprintln(os.Stderr, "the private key below is not stored by the server and is shown once")
	fmt.Print(minted.Configuration)
	return nil
}

// runDevices lists every Device with what the tunnel says about it. A tunnel
// this command cannot read is an error rather than an empty list: "no Devices
// are connected" and "nothing is known" are different answers.
func runDevices() error {
	service, db, err := openEnforcement()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devices, err := service.Devices(ctx)
	if err != nil {
		return err
	}
	for _, status := range devices {
		handshake := "never"
		if !status.LatestHandshake.IsZero() {
			handshake = status.LatestHandshake.Format(time.RFC3339)
		}
		membership := "on tunnel"
		if !status.OnTunnel {
			membership = "MISSING FROM TUNNEL"
		}
		fmt.Printf("%s\t%s\t%s\tlast handshake %s\n",
			status.Device.Address, status.Device.Name, membership, handshake)
	}
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
