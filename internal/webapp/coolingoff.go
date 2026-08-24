package webapp

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// coolingOffData is the Protectee's confirmation screen for the one exit they
// hold. Request carries the exact deadline the request would set and the
// final-Partnership consequence it would eventually have, so the disclosure a
// refused submission returns to is the same one they first read (S3-43).
type coolingOffData struct {
	CSRFToken string
	Action    string
	Back      string
	Error     string
	Request   partnership.CoolingOff
	Now       time.Time
	Version   string
}

// coolingOffReceiptData is the in-app receipt the Protectee gets instead of
// mail, because they are the one who acted (S3-20). Canceled picks which of
// the two receipts this is.
type coolingOffReceiptData struct {
	Request  partnership.CoolingOff
	Canceled bool
	Now      time.Time
	Version  string
}

// confirmRequestEnd renders the consequence-specific confirmation before a
// Protectee schedules their own Partnership to end. It is a GET and mutates
// nothing. Only the Protectee on that Partnership resolves it; the Partner,
// another Partner, and a stranger all land on the same unavailable answer.
func (a *App) confirmRequestEnd(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	request, err := a.partnerships.CoolingOffPreview(r.Context(), id, user.ID)
	if errors.Is(err, partnership.ErrPartnershipUnavailable) {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.renderCoolingOffConfirmation(w, r, http.StatusOK, "cooling-off-confirm", request, "")
}

// requestEnd starts Cooling-off. A request that lost its race to an earlier
// one shows the same screen the winner produced, because that is the state
// that actually holds.
func (a *App) requestEnd(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	request, err := a.partnerships.StartCoolingOff(r.Context(), partnership.CoolingOffCommand{
		PartnershipID: id, ProtecteeID: user.ID,
	})
	switch {
	case err == nil:
		a.renderCoolingOffReceipt(w, r, http.StatusOK, request, false)
	case errors.Is(err, partnership.ErrCoolingOffRunning):
		a.renderCoolingOffLostRace(w, r, id, user.ID, false)
	case errors.Is(err, partnership.ErrPartnershipUnavailable):
		a.renderPartnershipUnavailable(w, r)
	default:
		a.internalError(w, r, err)
	}
}

// confirmCancelEnd renders the confirmation before a Protectee withdraws their
// own ending request.
func (a *App) confirmCancelEnd(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	request, err := a.partnerships.CoolingOffPreview(r.Context(), id, user.ID)
	if errors.Is(err, partnership.ErrPartnershipUnavailable) {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.renderCoolingOffConfirmation(w, r, http.StatusOK, "cooling-off-cancel-confirm", request, "")
}

// cancelEnd withdraws the ending request. A cancellation that arrives after
// the deadline completed, or after the Partner agreed to end now, finds
// nothing to cancel and says so on the screen that reports the real state.
func (a *App) cancelEnd(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	cancellation, err := a.partnerships.CancelCoolingOff(r.Context(), partnership.CoolingOffCommand{
		PartnershipID: id, ProtecteeID: user.ID,
	})
	switch {
	case err == nil:
		a.renderCoolingOffReceipt(w, r, http.StatusOK, cancellation, true)
	case errors.Is(err, partnership.ErrCoolingOffNotRunning):
		a.renderCoolingOffLostRace(w, r, id, user.ID, true)
	case errors.Is(err, partnership.ErrPartnershipUnavailable):
		a.renderPartnershipUnavailable(w, r)
	default:
		a.internalError(w, r, err)
	}
}

// renderCoolingOffLostRace answers a control that arrived second. The
// Partnership may still be live under someone else's outcome, or it may have
// ended entirely; either way the Protectee sees the state that won rather than
// a refusal about the one that did not.
func (a *App) renderCoolingOffLostRace(w http.ResponseWriter, r *http.Request, partnershipID, protecteeID int64, canceling bool) {
	request, err := a.partnerships.CoolingOffPreview(r.Context(), partnershipID, protecteeID)
	if errors.Is(err, partnership.ErrPartnershipUnavailable) {
		// The Partnership ended while this control was in flight. The
		// Protectee's own Roster is where that outcome is now visible.
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	refusal := partnership.ErrCoolingOffRunning.Error()
	template := "cooling-off-confirm"
	if canceling {
		refusal, template = partnership.ErrCoolingOffNotRunning.Error(), "cooling-off-cancel-confirm"
	}
	a.renderCoolingOffConfirmation(w, r, http.StatusConflict, template, request, refusal)
}

func (a *App) renderCoolingOffConfirmation(w http.ResponseWriter, r *http.Request, status int,
	name string, request partnership.CoolingOff, refusal string) {

	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	action := "request-end"
	if name == "cooling-off-cancel-confirm" {
		action = "cancel-end"
	}
	a.render(w, r, status, name, coolingOffData{
		CSRFToken: csrf,
		Action:    fmt.Sprintf("/partners/partnerships/%d/%s", request.PartnershipID, action),
		Back:      "/", Error: refusal, Request: request, Now: a.now().UTC(), Version: a.version,
	})
}

func (a *App) renderCoolingOffReceipt(w http.ResponseWriter, r *http.Request, status int,
	request partnership.CoolingOff, canceled bool) {

	a.render(w, r, status, "cooling-off-receipt", coolingOffReceiptData{
		Request: request, Canceled: canceled, Now: a.now().UTC(), Version: a.version,
	})
}
