package mail

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRecordingSenderCapturesRecipientSpecificEnvelopes(t *testing.T) {
	t.Parallel()

	recorder := &RecordingSender{}
	want := []Envelope{
		{
			To:             "partner-one@example.com",
			Subject:        "First message",
			Text:           "Plain text one",
			HTML:           "<p>HTML one</p>",
			ReplyTo:        "protectee@example.com",
			IdempotencyKey: "partnership/one/partner-one",
		},
		{
			To:             "partner-two@example.com",
			Subject:        "Second message",
			Text:           "Plain text two",
			HTML:           "<p>HTML two</p>",
			ReplyTo:        "alerts@nightloch.app",
			IdempotencyKey: "partnership/two/partner-two",
		},
	}
	for sequence, envelope := range want {
		receipt, err := recorder.Send(context.Background(), envelope)
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if receipt.ProviderMessageID != RecordedMessageID(sequence+1) {
			t.Fatalf("provider message id = %q", receipt.ProviderMessageID)
		}
	}
	if !reflect.DeepEqual(recorder.Envelopes, want) {
		t.Fatalf("recorded envelopes = %#v, want %#v", recorder.Envelopes, want)
	}

	sendError := errors.New("mail unavailable")
	recorder.SendError = sendError
	receipt, err := recorder.Send(context.Background(), want[0])
	if !errors.Is(err, sendError) {
		t.Fatalf("Send error = %v, want %v", err, sendError)
	}
	if receipt.ProviderMessageID != "" {
		t.Fatalf("a refused envelope reported provider message id %q", receipt.ProviderMessageID)
	}
}
