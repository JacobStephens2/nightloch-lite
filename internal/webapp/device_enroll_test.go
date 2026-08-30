package webapp

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const enrollPath = "https://lite.nightloch.app/devices/enroll"

// The screen's recovery detail says exactly which setting takes a phone off
// the internet and how to undo it. That is written for a Protectee who is
// about to enrol, not for anyone who finds the address, so an anonymous
// visitor is sent to sign in and reads none of it.
func TestDeviceEnrollScreenRequiresASession(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)

	anonymous := get(t, env, enrollPath)
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/sign-in" {
		t.Fatalf("anonymous GET /devices/enroll = %d location=%q", anonymous.Code, anonymous.Header().Get("Location"))
	}
	if body := anonymous.Body.String(); strings.Contains(body, "Always-on VPN") || strings.Contains(body, "blackhole") {
		t.Fatalf("anonymous response disclosed recovery detail:\n%s", body)
	}
}

// The blackhole is the one thing the product must not let a Protectee meet
// unprepared: the Device stops reaching the internet rather than falling open,
// and the screen says so before anyone enrols.
func TestDeviceEnrollScreenStatesTheBlackholeAndItsRecovery(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	recorder := get(t, env, enrollPath, session, csrf)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /devices/enroll = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"stops reaching the internet",
		"deliberate",
		"not a bug",
		"Nightloch does not fall open",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing %q:\n%s", want, body)
		}
	}
	// Recovery is the other half of stating the failure: a screen that names
	// the blackhole without saying how to get out of it is worse than silent.
	for _, platform := range enrollPlatforms {
		if !strings.Contains(body, platform.Name) {
			t.Fatalf("enroll screen missing platform %q:\n%s", platform.Name, body)
		}
		for _, step := range platform.Recovery {
			if !strings.Contains(body, html.EscapeString(step)) {
				t.Fatalf("enroll screen missing %s recovery step %q:\n%s", platform.Name, step, body)
			}
		}
		for _, trap := range platform.Traps {
			for _, sentence := range []string{trap.Setting, trap.Consequence, trap.Recovery} {
				if !strings.Contains(body, html.EscapeString(sentence)) {
					t.Fatalf("enroll screen missing %s trap copy %q:\n%s", platform.Name, sentence, body)
				}
			}
		}
	}
}

// Every supported platform carries its own recovery steps and at least one
// named trap. A platform added to the table without them would ship a screen
// that names a failure it cannot tell the reader how to leave.
func TestEverySupportedPlatformCarriesRecoveryAndANamedTrap(t *testing.T) {
	t.Parallel()
	if len(enrollPlatforms) == 0 {
		t.Fatal("no supported platforms")
	}
	for _, platform := range enrollPlatforms {
		if platform.Name == "" || len(platform.Recovery) == 0 || len(platform.Traps) == 0 {
			t.Fatalf("platform %+v is missing a name, recovery steps, or a trap", platform)
		}
		for _, trap := range platform.Traps {
			if trap.Setting == "" || trap.Consequence == "" || trap.Recovery == "" {
				t.Fatalf("%s trap %+v is missing a setting, consequence, or recovery", platform.Name, trap)
			}
		}
	}
}

// The two traps the specification calls out by name are on the screen as the
// settings a Protectee would actually look for, not as a general warning.
func TestDeviceEnrollScreenNamesTheAndroidAndMacOSTraps(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, enrollPath, session, csrf).Body.String()
	for _, want := range []string{
		"Always-on VPN",
		"Block connections without VPN",
		"On-Demand",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing trap %q:\n%s", want, body)
		}
	}
}

// The screen is honest in the direction that costs the product something too:
// the tunnel off means unprotected, and a determined bypass on hardware the
// Protectee owns is not fully preventable.
func TestDeviceEnrollScreenStatesTheLimitsOfProtection(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, enrollPath, session, csrf).Body.String()
	for _, want := range []string{
		"you are not protected",
		"encrypted DNS",
		"not fully preventable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing honesty copy %q:\n%s", want, body)
		}
	}
}

// A screen with no action must say why there is none, or it reads as broken.
func TestDeviceEnrollScreenExplainsWhyEnrollmentIsNotOpenYet(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, enrollPath, session, csrf).Body.String()
	for _, want := range []string{
		"not open yet",
		"Enrollment allowlist",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("enroll screen missing unavailability copy %q:\n%s", want, body)
		}
	}
}

// Copy ships before the action does, so the screen mints nothing: it carries
// no form, and no route was added that could change anything.
func TestDeviceEnrollScreenOffersNoEnrollmentAction(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, enrollPath, session, csrf).Body.String()
	if strings.Contains(body, "<form") || strings.Contains(strings.ToLower(body), "method=\"post\"") {
		t.Fatalf("enroll screen offers a form:\n%s", body)
	}

	request := httptest.NewRequest(http.MethodPost, enrollPath, strings.NewReader("csrf_token="+csrf.Value))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	request.AddCookie(csrf)
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /devices/enroll = %d, want 405", recorder.Code)
	}
}

// The dashboard already tells a Protectee that enrollment is unavailable; that
// sentence is where they go to read what enrolling will mean.
func TestDashboardLinksToTheDeviceEnrollScreen(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")

	body := get(t, env, "https://lite.nightloch.app/", session, csrf).Body.String()
	if !strings.Contains(body, `href="/devices/enroll"`) {
		t.Fatalf("dashboard does not link to the enroll screen:\n%s", body)
	}
}
