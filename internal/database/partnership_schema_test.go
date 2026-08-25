package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users(id, email, created_at) VALUES
			(1, 'protectee@example.com', unixepoch()),
			(2, 'partner@example.com', unixepoch())`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPartnershipSchemaKeepsOneLiveRelationshipPerDirection(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO partnerships(protectee_id, partner_id, edge_type, log_sharing, accepted_at)
		VALUES (?, ?, 'peer', 0, unixepoch())`

	if _, err := db.Exec(insert, 1, 2); err != nil {
		t.Fatalf("first Partnership: %v", err)
	}
	if _, err := db.Exec(insert, 1, 2); err == nil {
		t.Fatal("schema allowed two live Partnerships in the same direction")
	}
	// The reverse direction is an independent Reciprocal Partnership.
	if _, err := db.Exec(insert, 2, 1); err != nil {
		t.Fatalf("reverse direction: %v", err)
	}
	if _, err := db.Exec(insert, 1, 1); err == nil {
		t.Fatal("schema allowed a User to partner themselves")
	}

	// Ending a Partnership keeps it as a receipt and frees the direction for a
	// later, separate Partnership between the same Users.
	if _, err := db.Exec(`UPDATE partnerships SET ended_at = unixepoch(), ended_reason = 'partner_ended'
		WHERE protectee_id = 1 AND partner_id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, 1, 2); err != nil {
		t.Fatalf("re-partnering after an ended Partnership: %v", err)
	}
	var receipts int
	if err := db.QueryRow("SELECT COUNT(*) FROM partnerships WHERE protectee_id = 1 AND partner_id = 2").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 {
		t.Fatalf("rows for the same direction = %d, want 2", receipts)
	}
}

func TestPartnerInvitationSchemaGuardsPendingUniquenessAndTokenShape(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO partner_invitations(
			protectee_id, invitee_email, log_sharing, token_hash,
			created_at, expires_at, last_sent_at, send_count, state)
		VALUES (1, ?, 0, ?, unixepoch(), unixepoch() + 604800, unixepoch(), 1, ?)`
	hash := make([]byte, 32)

	if _, err := db.Exec(insert, "invitee@example.com", hash, "pending"); err != nil {
		t.Fatalf("first invitation: %v", err)
	}
	second := append([]byte(nil), hash...)
	second[0] = 1
	if _, err := db.Exec(insert, "INVITEE@example.com", second, "pending"); err == nil {
		t.Fatal("schema allowed a duplicate pending invitation to the same address")
	}
	if _, err := db.Exec(insert, "invitee@example.com", second, "accepted"); err == nil ||
		!strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("resolved invitation without a resolution time = %v", err)
	}
	if _, err := db.Exec(insert, "other@example.com", []byte("short"), "pending"); err == nil {
		t.Fatal("schema accepted a token hash that is not 32 bytes")
	}
}

func TestMailDeliveryJobSchemaRejectsDuplicateIdempotencyKeys(t *testing.T) {
	t.Parallel()
	db := migratedDB(t)
	insert := `INSERT INTO mail_delivery_jobs(
			message_kind, recipient_email, subject, body_text, body_html,
			reply_to, idempotency_key, state, attempts, next_attempt_at, created_at)
		VALUES ('partner_invitation', ?, 's', 't', '<p>t</p>', 'r@example.com', ?, 'queued', 0, unixepoch(), unixepoch())`
	if _, err := db.Exec(insert, "invitee@example.com", "key-1"); err != nil {
		t.Fatalf("first job: %v", err)
	}
	if _, err := db.Exec(insert, "invitee@example.com", "key-1"); err == nil {
		t.Fatal("schema allowed two delivery jobs to share an idempotency key")
	}
	if _, err := db.Exec(insert, "invitee@example.com", "key-2"); err != nil {
		t.Fatalf("distinct job: %v", err)
	}
}

