package webapp

import (
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

// invitationConfirmData is one consequence-specific confirmation: the party it
// affects, where the confirmed mutation posts, and where declining to go
// through with it returns (S3-46).
type invitationConfirmData struct {
	CSRFToken         string
	Action            string
	Back              string
	CounterpartyEmail string
	// ExpiresAt is the invitation's own deadline, stated by the confirmations
	// whose consequence depends on it. A resend confirmation names it because
	// the deadline not moving is the consequence.
	ExpiresAt time.Time
	Version   string
}

// invitationReceiptData is the in-app receipt an initiator gets for a mutation
// that leaves nothing behind on their dashboard (S3-20).
type invitationReceiptData struct {
	CounterpartyEmail string
	At                time.Time
	Version           string
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
		actionBase: "/partners/received-invitations/" + strconv.FormatInt(id, 10),
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
// the emailed link; the received variants are the same two from their own
// **Partner invitations** list.
func (a *App) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	a.acceptVia(w, r, bearerRoute(r))
}

func (a *App) acceptReceivedInvitation(w http.ResponseWriter, r *http.Request) {
	if route, ok := a.receivedOr404(w, r); ok {
		a.acceptVia(w, r, route)
	}
}

func (a *App) declineInvitation(w http.ResponseWriter, r *http.Request) {
	a.declineVia(w, r, bearerRoute(r))
}

func (a *App) declineReceivedInvitation(w http.ResponseWriter, r *http.Request) {
	if route, ok := a.receivedOr404(w, r); ok {
		a.declineVia(w, r, route)
	}
}

// confirmDecline and confirmReceivedDecline render the invitee's one
// consequence-specific confirmation before they decline (S3-46). Both are
// GETs, so neither mutates anything (S3-28).
func (a *App) confirmDecline(w http.ResponseWriter, r *http.Request) {
	a.confirmDeclineVia(w, r, bearerRoute(r))
}

func (a *App) confirmReceivedDecline(w http.ResponseWriter, r *http.Request) {
	if route, ok := a.receivedOr404(w, r); ok {
		a.confirmDeclineVia(w, r, route)
	}
}

func (a *App) receivedOr404(w http.ResponseWriter, r *http.Request) (invitationRoute, bool) {
	route, ok := receivedRoute(r)
	if !ok {
		a.renderUnavailable(w, r, http.StatusNotFound)
	}
	return route, ok
}

func (a *App) acceptVia(w http.ResponseWriter, r *http.Request, route invitationRoute) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	if _, err := a.partnerships.AcceptInvitation(r.Context(), route.ref, user.ID); err != nil {
		a.refuseAnswer(w, r, route, err)
		return
	}
	// The dashboard is the acceptance receipt: it now names the Partner and
	// says the Partner lock is active.
	a.clearInvitationReturn(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) declineVia(w http.ResponseWriter, r *http.Request, route invitationRoute) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	receipt, err := a.partnerships.DeclineInvitation(r.Context(), route.ref, user.ID)
	if err != nil {
		a.refuseAnswer(w, r, route, err)
		return
	}
	// Nothing about a declined invitation survives on the dashboard, so the
	// decliner gets their own in-app receipt (S3-20).
	a.clearInvitationReturn(w)
	a.render(w, r, http.StatusOK, "invitation-declined-receipt", invitationReceiptData{
		CounterpartyEmail: receipt.ProtecteeEmail, At: receipt.DeclinedAt, Version: a.version,
	})
}

func (a *App) confirmDeclineVia(w http.ResponseWriter, r *http.Request, route invitationRoute) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	review, err := a.partnerships.ReviewInvitation(r.Context(), route.ref, user.ID)
	if errors.Is(err, partnership.ErrInvitationUnavailable) {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	if !review.MayDecline() {
		// A settled invitation or a wrong session has nothing to confirm; the
		// review screen already carries the right answer for both.
		a.renderInvitation(w, r, http.StatusOK, route, false)
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "invitation-decline-confirm", invitationConfirmData{
		CSRFToken: csrf, Action: route.actionBase + "/decline", Back: route.actionBase,
		CounterpartyEmail: review.ProtecteeEmail, Version: a.version,
	})
}

