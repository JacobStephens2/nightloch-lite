package webapp

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"
)

func (a *App) csrfToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && validCSRFToken(cookie.Value) {
		return cookie.Value, nil
	}
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(a.random, bytes); err != nil {
		return "", fmt.Errorf("generate CSRF token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: token, Path: "/",
		Expires: a.now().Add(24 * time.Hour), MaxAge: int((24 * time.Hour) / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func validCSRFToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32
}

func (a *App) verifyCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// A provider delivery callback has no session to ride on, so CSRF is
		// not the control that protects it; its signature is, and the body
		// that signature covers must not be consumed by ParseForm here.
		if r.URL.Path == deliveryWebhookPath {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form submission.", http.StatusBadRequest)
			return
		}
		cookie, err := r.Cookie(csrfCookieName)
		formToken := r.PostForm.Get("csrf_token")
		if err != nil || !validCSRFToken(cookie.Value) || !validCSRFToken(formToken) ||
			subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) != 1 {
			http.Error(w, "Your form expired. Go back and try again.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; connect-src 'self' https://challenges.cloudflare.com; style-src 'self'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (a *App) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("request panic", "method", r.Method, "path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
				http.Error(w, "Nightloch could not complete that request.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
