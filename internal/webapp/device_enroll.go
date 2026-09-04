package webapp

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

// enrollTrap is one setting that turns the blackhole from an inconvenience
// into something a Protectee cannot get out of unaided. Each one is stored as
// the setting's own name, what it does, and how to undo it, because a warning
// that does not name the screen the setting lives on is not recovery.
type enrollTrap struct {
	// Setting is the platform's own label for the control, so a Protectee can
	// search for the words they are reading.
	Setting     string
	Consequence string
	Recovery    string
}

// enrollPlatform is one supported platform's recovery guidance. The screen
// renders the table rather than restating it, so a platform cannot reach the
// page without steps and at least one named trap (asserted in
// device_enroll_test.go).
type enrollPlatform struct {
	Name     string
	Recovery []string
	Traps    []enrollTrap
}

// enrollPlatforms is every platform Nightloch supports for a Device in v1:
// Android and macOS, which is what the dogfood deployment runs and what the
// full-tunnel configuration is tested against.
var enrollPlatforms = []enrollPlatform{
	{
		Name: "Android",
		Recovery: []string{
			"Open the WireGuard app and turn the Nightloch tunnel off. The internet comes back as soon as the tunnel stops.",
			"If the tunnel will not stay off, the recovery is in Android's own settings rather than in the WireGuard app: Settings, then Network & internet, then VPN, then the gear beside WireGuard.",
		},
		Traps: []enrollTrap{
			{
				Setting:     "Always-on VPN",
				Consequence: "Android brings the tunnel straight back up every time you turn it off in the WireGuard app, so a Device that cannot reach Nightloch looks like a phone with no internet and no way out of it.",
				Recovery:    "Turn Always-on VPN off under Settings, Network & internet, VPN, and the gear beside WireGuard. Turning the tunnel off inside the WireGuard app will not hold while this is on.",
			},
			{
				Setting:     "Block connections without VPN",
				Consequence: "Android drops every packet that is not going through the tunnel, so turning the tunnel off leaves you with no internet at all rather than with unfiltered internet.",
				Recovery:    "Turn Block connections without VPN off on that same VPN settings screen, then turn Always-on VPN off, then turn the tunnel off.",
			},
			{
				Setting:     "Private DNS set to a hostname",
				Consequence: "Encrypted DNS on port 853 will not be carried out of the tunnel, and Android's strict Private DNS mode has no plaintext fallback, so names stop resolving and the phone reads as offline even though the tunnel is up.",
				Recovery:    "Set Private DNS to Off or Automatic under Settings, Network & internet, Private DNS.",
			},
		},
	},
	{
		Name: "macOS",
		Recovery: []string{
			"Open the WireGuard app, select the Nightloch tunnel, and click Deactivate. The WireGuard icon in the menu bar does the same thing.",
		},
		Traps: []enrollTrap{
			{
				Setting:     "On-Demand",
				Consequence: "macOS reconnects the tunnel within seconds of every Deactivate, so while Nightloch is unreachable the tunnel looks impossible to turn off.",
				Recovery:    "Turn On-Demand off first: in the WireGuard app select the Nightloch tunnel, click Edit, clear the On-Demand setting, save, and then click Deactivate.",
			},
		},
	},
}

// deviceEnrollData is the enroll screen.
type deviceEnrollData struct {
	User        githubauth.User
	Version     string
	Platforms   []enrollPlatform
	Allowlisted bool
	CSRFToken   string
	DeviceName  string
	Error       string
}

// deviceEnrolledData is the one-time view rendered immediately after enrollment.
// The private key inside Configuration is dropped from memory after this response.
type deviceEnrolledData struct {
	User          githubauth.User
	Version       string
	Device        enforcement.Device
	Configuration string
	QRDataURI     template.URL
	ConfDataURI   template.URL
	DownloadName  string
}

// deviceEnroll is what a Protectee reads before they ever enroll a Device. It
// requires a session, so the recovery detail is not published to anonymous
// visitors. Allowlisted users see the enrollment form; non-allowlisted users
// see the beta invite-only notice.
func (a *App) deviceEnroll(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	allowed := a.isEnrollmentAllowed(user.Email)
	var csrfToken string
	if allowed {
		csrf, err := a.csrfToken(w, r)
		if err != nil {
			a.internalError(w, r, err)
			return
		}
		csrfToken = csrf
	}
	a.render(w, r, http.StatusOK, "device-enroll", deviceEnrollData{
		User:        user,
		Version:     a.version,
		Platforms:   enrollPlatforms,
		Allowlisted: allowed,
		CSRFToken:   csrfToken,
	})
}

// deviceEnrollSubmit handles POST /devices/enroll: verifies authentication and CSRF,
// enforces the beta allowlist (returning 403 Forbidden to unauthorized users),
// validates the Device name, enrolls the Device, appends the Activity entry,
// and renders the ephemeral one-time configuration view.
func (a *App) deviceEnrollSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := a.currentUser(w, r)
	if !ok {
		return
	}
	if !a.isEnrollmentAllowed(user.Email) {
		http.Error(w, "Device enrollment is invite-only during beta. Your account is not currently allowlisted to enroll Devices.", http.StatusForbidden)
		return
	}
	csrfToken, err := a.csrfToken(w, r)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	deviceName := strings.TrimSpace(r.Form.Get("device_name"))
	renderError := func(errMsg string) {
		a.render(w, r, http.StatusUnprocessableEntity, "device-enroll", deviceEnrollData{
			User:        user,
			Version:     a.version,
			Platforms:   enrollPlatforms,
			Allowlisted: true,
			CSRFToken:   csrfToken,
			DeviceName:  deviceName,
			Error:       errMsg,
		})
	}

	if deviceName == "" {
		renderError("Device name is required.")
		return
	}
	minted, err := a.enforcement.EnrollDevice(r.Context(), user.ID, deviceName)
	if err != nil {
		switch {
		case errors.Is(err, enforcement.ErrDeviceQuotaExceeded):
			renderError("You have reached the limit of 5 live Devices. Remove an existing Device before enrolling a new one.")
			return
		case errors.Is(err, enforcement.ErrDeviceExists):
			renderError("You already have an active Device with this name.")
			return
		case errors.Is(err, enforcement.ErrInvalidDeviceName):
			renderError("Device name must be 1 to 64 printable characters.")
			return
		default:
			a.internalError(w, r, fmt.Errorf("enroll device: %w", err))
			return
		}
	}

	// Transactionally append Activity entry and alert partners
	if err := a.partnerships.RecordDeviceEnrolled(r.Context(), user.ID, minted.Device.Name); err != nil {
		a.logger.Error("record device enrolled activity", "error", err, "user_id", user.ID, "device", minted.Device.Name)
	}

	qrURI, err := qrCodeSVGDataURI(minted.Configuration)
	if err != nil {
		a.internalError(w, r, fmt.Errorf("generate qr: %w", err))
		return
	}
	confURI := confDataURI(minted.Configuration)
	downloadName := fmt.Sprintf("%s.conf", minted.Device.Name)

	a.render(w, r, http.StatusOK, "device-enrolled", deviceEnrolledData{
		User:          user,
		Version:       a.version,
		Device:        minted.Device,
		Configuration: minted.Configuration,
		QRDataURI:     template.URL(qrURI),
		ConfDataURI:   template.URL(confURI),
		DownloadName:  downloadName,
	})
}
