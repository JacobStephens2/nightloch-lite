package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestMigrationLifecycleAndWritableHealth(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := CheckCurrent(ctx, db); !errors.Is(err, ErrMigrationsPending) {
		t.Fatalf("CheckCurrent before migration = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}
	if err := CheckCurrent(ctx, db); err != nil {
		t.Fatalf("CheckCurrent: %v", err)
	}
	if err := Writable(ctx, db); err != nil {
		t.Fatalf("Writable: %v", err)
	}
}

func TestSnapshotIsConsistentAndNeverOverwrites(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	db, err := Open(filepath.Join(directory, "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users(email, created_at) VALUES ('person@example.com', unixepoch())"); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(directory, "backups", "before.db")
	if err := Snapshot(ctx, db, destination); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := Snapshot(ctx, db, destination); err == nil {
		t.Fatal("Snapshot overwrote an existing destination")
	}

	copyDB, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var email string
	if err := copyDB.QueryRow("SELECT email FROM users").Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "person@example.com" {
		t.Fatalf("snapshot email = %q", email)
	}
}
