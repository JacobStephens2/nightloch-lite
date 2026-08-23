package webapp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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

// invitationPageData is the invitation disclosure plus whatever the current
// viewer is authorized to act on.
type invitationPageData struct {
	CSRFToken string
	// ActionBase is the URL prefix this viewer's own controls post back to.
	// It is rebuilt server-side from however they reached the invitation, so
	// the emailed bearer link and the signed-in list share one screen.
	ActionBase string
	Version    string
	Review     partnership.InvitationReview
	// SignedIn is true whenever any User holds a session, matching or not.
	SignedIn bool
}

// invitationRoute is one viewer's path to one invitation: the module reference
// that identifies it, and the URL prefix their controls post back to.
type invitationRoute struct {
	ref        partnership.InvitationRef
	actionBase string
	// token is the emailed bearer token, empty on the signed-in path. Only a
	// bearer link can arm a return destination for a visitor who has not
	// authenticated yet.
	token string
}

// bearerRoute is the invitation as reached from the emailed link.
func bearerRoute(r *http.Request) invitationRoute {
	token := r.PathValue("token")
	return invitationRoute{
		ref:        partnership.ByToken(token),
		actionBase: "/partners/invitations/" + token,
		token:      token,
	}
}

// receivedRoute is the invitation as reached from the signed-in invitee's own
// **Partner invitations** list, with no bearer token involved.
func receivedRoute(r *http.Request) (invitationRoute, bool) {
	id, ok := pathID(r)
	if !ok {
		return invitationRoute{}, false
	}
	return invitationRoute{
		ref:        partnership.ByID(id),
		actionBase: "/partners/invitations/received/" + strconv.FormatInt(id, 10),
	}, true
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// reviewInvitation renders the approved disclosure for an emailed bearer link.
// It is a GET and mutates nothing (S3-28).
func (a *App) reviewInvitation(w http.ResponseWriter, r *http.Request) {
	a.renderInvitation(w, r, http.StatusOK, bearerRoute(r), true)
}

// reviewReceivedInvitation renders the same disclosure for a signed-in invitee
// who found the invitation in their own list instead of their inbox.
func (a *App) reviewReceivedInvitation(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.currentUser(w, r); !ok {
		return
	}
	route, ok := receivedRoute(r)
	if !ok {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	a.renderInvitation(w, r, http.StatusOK, route, false)
}

// renderInvitation shows a pending invitation's disclosure or a settled one's
// precise outcome. Only the bearer GET arms the sign-in return; a refused POST
// must not plant a return destination in the browser of a User who was never
// invited.
func (a *App) renderInvitation(w http.ResponseWriter, r *http.Request, status int, route invitationRoute, armReturn bool) {
	viewer, signedIn := a.optionalUser(r)
	review, err := a.partnerships.ReviewInvitation(r.Context(), route.ref, viewer.ID)
	if errors.Is(err, partnership.ErrInvitationUnavailable) {
		a.renderUnavailable(w, r, http.StatusNotFound)
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
	case review.State != partnership.InvitationPending, review.ViewerMatches:
		// Either the invited User is here, or there is nothing left to answer.
		// Both retire the return destination.
		a.clearInvitationReturn(w)
	case armReturn:
		// Nobody matching is signed in yet. A wrong session counts too: the
		// approved guidance is to sign out and return with the invited
		// address, and that round trip needs the destination remembered.
		a.rememberInvitationReturn(w, route.token)
	}
	page := invitationPageData{
		CSRFToken: csrf, ActionBase: route.actionBase, Version: a.version,
		Review: review, SignedIn: signedIn,
	}
	template := "invitation-review"
	if review.State != partnership.InvitationPending {
		template = "invitation-outcome"
	}
	a.render(w, r, status, template, page)
}

func (a *App) renderUnavailable(w http.ResponseWriter, r *http.Request, status int) {
	a.clearInvitationReturn(w)
	a.render(w, r, status, "invitation-unavailable", pageData{Version: a.version})
}

// acceptInvitation and declineInvitation are the invitee's two mutations from
// the emailed link; the received variants are the same two from their list.
func (a *App) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	a.answerInvitation(w, r, bearerRoute(r), a.accept)
}

func (a *App) acceptReceivedInvitation(w http.ResponseWriter, r *http.Request) {
	a.answerReceivedInvitation(w, r, a.accept)
}

func (a *App) declineInvitation(w http.ResponseWriter, r *http.Request) {
	a.answerInvitation(w, r, bearerRoute(r), a.decline)
}

func (a *App) declineReceivedInvitation(w http.ResponseWriter, r *http.Request) {
	a.answerReceivedInvitation(w, r, a.decline)
}

// invitationAnswer is the shape accept and decline share: one authenticated,
// CSRF-protected mutation on one invitation reference. Only the module's
// refusal matters here; the receipts are what the module returns to itself.
type invitationAnswer func(ctx context.Context, ref partnership.InvitationRef, viewerID int64) error

func (a *App) accept(ctx context.Context, ref partnership.InvitationRef, viewerID int64) error {
	_, err := a.partnerships.AcceptInvitation(ctx, ref, viewerID)
	return err
}

func (a *App) decline(ctx context.Context, ref partnership.InvitationRef, viewerID int64) error {
	_, err := a.partnerships.DeclineInvitation(ctx, ref, viewerID)
	return err
}

func (a *App) answerReceivedInvitation(w http.ResponseWriter, r *http.Request, answer invitationAnswer) {
	route, ok := receivedRoute(r)
	if !ok {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	a.answerInvitation(w, r, route, answer)
}

// answerInvitation performs one explicit, authenticated, CSRF-protected
// mutation. A refusal re-renders the screen the viewer is authorized to see
// rather than a bare error, and a losing race lands on the outcome that won.
func (a *App) answerInvitation(w http.ResponseWriter, r *http.Request, route invitationRoute, answer invitationAnswer) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	err := answer(r.Context(), route.ref, user.ID)
	switch {
	case err == nil:
		a.clearInvitationReturn(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case errors.Is(err, partnership.ErrInvitationUnavailable):
		a.renderInvitation(w, r, http.StatusConflict, route, false)
	case errors.Is(err, partnership.ErrInvitationNotYours):
		a.renderInvitation(w, r, http.StatusForbidden, route, false)
	default:
		a.internalError(w, r, err)
	}
}

// cancelInvitation withdraws an invitation the signed-in Protectee sent. It is
// also the only way to correct one, because the invited address and proposed
// Log sharing are immutable while the invitation is pending.
func (a *App) cancelInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	_, err := a.partnerships.CancelInvitation(r.Context(), id, user.ID)
	switch {
	case err == nil, errors.Is(err, partnership.ErrInvitationUnavailable):
		// A cancellation that lost its race, or that was never this User's to
		// make, mutates nothing and lands on the state they are authorized to
		// see. Neither answer confirms the invitation exists.
		http.Redirect(w, r, "/", http.StatusSeeOther)
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
