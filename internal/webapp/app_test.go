package webapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	githubauth "github.com/JacobStephens2/nightloch-lite/internal/auth"
	"github.com/JacobStephens2/nightloch-lite/internal/database"
	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

type fakeVerifier struct{}

func (fakeVerifier) Verify(_ context.Context, token, _ string) error {
	if token != "human" {
		return githubauth.ErrHumanCheck
	}
	return nil
}

func newTestApp(t *testing.T) (*App, *mailmessage.RecordingSender, *githubauth.Service) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	baseURL, _ := url.Parse("https://lite.nightloch.app")
	sender := &mailmessage.RecordingSender{}
	service := githubauth.NewService(githubauth.NewStore(db), sender, fakeVerifier{}, baseURL)
	app, err := New(Options{
		Auth: service, DB: db, TurnstileSite: "site-key", Version: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, sender, service
}

func TestAuthHTTPFlowCookieSecurityCSRFAndSignOut(t *testing.T) {
	t.Parallel()
	app, sender, service := newTestApp(t)

	getSignIn := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/sign-in", nil)
	getSignIn.RemoteAddr = "127.0.0.1:32100"
	getRecorder := httptest.NewRecorder()
	app.ServeHTTP(getRecorder, getSignIn)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), "Sign in") {
		t.Fatalf("GET /sign-in = %d %q", getRecorder.Code, getRecorder.Body.String())
	}
	csrfCookie := cookieNamed(t, getRecorder.Result().Cookies(), csrfCookieName)
	if !csrfCookie.Secure || !csrfCookie.HttpOnly || csrfCookie.Domain != "" || csrfCookie.Path != "/" || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF cookie attributes = %+v", csrfCookie)
	}

	missingCSRF := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/sign-in", strings.NewReader("email=person%40example.com"))
	missingCSRF.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingRecorder := httptest.NewRecorder()
	app.ServeHTTP(missingRecorder, missingCSRF)
	if missingRecorder.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d", missingRecorder.Code)
	}

	form := url.Values{
		"email":                 []string{"person@example.com"},
		"csrf_token":            []string{csrfCookie.Value},
		"cf-turnstile-response": []string{"human"},
	}
	postSignIn := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/sign-in", strings.NewReader(form.Encode()))
	postSignIn.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postSignIn.Header.Set("X-Forwarded-For", "203.0.113.77")
	postSignIn.RemoteAddr = "127.0.0.1:32100"
	postSignIn.AddCookie(csrfCookie)
	postRecorder := httptest.NewRecorder()
	app.ServeHTTP(postRecorder, postSignIn)
	if postRecorder.Code != http.StatusSeeOther || postRecorder.Header().Get("Location") != "/auth/check-email" || len(sender.Envelopes) != 1 {
		t.Fatalf("POST /sign-in = %d location=%q envelopes=%#v", postRecorder.Code, postRecorder.Header().Get("Location"), sender.Envelopes)
	}

	const textPrefix = "Sign in to Nightloch (this link expires in 15 minutes and can be used once):\n\n"
	link := strings.TrimPrefix(sender.Envelopes[0].Text, textPrefix)
	consume := httptest.NewRequest(http.MethodGet, link, nil)
	consumeRecorder := httptest.NewRecorder()
	app.ServeHTTP(consumeRecorder, consume)
	if consumeRecorder.Code != http.StatusSeeOther || consumeRecorder.Header().Get("Location") != "/" {
		t.Fatalf("consume = %d location=%q", consumeRecorder.Code, consumeRecorder.Header().Get("Location"))
	}
	sessionCookie := cookieNamed(t, consumeRecorder.Result().Cookies(), sessionCookieName)
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.Domain != "" || sessionCookie.Path != "/" || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie attributes = %+v", sessionCookie)
	}

	home := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/", nil)
	home.AddCookie(sessionCookie)
	home.AddCookie(csrfCookie)
	homeRecorder := httptest.NewRecorder()
	app.ServeHTTP(homeRecorder, home)
	if homeRecorder.Code != http.StatusOK || !strings.Contains(homeRecorder.Body.String(), "person@example.com") || !strings.Contains(homeRecorder.Body.String(), "Your protection") {
		t.Fatalf("GET / = %d %q", homeRecorder.Code, homeRecorder.Body.String())
	}

	signOutForm := url.Values{"csrf_token": []string{csrfCookie.Value}}
	signOut := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/sign-out", strings.NewReader(signOutForm.Encode()))
	signOut.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	signOut.AddCookie(sessionCookie)
	signOut.AddCookie(csrfCookie)
	signOutRecorder := httptest.NewRecorder()
	app.ServeHTTP(signOutRecorder, signOut)
	if signOutRecorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /sign-out = %d", signOutRecorder.Code)
	}
	if _, err := service.Authenticate(context.Background(), sessionCookie.Value); err == nil {
		t.Fatal("session remained valid after sign out")
	}
}

func TestMagicLinkDeliveryFailureKeepsGenericPublicResponse(t *testing.T) {
	t.Parallel()

	app, sender, _ := newTestApp(t)
	sender.SendError = errors.New("delivery unavailable")

	get := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/sign-in", nil)
	get.RemoteAddr = "127.0.0.1:32100"
	getRecorder := httptest.NewRecorder()
	app.ServeHTTP(getRecorder, get)
	csrfCookie := cookieNamed(t, getRecorder.Result().Cookies(), csrfCookieName)

	form := url.Values{
		"email":                 []string{"person@example.com"},
		"csrf_token":            []string{csrfCookie.Value},
		"cf-turnstile-response": []string{"human"},
	}
	post := httptest.NewRequest(http.MethodPost, "https://lite.nightloch.app/sign-in", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("X-Forwarded-For", "203.0.113.78")
	post.RemoteAddr = "127.0.0.1:32100"
	post.AddCookie(csrfCookie)
	postRecorder := httptest.NewRecorder()
	app.ServeHTTP(postRecorder, post)

	if postRecorder.Code != http.StatusSeeOther || postRecorder.Header().Get("Location") != "/auth/check-email" {
		t.Fatalf("POST /sign-in after delivery failure = %d location=%q", postRecorder.Code, postRecorder.Header().Get("Location"))
	}
}

func TestHealthChecksWritableDatabase(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)
	request := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/health", nil)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("GET /health = %d %q", recorder.Code, recorder.Body.String())
	}
}

func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %+v", name, cookies)
	return nil
}
