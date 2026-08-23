package webapp

import (
	"errors"
	"net/http"
	"time"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// invitationReturnCookie remembers which invitation an anonymous visitor was
// reading so the magic-link round trip returns them to that review screen.
// Only the token is stored and the destination path is rebuilt server-side,
// so the cookie can never redirect anywhere but a review screen.
const invitationReturnCookie = "__Host-nightloch_invitation"

// invitationReturnTTL bounds how long a pending sign-in keeps its return
// destination. It is comfortably longer than the 15-minute magic link.
const invitationReturnTTL = time.Hour

// invitationPageData is the review disclosure plus whatever the current
// viewer is authorized to act on.
type invitationPageData struct {
	CSRFToken string
	Token     string
	Version   string
	Review    partnership.InvitationReview
	// SignedIn is true whenever any User holds a session, matching or not.
	SignedIn bool
}

// reviewInvitation renders the approved pre-acceptance disclosure. It is a
// GET over a bearer token and mutates nothing (S3-28).
func (a *App) reviewInvitation(w http.ResponseWriter, r *http.Request) {
	a.renderInvitationReview(w, r, http.StatusOK, true)
}

// renderInvitationReview shows the review screen at the given status. Only the
// GET path arms the sign-in return; a refused POST must not plant a return
// destination in the browser of a User who was never invited.
func (a *App) renderInvitationReview(w http.ResponseWriter, r *http.Request, status int, armReturn bool) {
	token := r.PathValue("token")
	viewer, signedIn := a.optionalUser(r)
	review, err := a.partnerships.ReviewInvitation(r.Context(), token, viewer.ID)
	if errors.Is(err, partnership.ErrInvitationUnavailable) {
		a.clearInvitationReturn(w)
		a.render(w, r, http.StatusNotFound, "invitation-unavailable", pageData{Version: a.version})
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	switch {
	case review.ViewerMatches:
		// The invited User is here, so the return destination has done its job.
		a.clearInvitationReturn(w)
	case armReturn:
		// Nobody matching is signed in yet. A wrong session counts too: the
		// approved guidance is to sign out and return with the invited
		// address, and that round trip needs the destination remembered.
		a.rememberInvitationReturn(w, token)
	}
	a.render(w, r, status, "invitation-review", invitationPageData{
		CSRFToken: csrf, Token: token, Version: a.version, Review: review, SignedIn: signedIn,
	})
}

// acceptInvitation performs the one explicit, authenticated, CSRF-protected
// mutation that creates a Partnership.
func (a *App) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	token := r.PathValue("token")
	_, err := a.partnerships.AcceptInvitation(r.Context(), token, user.ID)
	switch {
	case err == nil:
		a.clearInvitationReturn(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case errors.Is(err, partnership.ErrInvitationUnavailable):
		a.clearInvitationReturn(w)
		a.render(w, r, http.StatusConflict, "invitation-unavailable", pageData{Version: a.version})
	case errors.Is(err, partnership.ErrInvitationNotYours):
		// Refuse the mutation, but re-render the review screen so the wrong
		// signed-in User gets the approved mismatch guidance rather than a
		// bare error.
		a.renderInvitationReview(w, r, http.StatusForbidden, false)
	default:
		a.internalError(w, r, err)
	}
}

// optionalUser resolves the current session without forcing a redirect, so an
// anonymous reader still receives the masked review disclosure.
func (a *App) optionalUser(r *http.Request) (githubauth.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return githubauth.User{}, false
	}
	user, err := a.auth.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		return githubauth.User{}, false
	}
	return user, true
}

func (a *App) rememberInvitationReturn(w http.ResponseWriter, token string) {
	if !partnership.ValidToken(token) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: invitationReturnCookie, Value: token, Path: "/",
		Expires: a.now().Add(invitationReturnTTL), MaxAge: int(invitationReturnTTL / time.Second),
		Secure: true, HttpOnly: true,
		// The magic link arrives as a top-level GET from a mail client, so
		// Lax is the strictest mode that still carries the return.
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) clearInvitationReturn(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: invitationReturnCookie, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// invitationReturnPath is the review screen a just-authenticated User was
// reading, or "" when there is none. The path is rebuilt from a validated
// token, never taken from the request.
func (a *App) invitationReturnPath(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(invitationReturnCookie)
	if err != nil || !partnership.ValidToken(cookie.Value) {
		return ""
	}
	a.clearInvitationReturn(w)
	return "/partners/invitations/" + cookie.Value
}
