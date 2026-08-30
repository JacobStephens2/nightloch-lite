package main

import (
	"io"
	"os"
	"testing"
)

func TestResolverQueryLogRetentionCommandReportsTheEnforcementRule(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = original })

	runErr := run([]string{"resolver-query-log-retention"})
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if want := "168h\n"; string(got) != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
