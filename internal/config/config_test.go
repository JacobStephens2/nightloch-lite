package config

import "testing"

func TestParseBaseURL(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"", "http://lite.nightloch.app", "https://lite.nightloch.app/path",
		"https://user@lite.nightloch.app", "https://lite.nightloch.app?q=x",
	} {
		if _, err := parseBaseURL(bad); err == nil {
			t.Errorf("parseBaseURL(%q) succeeded, want error", bad)
		}
	}
	got, err := parseBaseURL("https://lite.nightloch.app/")
	if err != nil {
		t.Fatalf("parseBaseURL(valid): %v", err)
	}
	if got.String() != "https://lite.nightloch.app" {
		t.Fatalf("base URL = %q", got.String())
	}
}

func TestValidateLoopbackAddr(t *testing.T) {
	t.Parallel()

	for _, good := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if err := validateLoopbackAddr(good); err != nil {
			t.Errorf("validateLoopbackAddr(%q): %v", good, err)
		}
	}
	for _, bad := range []string{":8080", "0.0.0.0:8080", "10.0.0.1:8080", "broken"} {
		if err := validateLoopbackAddr(bad); err == nil {
			t.Errorf("validateLoopbackAddr(%q) succeeded, want error", bad)
		}
	}
}
