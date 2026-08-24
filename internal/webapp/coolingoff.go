package webapp

import (
	"context"
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

// coolingOffControl is one of the two mutations a Protectee holds over their
// own Partnership. It names the route segment, the confirmation screen, and
// the refusal that means this control arrived second, so the two handlers
// below differ only by which one they are given.
type coolingOffControl struct {
	path     string
	template string
	lostRace error
}

var (
	requestEndControl = coolingOffControl{
		path: "request-end", template: "cooling-off-confirm",
		lostRace: partnership.ErrCoolingOffRunning,
	}
	cancelEndControl = coolingOffControl{
		path: "cancel-end", template: "cooling-off-cancel-confirm",
		lostRace: partnership.ErrCoolingOffNotRunning,
	}
)

// confirmRequestEnd and confirmCancelEnd render the consequence-specific
// confirmation before a Protectee schedules — or unschedules — their own
// Partnership's ending. Both are GETs and mutate nothing (S3-28). Only the
// Protectee on that Partnership resolves either; the Partner, another Partner,
// and a stranger all land on the same unavailable answer.
func (a *App) confirmRequestEnd(w http.ResponseWriter, r *http.Request) {
	a.confirmCoolingOff(w, r, requestEndControl)
}

// confirmCancelEnd is the same read behind the cancelling control.
func (a *App) confirmCancelEnd(w http.ResponseWriter, r *http.Request) {
	a.confirmCoolingOff(w, r, cancelEndControl)
}

func (a *App) confirmCoolingOff(w http.ResponseWriter, r *http.Request, control coolingOffControl) {
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
	a.renderCoolingOffConfirmation(w, r, http.StatusOK, control, request, "")
}

// requestEnd starts Cooling-off, and cancelEnd withdraws it. Either may arrive
// second — behind an earlier submission of the same control, behind the
// Partner's End now, or behind the deadline itself — and then it commits
// nothing and shows the state that actually won.
func (a *App) requestEnd(w http.ResponseWriter, r *http.Request) {
	a.commitCoolingOff(w, r, requestEndControl, a.partnerships.StartCoolingOff)
}

// cancelEnd withdraws the Protectee's own ending request.
func (a *App) cancelEnd(w http.ResponseWriter, r *http.Request) {
	a.commitCoolingOff(w, r, cancelEndControl, a.partnerships.CancelCoolingOff)
}

func (a *App) commitCoolingOff(w http.ResponseWriter, r *http.Request, control coolingOffControl,
	mutate func(context.Context, partnership.CoolingOffCommand) (partnership.CoolingOff, error)) {

	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	result, err := mutate(r.Context(), partnership.CoolingOffCommand{
		PartnershipID: id, ProtecteeID: user.ID,
	})
	switch {
	case err == nil:
		a.renderCoolingOffReceipt(w, r, result, control == cancelEndControl)
	case errors.Is(err, control.lostRace):
		a.renderCoolingOffLostRace(w, r, control, id, user.ID)
	case errors.Is(err, partnership.ErrPartnershipUnavailable):
		a.renderPartnershipUnavailable(w, r)
	default:
		a.internalError(w, r, err)
	}
}

// renderCoolingOffLostRace answers a control that arrived second. It re-reads
// the Partnership and renders the same confirmation screen over the state that
// won, which states what is actually scheduled and — because that state is not
// one this control can act on — offers no submit button to repeat.
func (a *App) renderCoolingOffLostRace(w http.ResponseWriter, r *http.Request,
	control coolingOffControl, partnershipID, protecteeID int64) {

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
	a.renderCoolingOffConfirmation(w, r, http.StatusConflict, control, request, control.lostRace.Error())
}

func (a *App) renderCoolingOffConfirmation(w http.ResponseWriter, r *http.Request, status int,
	control coolingOffControl, request partnership.CoolingOff, refusal string) {

	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, status, control.template, coolingOffData{
		CSRFToken: csrf,
		Action:    fmt.Sprintf("/partners/partnerships/%d/%s", request.PartnershipID, control.path),
		Back:      "/", Error: refusal, Request: request, Now: a.now().UTC(), Version: a.version,
	})
}

func (a *App) renderCoolingOffReceipt(w http.ResponseWriter, r *http.Request,
	request partnership.CoolingOff, canceled bool) {

	a.render(w, r, http.StatusOK, "cooling-off-receipt", coolingOffReceiptData{
		Request: request, Canceled: canceled, Now: a.now().UTC(), Version: a.version,
	})
}
