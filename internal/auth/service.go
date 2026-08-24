package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/emailaddr"
	mailmessage "github.com/JacobStephens2/nightloch-lite/internal/mail"
)

const (
	magicLinkTTL  = 15 * time.Minute
	sessionTTL    = 30 * 24 * time.Hour
	tokenBytes    = 32
	signInReplyTo = "alerts@nightloch.app"
)

var (
	ErrInvalidEmail = emailaddr.ErrInvalid
	ErrHumanCheck   = errors.New("human verification failed")
	ErrDelivery     = errors.New("magic-link delivery failed")
)

// HumanVerifier validates a Turnstile response for the originating IP.
type HumanVerifier interface {
	Verify(context.Context, string, string) error
}

// Service coordinates authentication without exposing raw tokens to Store.
type Service struct {
	store    *Store
	sender   mailmessage.Sender
	verifier HumanVerifier
	baseURL  *url.URL
	now      func() time.Time
	random   io.Reader
}

func NewService(store *Store, sender mailmessage.Sender, verifier HumanVerifier, baseURL *url.URL) *Service {
	return &Service{
		store:    store,
		sender:   sender,
		verifier: verifier,
		baseURL:  baseURL,
		now:      time.Now,
		random:   rand.Reader,
	}
}

// RequestMagicLink verifies the request, applies both quotas, stores only a
// token hash, and sends the raw token directly to the requested address.
func (s *Service) RequestMagicLink(ctx context.Context, rawEmail, ip, humanToken string) error {
	if err := s.verifier.Verify(ctx, humanToken, ip); err != nil {
		if errors.Is(err, ErrHumanCheck) {
			return ErrHumanCheck
		}
		return fmt.Errorf("verify human challenge: %w", err)
	}
	email, err := emailaddr.Normalize(rawEmail)
	if err != nil {
		return err
	}
	if parsed := net.ParseIP(ip); parsed == nil {
		return fmt.Errorf("invalid request IP")
	}

	rawToken, tokenHash, err := s.newToken()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if err := s.store.CreateMagicLink(ctx, email, ip, tokenHash, now, now.Add(magicLinkTTL)); err != nil {
		return err
	}

	link := *s.baseURL
	link.Path = "/auth/consume"
	query := link.Query()
	query.Set("token", rawToken)
	link.RawQuery = query.Encode()
	linkString := link.String()
	linkDigest := sha256.Sum256([]byte(linkString))
	envelope := mailmessage.Envelope{
		To:      email,
		Subject: "Your Nightloch sign-in link",
		Text:    "Sign in to Nightloch (this link expires in 15 minutes and can be used once):\n\n" + linkString,
		HTML: `<p>Sign in to Nightloch. This link expires in 15 minutes and can be used once.</p><p><a href="` +
			html.EscapeString(linkString) + `">Sign in to Nightloch</a></p>`,
		ReplyTo:        signInReplyTo,
		IdempotencyKey: "nightloch-magic-" + hex.EncodeToString(linkDigest[:]),
	}
	// Sign-in links are single-use and short-lived, so this path keeps no
	// provider message id: there is no Partner reachability to report on it.
	if _, err := s.sender.Send(ctx, envelope); err != nil {
		// The user receives a generic response, but a failed delivery must not
		// leave a live bearer token behind.
		_ = s.store.InvalidateMagicLink(ctx, tokenHash, now)
		return fmt.Errorf("%w: %v", ErrDelivery, err)
	}
	return nil
}

// ConsumeMagicLink exchanges one valid link for one 30-day session.
func (s *Service) ConsumeMagicLink(ctx context.Context, rawLinkToken string) (string, User, error) {
	linkHash, err := decodeAndHash(rawLinkToken)
	if err != nil {
		return "", User{}, ErrInvalidLink
	}
	rawSession, sessionHash, err := s.newToken()
	if err != nil {
		return "", User{}, err
	}
	now := s.now().UTC()
	user, err := s.store.ConsumeMagicLink(ctx, linkHash, sessionHash, now, now.Add(sessionTTL))
	if err != nil {
		return "", User{}, err
	}
	return rawSession, user, nil
}

func (s *Service) Authenticate(ctx context.Context, rawSessionToken string) (User, error) {
	hash, err := decodeAndHash(rawSessionToken)
	if err != nil {
		return User{}, ErrInvalidLink
	}
	return s.store.Authenticate(ctx, hash, s.now().UTC())
}

func (s *Service) SignOut(ctx context.Context, rawSessionToken string) error {
	hash, err := decodeAndHash(rawSessionToken)
	if err != nil {
		return nil
	}
	return s.store.RevokeSession(ctx, hash, s.now().UTC())
}

func (s *Service) newToken() (string, [32]byte, error) {
	bytes := make([]byte, tokenBytes)
	if _, err := io.ReadFull(s.random, bytes); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate bearer token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(bytes)
	return raw, sha256.Sum256(bytes), nil
}

func decodeAndHash(raw string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != tokenBytes {
		return [32]byte{}, ErrInvalidLink
	}
	return sha256.Sum256(decoded), nil
}