// TestSendLedgerMigrationBackfillsExistingInvitations exercises the only state
// the backfill exists for: a deployed database that already holds invitations
// sent before the send ledger existed. Their last send is the only one
// recorded anywhere, so counting it is what keeps an upgrade from handing
// every Protectee a fresh rolling window.
func TestSendLedgerMigrationBackfillsExistingInvitations(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	// Stand the database up at the schema version before the send ledger,
	// which is where an existing deployment is when this migration arrives.
	applyMigrations(t, db, sendLedgerMigration-1)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users(id, email, created_at) VALUES (1, 'protectee@example.com', 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO partner_invitations(
		protectee_id, invitee_email, log_sharing, token_hash, created_at,
		expires_at, last_sent_at, send_count, state)
		VALUES (1, 'invitee@example.com', 0, randomblob(32), 100, 200, 150, 1, 'pending')`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate forward: %v", err)
	}
	var protecteeID, sentAt int64
	if err := db.QueryRowContext(ctx,
		"SELECT protectee_id, sent_at FROM partner_invitation_sends").
		Scan(&protecteeID, &sentAt); err != nil {
		t.Fatal(err)
	}
	if protecteeID != 1 || sentAt != 150 {
		t.Fatalf("backfilled send = Protectee %d at %d, want 1 at 150", protecteeID, sentAt)
	}
}

// applyMigrations brings a fresh database up to the first count migrations,
// recording them exactly as Migrate would, so a later Migrate resumes rather
// than starting over.
func applyMigrations(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index, migration := range migrations[:count] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply migration %d: %v", index+1, err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch())", index+1); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDeliveryEventMigrationLeavesExistingJobsCorrelatable exercises the
// upgrade an existing deployment takes: jobs sent before delivery events
// existed have no provider message id, and many of them must coexist. A
// UNIQUE index that counted NULLs as equal would refuse the second one.
func TestDeliveryEventMigrationLeavesExistingJobsCorrelatable(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	applyMigrations(t, db, deliveryEventMigration-1)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users(id, email, created_at) VALUES (1, 'protectee@example.com', 1)"); err != nil {
		t.Fatal(err)
	}
	insertJob := `INSERT INTO mail_delivery_jobs(
		message_kind, recipient_email, subject, body_text, body_html, reply_to,
		idempotency_key, state, attempts, next_attempt_at, created_at)
		VALUES ('partner_invitation', ?, 's', 't', 'h', 'r', ?, 'sent', 1, 1, 1)`
	for _, recipient := range []string{"one@example.com", "two@example.com"} {
		if _, err := db.ExecContext(ctx, insertJob, recipient, "key-"+recipient); err != nil {
			t.Fatal(err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("migrate forward: %v", err)
	}
	var uncorrelatable int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM mail_delivery_jobs WHERE provider_message_id IS NULL").
		Scan(&uncorrelatable); err != nil {
		t.Fatal(err)
	}
	if uncorrelatable != 2 {
		t.Fatalf("jobs without a provider message id = %d, want 2", uncorrelatable)
	}
	// Two jobs must never share one provider message: an event names exactly
	// one recipient's message or none at all.
	if _, err := db.ExecContext(ctx,
		"UPDATE mail_delivery_jobs SET provider_message_id = 'shared'"); err == nil {
		t.Fatal("the schema allowed two jobs to share a provider message id")
	}
}

// TestUserDeletionMigrationLeavesExistingUsersUndeleted exercises the upgrade
// an existing deployment takes: every User that predates deletion is a User
// who has not been deleted, and the pseudonymized state has to be reachable
// only by writing it.
func TestUserDeletionMigrationLeavesExistingUsersUndeleted(t *testing.T) {
	t.Parallel()
	db, err := Open(filepath.Join(t.TempDir(), "nightloch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()

	applyMigrations(t, db, userDeletionMigration-1)
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users(id, email, created_at) VALUES (1, 'protectee@example.com', 1)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var email string
	var deletedAt sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT email, deleted_at FROM users WHERE id = 1").
		Scan(&email, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if email != "protectee@example.com" || deletedAt.Valid {
		t.Fatalf("existing User after the migration = %q, deleted_at valid %v", email, deletedAt.Valid)
	}
	// A deleted User frees their address for a new registration, so the
	// uniqueness that made that necessary still has to hold for the pseudonym.
	if _, err := db.ExecContext(ctx,
		"UPDATE users SET email = 'deleted-user-1', deleted_at = 2 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users(id, email, created_at) VALUES (2, 'protectee@example.com', 3)"); err != nil {
		t.Fatalf("the freed address could not be registered again: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO users(id, email, created_at) VALUES (3, 'deleted-user-1', 4)"); err == nil {
		t.Fatal("two Users share one pseudonymized identity")
	}
}
