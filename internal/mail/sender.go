// Package mail transmits prepared, single-recipient transactional messages.
package mail

import (
	"context"
	"strconv"
)

// Envelope is one fully rendered transactional message for one recipient.
type Envelope struct {
	To             string
	Subject        string
	Text           string
	HTML           string
	ReplyTo        string
	IdempotencyKey string
}

// Receipt is what the provider says about one accepted message. Its
// ProviderMessageID is how an asynchronous delivery callback names the message
// it reports on, so a caller that wants delivery events must keep it.
type Receipt struct {
	ProviderMessageID string
}

// Sender transmits a prepared transactional envelope.
type Sender interface {
	Send(context.Context, Envelope) (Receipt, error)
}

// RecordingSender captures envelopes for tests without making network requests.
type RecordingSender struct {
	Envelopes []Envelope
	SendError error
}

// Send records envelope and returns the configured error, if any. Accepted
// envelopes receive a deterministic provider message id — `recorded-1` for the
// first, and so on — so a test can name the message a delivery callback
// reports on without a real provider.
func (s *RecordingSender) Send(_ context.Context, envelope Envelope) (Receipt, error) {
	s.Envelopes = append(s.Envelopes, envelope)
	if s.SendError != nil {
		return Receipt{}, s.SendError
	}
	return Receipt{ProviderMessageID: RecordedMessageID(len(s.Envelopes))}, nil
}

// RecordedMessageID is the provider message id RecordingSender assigns to its
// sequence-th accepted envelope, counting from one.
func RecordedMessageID(sequence int) string {
	return "recorded-" + strconv.Itoa(sequence)
}
