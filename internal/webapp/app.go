// Package webapp provides Nightloch's server-rendered HTTP control plane.
package webapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/database"
)

const (
	sessionCookieName = "__Host-nightloch_session"
	csrfCookieName    = "__Host-nightloch_csrf"
	maxFormBytes      = 64 << 10
)

type authService interface {
	RequestMagicLink(context.Context, string, string, string) error
	ConsumeMagicLink(context.Context, string) (string, githubauth.User, error)
	Authenticate(context.Context, string) (githubauth.User, error)
	SignOut(context.Context, string) error
}

// Options are the dependencies and public settings for the web app.
type Options struct {
	Auth          authService
	DB            *sql.DB
	Logger        *slog.Logger
	TurnstileSite string
	Version       string
}

// App is an http.Handler with explicit dependencies for integration testing.
type App struct {
	auth          authService
	db            *sql.DB
	logger        *slog.Logger
	turnstileSite string
	version       string
	now           func() time.Time
	random        io.Reader
	templates     map[string]*template.Template
	handler       http.Handler
}

func New(options Options) (*App, error) {
	if options.Auth == nil || options.DB == nil || options.TurnstileSite == "" {
		return nil, errors.New("webapp requires Auth, DB, and TurnstileSite")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	app := &App{
		auth:          options.Auth,
		db:            options.DB,
		logger:        options.Logger,
		turnstileSite: options.TurnstileSite,
		version:       options.Version,
		now:           time.Now,
		random:        rand.Reader,
		templates:     templates,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.home)
	mux.HandleFunc("GET /sign-in", app.signInForm)
	mux.HandleFunc("POST /sign-in", app.signInSubmit)
	mux.HandleFunc("GET /auth/check-email", app.checkEmail)
	mux.HandleFunc("GET /auth/consume", app.consumeMagicLink)
	mux.HandleFunc("POST /sign-out", app.signOut)
	mux.HandleFunc("GET /health", app.health)
	mux.HandleFunc("GET /assets/style.css", app.styles)

	app.handler = app.recoverPanics(app.securityHeaders(app.verifyCSRF(mux)))
	return app, nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

type pageData struct {
	CSRFToken     string
	Email         string
	Error         string
	TurnstileSite string
	User          githubauth.User
	Version       string
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "home", pageData{CSRFToken: csrf, User: user, Version: a.version})
}

func (a *App) signInForm(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if _, err := a.auth.Authenticate(r.Context(), cookie.Value); err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "sign-in", pageData{CSRFToken: csrf, TurnstileSite: a.turnstileSite, Version: a.version})
}

func (a *App) signInSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	err := a.auth.RequestMagicLink(r.Context(), email, clientIP(r), r.PostFormValue("cf-turnstile-response"))
	switch {
	case err == nil, errors.Is(err, githubauth.ErrRateLimited):
		http.Redirect(w, r, "/auth/check-email", http.StatusSeeOther)
	case errors.Is(err, githubauth.ErrDelivery):
		// Do not disclose whether an address exists or delivery was attempted.
		a.logger.Error("magic-link delivery failed", "error", err)
		http.Redirect(w, r, "/auth/check-email", http.StatusSeeOther)
	case errors.Is(err, githubauth.ErrHumanCheck), errors.Is(err, githubauth.ErrInvalidEmail):
		csrf, csrfErr := a.csrfToken(w, r)
		if csrfErr != nil {
			a.internalError(w, r, csrfErr)
			return
		}
		status := http.StatusUnprocessableEntity
		message := err.Error()
		if errors.Is(err, githubauth.ErrHumanCheck) {
			status = http.StatusBadRequest
			message = "Please complete the human verification and try again."
		}
		a.render(w, r, status, "sign-in", pageData{
			CSRFToken: csrf, Email: email, Error: message,
			TurnstileSite: a.turnstileSite, Version: a.version,
		})
	default:
		a.internalError(w, r, err)
	}
}

func (a *App) checkEmail(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, "check-email", pageData{Version: a.version})
}

func (a *App) consumeMagicLink(w http.ResponseWriter, r *http.Request) {
	rawSession, _, err := a.auth.ConsumeMagicLink(r.Context(), r.URL.Query().Get("token"))
	if errors.Is(err, githubauth.ErrInvalidLink) {
		a.render(w, r, http.StatusBadRequest, "invalid-link", pageData{Version: a.version})
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    rawSession,
		Path:     "/",
		Expires:  a.now().Add(30 * 24 * time.Hour),
		MaxAge:   int((30 * 24 * time.Hour) / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) signOut(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := a.auth.SignOut(r.Context(), cookie.Value); err != nil {
			a.internalError(w, r, err)
			return
		}
	}
	a.clearSessionCookie(w)
	http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
}

func (a *App) currentUser(w http.ResponseWriter, r *http.Request) (githubauth.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return githubauth.User{}, false
	}
	user, err := a.auth.Authenticate(r.Context(), cookie.Value)
	if errors.Is(err, githubauth.ErrInvalidLink) {
		a.clearSessionCookie(w)
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return githubauth.User{}, false
	}
	if err != nil {
		a.internalError(w, r, err)
		return githubauth.User{}, false
	}
	return user, true
}

func (a *App) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status := http.StatusOK
	payload := map[string]string{"status": "ok", "version": a.version}
	if err := database.Writable(ctx, a.db); err != nil {
		status = http.StatusServiceUnavailable
		payload["status"] = "unhealthy"
		a.logger.Error("database health check failed", "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *App) styles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, stylesheet)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, name string, data pageData) {
	tmpl, ok := a.templates[name]
	if !ok {
		a.internalError(w, r, fmt.Errorf("unknown template %q", name))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		a.logger.Error("render response", "template", name, "error", err)
	}
}

func (a *App) internalError(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, "Nightloch could not complete that request.", http.StatusInternalServerError)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote := net.ParseIP(host)
	// The process only binds loopback. Caddy overwrites X-Forwarded-For with
	// the actual remote host in the shipped config, so the header is trusted
	// only on a request arriving from loopback.
	if remote != nil && remote.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Forwarded-For"))); forwarded != nil {
			return forwarded.String()
		}
	}
	if remote != nil {
		return remote.String()
	}
	return "0.0.0.0"
}

// A compile-time assertion catches accidental public methods that stop App
// satisfying http.Handler.
var _ http.Handler = (*App)(nil)
