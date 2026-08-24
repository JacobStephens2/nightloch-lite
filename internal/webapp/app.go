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
	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
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
	Auth         authService
	DB           *sql.DB
	Logger       *slog.Logger
	Partnerships *partnership.Service
	// DeliveryWebhookSecret authenticates the provider's delivery callbacks.
	// Without it Nightloch could only guess at Partner reachability from its
	// own send attempts, so it is required rather than optional.
	DeliveryWebhookSecret string
	TurnstileSite         string
	Version               string
}

// App is an http.Handler with explicit dependencies for integration testing.
type App struct {
	auth             authService
	db               *sql.DB
	logger           *slog.Logger
	partnerships     *partnership.Service
	deliveryVerifier mailmessage.WebhookVerifier
	turnstileSite    string
	version          string
	now              func() time.Time
	random           io.Reader
	templates        map[string]*template.Template
	handler          http.Handler
}

func New(options Options) (*App, error) {
	if options.Auth == nil || options.DB == nil || options.Partnerships == nil ||
		options.TurnstileSite == "" || options.DeliveryWebhookSecret == "" {
		return nil, errors.New("webapp requires Auth, DB, Partnerships, TurnstileSite, and DeliveryWebhookSecret")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	app := &App{
		auth:             options.Auth,
		db:               options.DB,
		logger:           options.Logger,
		partnerships:     options.Partnerships,
		deliveryVerifier: mailmessage.WebhookVerifier{Secret: options.DeliveryWebhookSecret},
		turnstileSite:    options.TurnstileSite,
		version:          options.Version,
		now:              time.Now,
		random:           rand.Reader,
		templates:        templates,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", app.home)
	mux.HandleFunc("GET /sign-in", app.signInForm)
	mux.HandleFunc("POST /sign-in", app.signInSubmit)
	mux.HandleFunc("GET /auth/check-email", app.checkEmail)
	mux.HandleFunc("GET /auth/consume", app.consumeMagicLink)
	mux.HandleFunc("POST /sign-out", app.signOut)
	mux.HandleFunc("GET /partners/invite", app.invitePartnerForm)
	mux.HandleFunc("POST /partners/invite", app.invitePartnerSubmit)
	// An invitee reaches the same invitation two ways: the emailed bearer
	// link, and — once signed in — their own Partner invitations list, which
	// needs no token at all.
	mux.HandleFunc("GET /partners/invitations/{token}", app.reviewInvitation)
	mux.HandleFunc("POST /partners/invitations/{token}/accept", app.acceptInvitation)
	mux.HandleFunc("GET /partners/invitations/{token}/decline", app.confirmDecline)
	mux.HandleFunc("POST /partners/invitations/{token}/decline", app.declineInvitation)
	mux.HandleFunc("GET /partners/received-invitations/{id}", app.reviewReceivedInvitation)
	mux.HandleFunc("POST /partners/received-invitations/{id}/accept", app.acceptReceivedInvitation)
	mux.HandleFunc("GET /partners/received-invitations/{id}/decline", app.confirmReceivedDecline)
	mux.HandleFunc("POST /partners/received-invitations/{id}/decline", app.declineReceivedInvitation)
	// A Partner ends their own Partnership immediately; the receipt they keep
	// afterwards is the only thing that survives for them (S3-16). The same
	// route is their **End now** once the Protectee has requested Cooling-off.
	mux.HandleFunc("GET /partners/partnerships/{id}/end", app.confirmEndPartnership)
	mux.HandleFunc("POST /partners/partnerships/{id}/end", app.endPartnership)
	// The Protectee's own exit is the 72-hour Cooling-off, which only they may
	// start and only they may cancel.
	mux.HandleFunc("GET /partners/partnerships/{id}/request-end", app.confirmRequestEnd)
	mux.HandleFunc("POST /partners/partnerships/{id}/request-end", app.requestEnd)
	mux.HandleFunc("GET /partners/partnerships/{id}/cancel-end", app.confirmCancelEnd)
	mux.HandleFunc("POST /partners/partnerships/{id}/cancel-end", app.cancelEnd)
	mux.HandleFunc("GET /partners/ended-partnerships/{id}", app.partnershipReceipt)
	mux.HandleFunc("GET /partners/sent-invitations/{id}/resend", app.confirmResendInvitation)
	mux.HandleFunc("POST /partners/sent-invitations/{id}/resend", app.resendInvitation)
	mux.HandleFunc("GET /partners/sent-invitations/{id}/cancel", app.confirmCancelInvitation)
	mux.HandleFunc("POST /partners/sent-invitations/{id}/cancel", app.cancelInvitation)
	// The provider's delivery callbacks are the only unauthenticated POST.
	// They carry no session and no CSRF token; the signature over the body is
	// what authorizes them, so they bypass the CSRF check by path (see
	// verifyCSRF) rather than by having a token to present.
	mux.HandleFunc("POST "+deliveryWebhookPath, app.deliveryWebhook)
	mux.HandleFunc("GET /health", app.health)
	mux.HandleFunc("GET /assets/style.css", app.styles)
	mux.HandleFunc("GET /assets/local-time.js", app.localTimeScript)

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

// dashboardData is the signed-in User's own protection state alongside the
// Protectees they protect. The two areas stay separate so a Reciprocal
// Partnership reads correctly in both (S3-45).
type dashboardData struct {
	CSRFToken string
	User      githubauth.User
	Version   string
	View      partnership.ProtecteeView
	// Received is every live invitation addressed to this User's verified
	// email, so an invitee who lost the email can still answer it.
	Received   []partnership.ReceivedInvitation
	Protecting []partnership.ProtecteeEntry
	// Ended is the read-only receipts this User keeps for Partnerships they
	// no longer hold. They carry no Roster and no later Activity.
	Ended []partnership.EndedPartnership
	// Now anchors every Cooling-off countdown on the page to one instant, so
	// two rows of the same Roster cannot disagree about how long is left, and
	// it is what decides whether a pending invitation is due to be resent.
	Now time.Time
	// Refusal is the reason a dashboard control was declined, shown above the
	// state it failed to change. It is always a partnership.IsRefusal message,
	// which is safe for this viewer to read.
	Refusal string
}

// invitePageData carries the Add a Partner form, including a rejected
// submission's own address so the Protectee can correct it.
type invitePageData struct {
	CSRFToken    string
	Error        string
	InviteeEmail string
	Version      string
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	a.renderDashboard(w, r, http.StatusOK, "")
}

// renderDashboard builds the signed-in User's whole dashboard. A control that
// was refused renders the same page with its reason, so the Protectee reads
// the refusal beside the unchanged state it applies to rather than on a screen
// of its own.
func (a *App) renderDashboard(w http.ResponseWriter, r *http.Request, status int, refusal string) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	view, err := a.partnerships.ProtecteeView(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	protecting, err := a.partnerships.ProtectingView(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	received, err := a.partnerships.ReceivedInvitations(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	ended, err := a.partnerships.EndedPartnerships(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, status, "dashboard", dashboardData{
		CSRFToken: csrf, User: user, Version: a.version, View: view,
		Received: received, Protecting: protecting, Ended: ended,
		Now: a.now().UTC(), Refusal: refusal,
	})
}

func (a *App) invitePartnerForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.currentUser(w, r); !ok {
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "invite", invitePageData{CSRFToken: csrf, Version: a.version})
}

func (a *App) invitePartnerSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	inviteeEmail := r.PostFormValue("invitee_email")
	_, err := a.partnerships.InvitePartner(r.Context(), partnership.InviteCommand{
		ProtecteeID:  user.ID,
		InviteeEmail: inviteeEmail,
		// Log sharing defaults off: only an explicitly checked box turns it on.
		LogSharing: r.PostFormValue("log_sharing") == "on",
	})
	if err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !partnership.IsRefusal(err) {
		a.internalError(w, r, err)
		return
	}
	csrf, csrfErr := a.csrfToken(w, r)
	if csrfErr != nil {
		a.internalError(w, r, csrfErr)
		return
	}
	a.render(w, r, http.StatusUnprocessableEntity, "invite", invitePageData{
		CSRFToken: csrf, Error: err.Error(), InviteeEmail: inviteeEmail, Version: a.version,
	})
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
	// An invitee who signed in to respond returns to that review screen
	// rather than to a dashboard that says nothing about the invitation.
	destination := "/"
	if returnPath := a.invitationReturnPath(w, r); returnPath != "" {
		destination = returnPath
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
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

func (a *App) localTimeScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, localTimeScript)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
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
