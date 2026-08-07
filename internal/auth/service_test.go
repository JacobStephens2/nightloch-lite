package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
)

type recordingMailer struct {
	to   []string
	link []string
	err  error
}

func (m *recordingMailer) SendMagicLink(_ context.Context, to, link string) error {
	m.to = append(m.to, to)
	m.link = append(m.link, link)
	return m.err
}

type acceptingVerifier struct {
	calls int
}

func (v *acceptingVerifier) Verify(_ context.Context, token, ip string) error {
	v.calls++
	if token != "human" || ip == "" {
		return ErrHumanCheck
	}
	return nil
}

func newTestService(t *testing.T) (*Service, *recordingMailer) {
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
	mailer := &recordingMailer{}
	service := NewService(NewStore(db), mailer, &acceptingVerifier{}, baseURL)
	service.now = func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) }
	return service, mailer
}

func TestMagicLinkSessionLifecycleStoresOnlyHashes(t *testing.T) {
	t.Parallel()
	service, mailer := newTestService(t)
	ctx := context.Background()

	if err := service.RequestMagicLink(ctx, "Person@Example.com", "203.0.113.7", "human"); err != nil {
		t.Fatalf("RequestMagicLink: %v", err)
	}
	if len(mailer.link) != 1 || mailer.to[0] != "person@example.com" {
		t.Fatalf("mail = to %v link %v", mailer.to, mailer.link)
	}
	parsed, err := url.Parse(mailer.link[0])
	if err != nil {
		t.Fatal(err)
	}
	rawLink := parsed.Query().Get("token")
	decodedLink, err := base64.RawURLEncoding.DecodeString(rawLink)
	if err != nil {
		t.Fatal(err)
	}
	wantLinkHash := sha256.Sum256(decodedLink)
	var storedLinkHash []byte
	var storedEmail string
	if err := service.store.db.QueryRow("SELECT token_hash, email FROM magic_link_tokens").Scan(&storedLinkHash, &storedEmail); err != nil {
		t.Fatal(err)
	}
	if string(storedLinkHash) != string(wantLinkHash[:]) || storedEmail != "person@example.com" {
		t.Fatalf("stored link data does not match hash/normalized email")
	}
	if strings.Contains(string(storedLinkHash), rawLink) {
		t.Fatal("raw link token was stored")
	}

	rawSession, user, err := service.ConsumeMagicLink(ctx, rawLink)
	if err != nil {
		t.Fatalf("ConsumeMagicLink: %v", err)
	}
	if user.Email != "person@example.com" || rawSession == "" {
		t.Fatalf("user/session = %+v %q", user, rawSession)
	}
	decodedSession, _ := base64.RawURLEncoding.DecodeString(rawSession)
	wantSessionHash := sha256.Sum256(decodedSession)
	var storedSessionHash []byte
	if err := service.store.db.QueryRow("SELECT token_hash FROM sessions").Scan(&storedSessionHash); err != nil {
		t.Fatal(err)
	}
	if string(storedSessionHash) != string(wantSessionHash[:]) || string(storedSessionHash) == rawSession {
		t.Fatal("session is not stored as the expected hash")
	}

	if _, err := service.Authenticate(ctx, rawSession); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, _, err := service.ConsumeMagicLink(ctx, rawLink); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("second ConsumeMagicLink = %v", err)
	}
	if err := service.SignOut(ctx, rawSession); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if _, err := service.Authenticate(ctx, rawSession); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("Authenticate after sign out = %v", err)
	}
}

func TestMagicLinkExpiresAndDeliveryFailureInvalidates(t *testing.T) {
	t.Parallel()
	service, mailer := newTestService(t)
	ctx := context.Background()

	if err := service.RequestMagicLink(ctx, "person@example.com", "203.0.113.8", "human"); err != nil {
		t.Fatal(err)
	}
	raw := mustTokenFromLink(t, mailer.link[0])
	service.now = func() time.Time { return time.Date(2026, 8, 6, 12, 15, 0, 0, time.UTC) }
	if _, _, err := service.ConsumeMagicLink(ctx, raw); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("ConsumeMagicLink at expiry = %v", err)
	}

	service2, mailer2 := newTestService(t)
	mailer2.err = errors.New("mail unavailable")
	err := service2.RequestMagicLink(ctx, "person@example.com", "203.0.113.9", "human")
	if !errors.Is(err, ErrDelivery) {
		t.Fatalf("RequestMagicLink delivery error = %v", err)
	}
	if _, _, err := service2.ConsumeMagicLink(ctx, mustTokenFromLink(t, mailer2.link[0])); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("undelivered link remained valid: %v", err)
	}
}

func TestMagicLinkRateLimitsByEmailAndIP(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	ctx := context.Background()

	for index := range emailRateLimit {
		ip := "203.0.113." + string(rune('1'+index))
		if err := service.RequestMagicLink(ctx, "same@example.com", ip, "human"); err != nil {
			t.Fatalf("email request %d: %v", index+1, err)
		}
	}
	if err := service.RequestMagicLink(ctx, "same@example.com", "203.0.113.99", "human"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("email rate limit = %v", err)
	}

	service2, _ := newTestService(t)
	for index := range ipRateLimit {
		email := "person" + string(rune('a'+index)) + "@example.com"
		if err := service2.RequestMagicLink(ctx, email, "198.51.100.1", "human"); err != nil {
			t.Fatalf("IP request %d: %v", index+1, err)
		}
	}
	if err := service2.RequestMagicLink(ctx, "overflow@example.com", "198.51.100.1", "human"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("IP rate limit = %v", err)
	}
}

func TestNormalizeEmailRejectsDisplayNamesAndMalformedInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", "Person <person@example.com>", "person@localhost", "person@example.com\nBcc:x@example.com"} {
		if _, err := normalizeEmail(input); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("normalizeEmail(%q) = %v", input, err)
		}
	}
	if got, err := normalizeEmail("Person@Example.com"); err != nil || got != "person@example.com" {
		t.Fatalf("normalizeEmail(valid) = %q, %v", got, err)
	}
}

func mustTokenFromLink(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("token")
}
