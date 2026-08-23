package webapp

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// partnershipEndData is the Partner's typed-confirmation screen: the exact
// consequences of ending, and a rejected attempt's own refusal so they can
// correct it without losing the disclosure (S3-46).
type partnershipEndData struct {
	CSRFToken string
	Action    string
	Back      string
	Error     string
	Ending    partnership.PartnershipEnding
	Version   string
}

// partnershipReceiptData renders a former Partner's read-only receipt. The
// same screen answers the mutation that just happened and a later visit, so
// JustEnded only changes the framing, never what the receipt discloses.
type partnershipReceiptData struct {
	Receipt   partnership.EndedPartnership
	JustEnded bool
	Version   string
}

// confirmEndPartnership renders the consequence-specific confirmation before a
// Partner ends their own Partnership. It is a GET and mutates nothing. Only
// the Partner on that Partnership resolves it; the Protectee, another Partner,
// and a stranger all land on the same unavailable answer.
func (a *App) confirmEndPartnership(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	ending, err := a.partnerships.EndablePartnership(r.Context(), id, user.ID)
	if errors.Is(err, partnership.ErrPartnershipUnavailable) {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.renderEndConfirmation(w, r, http.StatusOK, ending, "")
}

// endPartnership commits the immediate ending. A missing or misspelled
// confirmation returns to the same screen with the disclosure intact; an
// ending that lost its race shows the receipt the former Partner is authorized
// to keep, which is also the state that actually won.
func (a *App) endPartnership(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	_, err := a.partnerships.EndPartnership(r.Context(), partnership.EndCommand{
		PartnershipID: id, PartnerID: user.ID, Confirmation: r.PostFormValue("confirmation"),
	})
	switch {
	case err == nil:
		a.renderReceipt(w, r, http.StatusOK, id, user.ID, true)
	case errors.Is(err, partnership.ErrEndConfirmationRequired):
		preview, previewErr := a.partnerships.EndablePartnership(r.Context(), id, user.ID)
		if errors.Is(previewErr, partnership.ErrPartnershipUnavailable) {
			a.renderPartnershipUnavailable(w, r)
			return
		}
		if previewErr != nil {
			a.internalError(w, r, previewErr)
			return
		}
		a.renderEndConfirmation(w, r, http.StatusUnprocessableEntity, preview, err.Error())
	case errors.Is(err, partnership.ErrPartnershipUnavailable):
		a.renderReceipt(w, r, http.StatusConflict, id, user.ID, true)
	default:
		a.internalError(w, r, err)
	}
}

// partnershipReceipt is a former Partner's read-only receipt for their own
// ended Partnership. It is the only thing they retain (S3-16).
func (a *App) partnershipReceipt(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	a.renderReceipt(w, r, http.StatusOK, id, user.ID, false)
}

func (a *App) renderEndConfirmation(w http.ResponseWriter, r *http.Request, status int, ending partnership.PartnershipEnding, refusal string) {
	csrf, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	action := "/partners/partnerships/" + strconv.FormatInt(ending.PartnershipID, 10) + "/end"
	a.render(w, r, status, "partnership-end-confirm", partnershipEndData{
		CSRFToken: csrf, Action: action, Back: "/", Error: refusal,
		Ending: ending, Version: a.version,
	})
}

// renderReceipt shows the receipt or, when this viewer holds none, the same
// unavailable answer every other unauthorized reference receives.
func (a *App) renderReceipt(w http.ResponseWriter, r *http.Request, status int, partnershipID, partnerID int64, justEnded bool) {
	receipt, err := a.partnerships.EndedPartnershipReceipt(r.Context(), partnershipID, partnerID)
	if errors.Is(err, partnership.ErrPartnershipUnavailable) {
		a.renderPartnershipUnavailable(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	a.render(w, r, status, "partnership-receipt", partnershipReceiptData{
		Receipt: receipt, JustEnded: justEnded, Version: a.version,
	})
}

func (a *App) renderPartnershipUnavailable(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusNotFound, "partnership-unavailable", pageData{Version: a.version})
}