// refuseAnswer renders the screen a refused mutation's actor is authorized to
// see: the outcome that won the race, or the approved mismatch guidance for a
// wrong session.
func (a *App) refuseAnswer(w http.ResponseWriter, r *http.Request, route invitationRoute, err error) {
	switch {
	case errors.Is(err, partnership.ErrInvitationUnavailable):
		a.renderInvitation(w, r, http.StatusConflict, route, false)
	case errors.Is(err, partnership.ErrInvitationNotYours):
		a.renderInvitation(w, r, http.StatusForbidden, route, false)
	default:
		a.internalError(w, r, err)
	}
}

// confirmCancelInvitation renders the Protectee's one consequence-specific
// confirmation before they withdraw an invitation (S3-46). The invitation is
// read through their own authorized view, so this route discloses nothing a
// non-Protectee could not already see on their dashboard.
func (a *App) confirmCancelInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	view, err := a.partnerships.ProtecteeView(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	pending, found := pendingInvitation(view, id)
	if !found {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	action := "/partners/sent-invitations/" + strconv.FormatInt(id, 10) + "/cancel"
	a.render(w, r, http.StatusOK, "invitation-cancel-confirm", invitationConfirmData{
		CSRFToken: csrf, Action: action, Back: "/",
		CounterpartyEmail: pending.InviteeEmail, Version: a.version,
	})
}

func pendingInvitation(view partnership.ProtecteeView, id int64) (partnership.PendingInvitation, bool) {
	for _, pending := range view.PendingInvitations {
		if pending.ID == id {
			return pending, true
		}
	}
	return partnership.PendingInvitation{}, false
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
	receipt, err := a.partnerships.CancelInvitation(r.Context(), id, user.ID)
	switch {
	case err == nil:
		a.render(w, r, http.StatusOK, "invitation-canceled-receipt", invitationReceiptData{
			CounterpartyEmail: receipt.InviteeEmail, At: receipt.CanceledAt, Version: a.version,
		})
	case errors.Is(err, partnership.ErrInvitationUnavailable):
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

// confirmResendInvitation renders the Protectee's one consequence-specific
// confirmation before they resend (S3-46). The consequences worth stating are
// the ones a reminder makes easy to assume away: the deadline does not move,
// the earlier link stops working, and current Partners are not emailed again.
// It is a GET and mutates nothing (S3-28).
func (a *App) confirmResendInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	view, err := a.partnerships.ProtecteeView(r.Context(), user.ID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	pending, found := pendingInvitation(view, id)
	if !found {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, http.StatusOK, "invitation-resend-confirm", invitationConfirmData{
		CSRFToken: csrf, Back: "/",
		Action:            "/partners/sent-invitations/" + strconv.FormatInt(id, 10) + "/resend",
		CounterpartyEmail: pending.InviteeEmail, ExpiresAt: pending.ExpiresAt, Version: a.version,
	})
}

// resendInvitation puts a pending invitation in front of its invitee again.
// It is one of the Protectee's two pending-invitation controls, and the only
// one that changes nothing about the invitation: the invited address, the
// proposed Log sharing, and the seven-day expiry stay as sent, so correcting
// any of them is still cancel-and-reinvite.
//
// A refused resend re-renders the dashboard with its reason, because the
// reason is always a limit the Protectee can wait out and the row it applies
// to is on that page.
func (a *App) resendInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderUnavailable(w, r, http.StatusNotFound)
		return
	}
	_, err := a.partnerships.ResendInvitation(r.Context(), id, user.ID)
	switch {
	case err == nil:
		// The dashboard is the receipt: the row now names the resend and
		// repeats the expiry it did not move.
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case partnership.IsRefusal(err):
		a.renderDashboard(w, r, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, partnership.ErrInvitationUnavailable):
		// A settled invitation, or one that was never this User's to resend.
		// Neither answer confirms the invitation exists.
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		a.internalError(w, r, err)
	}
}
