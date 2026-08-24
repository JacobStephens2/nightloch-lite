package webapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/partnership"
)

// signedCallback is a provider delivery callback signed with the endpoint
// secret the test app is configured with.
func signedCallback(t *testing.T, env *testEnv, eventID, body string, at time.Time) *http.Request {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(testWebhookSecret, "whsec_"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(eventID + "." + stamp + "." + body))

	request := httptest.NewRequest(http.MethodPost,
		"https://lite.nightloch.app"+deliveryWebhookPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("svix-id", eventID)
	request.Header.Set("svix-timestamp", stamp)
	request.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	env.app.now = func() time.Time { return at }
	return request
}

// bouncedInvitation queues one invitation, hands it to the provider, and
// returns the provider message id a callback would name.
func bouncedInvitation(t *testing.T, env *testEnv) (int64, string) {
	t.Helper()
	ctx := context.Background()
	var protectee int64
	if err := env.db.QueryRow(
		"INSERT INTO users(email, created_at) VALUES ('protectee@example.com', unixepoch()) RETURNING id").
		Scan(&protectee); err != nil {
		t.Fatal(err)
	}
	if _, err := env.partnerships.InvitePartner(ctx, partnership.InviteCommand{
		ProtecteeID: protectee, InviteeEmail: "invitee@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.partnerships.DispatchDue(ctx, env.sender, 10); err != nil {
		t.Fatal(err)
	}
	var messageID string
	if err := env.db.QueryRow(`SELECT provider_message_id FROM mail_delivery_jobs
		WHERE recipient_email = 'invitee@example.com'`).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	return protectee, messageID
}

func bouncePayload(messageID string, at time.Time) string {
	return fmt.Sprintf(`{"type":"email.bounced","created_at":%q,
		"data":{"email_id":%q,"to":["invitee@example.com"],
		        "bounce":{"type":"Permanent","subType":"General","message":"mailbox does not exist"}}}`,
		at.UTC().Format(time.RFC3339), messageID)
}

func deliveryState(t *testing.T, env *testEnv, protectee int64) partnership.DeliveryState {
	t.Helper()
	view, err := env.partnerships.ProtecteeView(context.Background(), protectee)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.PendingInvitations) != 1 {
		t.Fatalf("pending invitations = %+v", view.PendingInvitations)
	}
	return view.PendingInvitations[0].DeliveryState
}

func TestSignedDeliveryCallbackMarksTheInvitationUndelivered(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()

	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, signedCallback(t, env, "evt_1", bouncePayload(messageID, at), at))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("signed callback = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := deliveryState(t, env, protectee); got != partnership.DeliveryFailed {
		t.Fatalf("invitation delivery state = %q", got)
	}
}

// The endpoint holds no session, so the signature is the whole authorization.
// An unsigned or wrongly signed callback must change nothing.
func TestUnauthenticatedDeliveryCallbackChangesNothing(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()
	body := bouncePayload(messageID, at)

	unsigned := httptest.NewRequest(http.MethodPost,
		"https://lite.nightloch.app"+deliveryWebhookPath, strings.NewReader(body))
	unsigned.Header.Set("Content-Type", "application/json")

	// Correct headers over a body the sender never signed.
	tampered := httptest.NewRequest(http.MethodPost,
		"https://lite.nightloch.app"+deliveryWebhookPath,
		strings.NewReader(bouncePayload(messageID, at.Add(time.Second))))
	for header, values := range signedCallback(t, env, "evt_1", body, at).Header {
		tampered.Header[header] = values
	}

	for name, request := range map[string]*http.Request{
		"unsigned":       unsigned,
		"rewritten body": tampered,
	} {
		recorder := httptest.NewRecorder()
		env.app.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s callback = %d, want 401", name, recorder.Code)
		}
	}
	if got := deliveryState(t, env, protectee); got != partnership.DeliverySent {
		t.Fatalf("an unauthenticated callback changed delivery state to %q", got)
	}
}

// Providers retry a callback until they see it acknowledged, so the same
// delivery event must be safe to receive many times.
func TestRedeliveredCallbackIsAcceptedAndIdempotent(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()
	body := bouncePayload(messageID, at)

	for attempt := range 3 {
		recorder := httptest.NewRecorder()
		env.app.ServeHTTP(recorder, signedCallback(t, env, "evt_1", body, at))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("attempt %d = %d", attempt, recorder.Code)
		}
	}
	var entries int
	if err := env.db.QueryRow(
		"SELECT COUNT(*) FROM activity_entries WHERE action = ?",
		partnership.ActionDeliveryFailed).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("a redelivered callback appended Activity %d times", entries)
	}
	if got := deliveryState(t, env, protectee); got != partnership.DeliveryFailed {
		t.Fatalf("delivery state = %q", got)
	}
}

// An event kind Nightloch does not act on is acknowledged rather than retried
// forever, and it changes nothing.
func TestUnhandledEventKindIsAcknowledgedWithoutChangingState(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()
	body := fmt.Sprintf(`{"type":"email.opened","created_at":%q,"data":{"email_id":%q}}`,
		at.UTC().Format(time.RFC3339), messageID)

	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, signedCallback(t, env, "evt_open", body, at))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unhandled event = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := deliveryState(t, env, protectee); got != partnership.DeliverySent {
		t.Fatalf("an unhandled event changed delivery state to %q", got)
	}
}

// The delivery endpoint is not a signed-in surface: it must not read a
// session, and it must not become a way to ask about one.
func TestDeliveryCallbackGrantsNoInformationToItsCaller(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	_, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()

	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, signedCallback(t, env, "evt_1", bouncePayload(messageID, at), at))
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("callback response body = %q", body)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("callback response set cookies: %+v", recorder.Result().Cookies())
	}
}

