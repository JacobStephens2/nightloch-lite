package emailaddr

import (
	"errors"
	"testing"
)

func TestNormalizeRejectsDisplayNamesAndMalformedInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"",
		"Person <person@example.com>",
		"person@localhost",
		"person@example.com\nBcc:x@example.com",
		"person@example.com, other@example.com",
	} {
		if _, err := Normalize(input); !errors.Is(err, ErrInvalid) {
			t.Errorf("Normalize(%q) = %v", input, err)
		}
	}
}

func TestNormalizeLowercasesAndTrims(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"Person@Example.com", "  person@example.com  "} {
		got, err := Normalize(input)
		if err != nil || got != "person@example.com" {
			t.Fatalf("Normalize(%q) = %q, %v", input, got, err)
		}
	}
}
