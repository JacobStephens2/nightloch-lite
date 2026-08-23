// Package emailaddr normalizes the verified email addresses that identify
// every Nightloch User. One implementation keeps sign-in, Partner
// invitations, and mail routing agreeing on what an address is.
package emailaddr

import (
	"errors"
	netmail "net/mail"
	"strings"
)

// ErrInvalid rejects an address Nightloch will not accept as an identity.
var ErrInvalid = errors.New("enter a valid email address")

// Normalize returns the lower-cased address, or ErrInvalid. It accepts only a
// bare address: no display name, no header-injecting whitespace, and a dotted
// domain.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 254 || strings.ContainsAny(raw, "\r\n") {
		return "", ErrInvalid
	}
	address, err := netmail.ParseAddress(raw)
	if err != nil || address.Name != "" || address.Address != raw {
		return "", ErrInvalid
	}
	at := strings.LastIndexByte(address.Address, '@')
	if at <= 0 || at == len(address.Address)-1 || !strings.Contains(address.Address[at+1:], ".") {
		return "", ErrInvalid
	}
	return strings.ToLower(address.Address), nil
}