// testWebhookSecret is the endpoint signing secret the test app runs with.
const testWebhookSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

// deliverEvent applies one signed provider callback of the given type to the
// message addressed to recipient.
func deliverEvent(t *testing.T, env *testEnv, eventID, recipient, eventType, bounceType string, at time.Time) {
	t.Helper()
	var messageID string
	if err := env.db.QueryRow(`SELECT COALESCE(provider_message_id, '') FROM mail_delivery_jobs
		WHERE recipient_email = ? ORDER BY id DESC LIMIT 1`, recipient).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if messageID == "" {
		t.Fatalf("no provider message id stored for %s", recipient)
	}
	body := fmt.Sprintf(`{"type":%q,"created_at":%q,"data":{"email_id":%q,"bounce":{"type":%q}}}`,
		eventType, at.UTC().Format(time.RFC3339), messageID, bounceType)
	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, signedCallback(t, env, eventID, body, at))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("%s callback = %d %q", eventType, recorder.Code, recorder.Body.String())
	}
}

// A Protectee whose invitation bounced is told what happened and what the one
// remedy is: the invitation is immutable, so a wrong address takes a cancel
// and a fresh invitation (S3-47).
func TestBouncedInvitationTellsTheProtecteeToCancelAndReinvite(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	session, csrf := signIn(t, env, "protectee@example.com")
	sendInvitation(t, env, session, csrf, "typo@example.com", false)
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 10); err != nil {
		t.Fatal(err)
	}

	deliverEvent(t, env, "evt_bounce", "typo@example.com", "email.bounced", "Permanent", time.Now())

	body := dashboard(t, env, session, csrf)
	mustContain(t, body, "Invitation not delivered",
		"cancel this invitation and send a new one to the corrected address",
		"/partners/sent-invitations/")
}

// Reachability is a warning both entitled viewers read the same way, and a
// User outside the relationship reads not at all.
func TestUnreachablePartnerWarnsBothPartiesAndNobodyElse(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, protecteeCSRF, partner, partnerCSRF, partnershipID := livePartnership(t, env)
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 50); err != nil {
		t.Fatal(err)
	}

	deliverEvent(t, env, "evt_bounce", "partner@example.com", "email.bounced", "Permanent", time.Now())

	protecteeBody := dashboard(t, env, protectee, protecteeCSRF)
	mustContain(t, ownProtection(protecteeBody), "Nightloch cannot reach this Partner",
		"This does not change their Partner powers")
	// The Partnership itself is untouched: the Protectee's own controls still
	// read as a live Partnership with the ordinary Cooling-off exit.
	mustContain(t, protecteeBody, "/partners/partnerships/"+strconv.FormatInt(partnershipID, 10)+"/request-end")

	// The unreachable Partner keeps every power, including ending now.
	partnerBody := dashboard(t, env, partner, partnerCSRF)
	mustContain(t, peopleYouProtect(t, partnerBody),
		"/partners/partnerships/"+strconv.FormatInt(partnershipID, 10)+"/end")

	// A signed-in User in neither role learns nothing about either address.
	outsider, outsiderCSRF := signIn(t, env, "outsider@example.com")
	outsiderBody := dashboard(t, env, outsider, outsiderCSRF)
	for _, leak := range []string{"partner@example.com", "protectee@example.com",
		"Nightloch cannot reach this Partner", "Delivery delayed"} {
		if strings.Contains(outsiderBody, leak) {
			t.Fatalf("an unrelated viewer read %q:\n%s", leak, outsiderBody)
		}
	}
}

// A Partner Nightloch can reach again stops carrying a warning, and the
// Partnership never changed while it did.
func TestDeliveredEventClearsTheReachabilityWarning(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, protecteeCSRF, _, _, _ := livePartnership(t, env)
	if _, err := env.partnerships.DispatchDue(context.Background(), env.sender, 50); err != nil {
		t.Fatal(err)
	}
	at := time.Now()

	deliverEvent(t, env, "evt_delayed", "partner@example.com", "email.delivery_delayed", "", at)
	if body := ownProtection(dashboard(t, env, protectee, protecteeCSRF)); !strings.Contains(body, "Delivery delayed") {
		t.Fatalf("a delayed delivery raised no warning:\n%s", body)
	}

	deliverEvent(t, env, "evt_delivered", "partner@example.com", "email.delivered", "", at.Add(time.Minute))
	body := ownProtection(dashboard(t, env, protectee, protecteeCSRF))
	if strings.Contains(body, "Delivery delayed") {
		t.Fatalf("recovery left the warning up:\n%s", body)
	}
	mustContain(t, body, "Reachable by email")
}

// A payload without a usable timestamp must still land. Stamped at the zero
// time it would sort behind everything already applied and quietly do nothing.
func TestCallbackWithoutATimestampIsStampedOnArrival(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	protectee, messageID := bouncedInvitation(t, env)
	at := time.Now().UTC()
	body := fmt.Sprintf(`{"type":"email.bounced","data":{"email_id":%q,"bounce":{"type":"Permanent"}}}`, messageID)

	recorder := httptest.NewRecorder()
	env.app.ServeHTTP(recorder, signedCallback(t, env, "evt_undated", body, at))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("undated callback = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := deliveryState(t, env, protectee); got != partnership.DeliveryFailed {
		t.Fatalf("an undated bounce left delivery state at %q", got)
	}
}
