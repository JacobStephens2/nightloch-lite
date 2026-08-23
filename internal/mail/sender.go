// Package mail transmits prepared, single-recipient transactional messages.
package mail

import "context"

// Envelope is one fully rendered transactional message for one recipient.
type Envelope struct {
	To             string
	Subject        string
	Text           string
	HTML           string
	ReplyTo        string
	IdempotencyKey string
}

// Sender transmits a prepared transactional envelope.
type Sender interface {
	Send(context.Context, Envelope) error
}

// RecordingSender captures envelopes for tests without making network requests.
type RecordingSender struct {
	Envelopes []Envelope
	SendError error
}

// Send records envelope and returns the configured error, if any.
func (s *RecordingSender) Send(_ context.Context, envelope Envelope) error {
	s.Envelopes = append(s.Envelopes, envelope)
	return s.SendError
}
