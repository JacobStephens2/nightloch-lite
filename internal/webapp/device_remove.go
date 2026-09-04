package webapp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

type deviceRemoveData struct {
	CSRFToken string
	Action    string
	Back      string
	Device    enforcement.Device
	Version   string
}

// confirmRemoveDevice renders the dedicated confirmation screen before removing a Device.
// Authorized for the Protectee owner OR any active Partner on that Protectee's Roster.
// Unauthenticated visitors are redirected to /sign-in.
// All other users receive 404/403.
func (a *App) confirmRemoveDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		http.NotFound(w, r)
		return
	}

	device, err := a.enforcement.AuthorizeRemoval(r.Context(), user.ID, id)
	if errors.Is(err, enforcement.ErrUnauthorizedRemoval) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, enforcement.ErrUnknownDevice) {
		http.NotFound(w, r)
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

	action := "/devices/" + strconv.FormatInt(device.ID, 10) + "/remove"
	a.render(w, r, http.StatusOK, "device-remove-confirm", deviceRemoveData{
		CSRFToken: csrf,
		Action:    action,
		Back:      "/",
		Device:    device,
		Version:   a.version,
	})
}

// removeDevice handles POST /devices/{id}/remove:
// Verifies CSRF token and authorization, calls enforcement.Service.RemoveDeviceByID,
// logs device_removed Activity entry and enqueues Action alert emails via
// partnerships.Service.RecordDeviceRemoved, and redirects back to dashboard with confirmation message.
func (a *App) removeDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r)
	if !valid {
		http.NotFound(w, r)
		return
	}

	removed, err := a.enforcement.RemoveDeviceByID(r.Context(), user.ID, id)
	if errors.Is(err, enforcement.ErrUnauthorizedRemoval) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, enforcement.ErrUnknownDevice) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}

	if err := a.partnerships.RecordDeviceRemoved(r.Context(), user.ID, removed.Device.ProtecteeID, removed.Device.Name); err != nil {
		a.logger.Error("record device removed activity", "error", err, "actor_id", user.ID, "device", removed.Device.Name)
	}

	http.Redirect(w, r, "/?removed="+url.QueryEscape(removed.Device.Name), http.StatusSeeOther)
}
