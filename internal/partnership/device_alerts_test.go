package partnership

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestRecordDeviceEnrolledUnpartnered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")

	if err := service.RecordDeviceEnrolled(ctx, protectee, "iPhone 15"); err != nil {
		t.Fatalf("RecordDeviceEnrolled for unpartnered Protectee: %v", err)
	}

	// Verify Activity entry exists
	view, err := service.Activity(ctx, protectee, protectee, ActivityQuery{Category: CategoryDevices})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(view.Entries))
	}
	entry := view.Entries[0]
	if entry.Action != ActionDeviceEnrolled {
		t.Errorf("entry.Action = %s, want %s", entry.Action, ActionDeviceEnrolled)
	}
	if !strings.Contains(entry.Summary(), `Device "iPhone 15"`) {
		t.Errorf("entry.Summary() = %q, want device name", entry.Summary())
	}

	// Verify 0 mail jobs queued
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("queued %d mail jobs for unpartnered Protectee, want 0", count)
	}
}

func TestRecordDeviceEnrolledPartnered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	partnerA := createUser(t, db, "partner-a@example.com")
	partnerB := createUser(t, db, "partner-b@example.com")

	invA := invite(t, service, protectee, "partner-a@example.com", false)
	acceptFor(t, service, invA, partnerA)
	invB := invite(t, service, protectee, "partner-b@example.com", false)
	acceptFor(t, service, invB, partnerB)

	// Clear mail delivery jobs from invitation/acceptance
	if _, err := db.Exec("DELETE FROM mail_delivery_jobs"); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordDeviceEnrolled(ctx, protectee, "Work Laptop"); err != nil {
		t.Fatalf("RecordDeviceEnrolled: %v", err)
	}

	// Verify Activity entry
	view, err := service.Activity(ctx, protectee, protectee, ActivityQuery{Category: CategoryDevices})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(view.Entries))
	}
	activityID := view.Entries[0].ID

	// Verify mail delivery jobs
	rows, err := db.Query(`SELECT recipient_email, subject, body_text, body_html, reply_to, activity_id, message_kind
		FROM mail_delivery_jobs ORDER BY recipient_email`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type jobRow struct {
		recipient   string
		subject     string
		text        string
		html        string
		replyTo     string
		activityID  sql.NullInt64
		messageKind string
	}
	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.recipient, &j.subject, &j.text, &j.html, &j.replyTo, &j.activityID, &j.messageKind); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	if len(jobs) != 2 {
		t.Fatalf("queued %d mail delivery jobs, want 2 (one per partner)", len(jobs))
	}

	wantRecipients := []string{"partner-a@example.com", "partner-b@example.com"}
	for i, wantRecipient := range wantRecipients {
		job := jobs[i]
		if job.recipient != wantRecipient {
			t.Errorf("job[%d].recipient = %q, want %q", i, job.recipient, wantRecipient)
		}
		if job.messageKind != MessageDeviceEnrolled {
			t.Errorf("job[%d].messageKind = %q, want %q", i, job.messageKind, MessageDeviceEnrolled)
		}
		wantSubject := `[Nightloch] protectee@example.com enrolled Device "Work Laptop"`
		if job.subject != wantSubject {
			t.Errorf("job[%d].subject = %q, want %q", i, job.subject, wantSubject)
		}
		if job.replyTo != alertsAddress {
			t.Errorf("job[%d].replyTo = %q, want %q", i, job.replyTo, alertsAddress)
		}
		if !job.activityID.Valid || job.activityID.Int64 != activityID {
			t.Errorf("job[%d].activityID = %v, want %d", i, job.activityID, activityID)
		}
		// Content checks: Protectee email, Device name, UTC stamp, traffic filtered, VPN fail mode warning
		for _, required := range []string{
			"protectee@example.com",
			`Device "Work Laptop"`,
			"UTC",
			"filtered through Nightloch",
			"Turning off the VPN on the Device removes protection",
		} {
			if !strings.Contains(job.text, required) {
				t.Errorf("job[%d].text missing %q:\n%s", i, required, job.text)
			}
		}
	}
}

func TestRecordDeviceRemovedByProtectee_Unpartnered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")

	if err := service.RecordDeviceRemoved(ctx, protectee, protectee, "iPhone 15"); err != nil {
		t.Fatalf("RecordDeviceRemoved: %v", err)
	}

	view, err := service.Activity(ctx, protectee, protectee, ActivityQuery{Category: CategoryDevices})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(view.Entries))
	}
	if view.Entries[0].Action != ActionDeviceRemoved {
		t.Errorf("entry.Action = %s, want %s", view.Entries[0].Action, ActionDeviceRemoved)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mail_delivery_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("queued %d mail jobs for unpartnered Protectee removal, want 0", count)
	}
}

func TestRecordDeviceRemovedByProtectee_Partnered(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	partnerA := createUser(t, db, "partner-a@example.com")
	partnerB := createUser(t, db, "partner-b@example.com")

	invA := invite(t, service, protectee, "partner-a@example.com", false)
	acceptFor(t, service, invA, partnerA)
	invB := invite(t, service, protectee, "partner-b@example.com", false)
	acceptFor(t, service, invB, partnerB)

	if _, err := db.Exec("DELETE FROM mail_delivery_jobs"); err != nil {
		t.Fatal(err)
	}

	if err := service.RecordDeviceRemoved(ctx, protectee, protectee, "Work Laptop"); err != nil {
		t.Fatalf("RecordDeviceRemoved: %v", err)
	}

	rows, err := db.Query(`SELECT recipient_email, subject, body_text, reply_to, message_kind
		FROM mail_delivery_jobs ORDER BY recipient_email`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type jobRow struct {
		recipient string
		subject   string
		text      string
		replyTo   string
		kind      string
	}
	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.recipient, &j.subject, &j.text, &j.replyTo, &j.kind); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}

	wantSubject := `[Nightloch] protectee@example.com removed Device "Work Laptop"`
	for _, j := range jobs {
		if j.kind != MessageDeviceRemoved {
			t.Errorf("kind = %q, want %q", j.kind, MessageDeviceRemoved)
		}
		if j.subject != wantSubject {
			t.Errorf("subject = %q, want %q", j.subject, wantSubject)
		}
		if j.replyTo != alertsAddress {
			t.Errorf("replyTo = %q, want %q", j.replyTo, alertsAddress)
		}
		for _, required := range []string{
			"protectee@example.com",
			`Device "Work Laptop"`,
			"UTC",
			"Protection on that hardware has ended",
		} {
			if !strings.Contains(j.text, required) {
				t.Errorf("job text missing %q:\n%s", required, j.text)
			}
		}
	}
}

func TestRecordDeviceRemovedByPartner(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	partnerA := createUser(t, db, "partner-a@example.com")
	partnerB := createUser(t, db, "partner-b@example.com")

	invA := invite(t, service, protectee, "partner-a@example.com", false)
	acceptFor(t, service, invA, partnerA)
	invB := invite(t, service, protectee, "partner-b@example.com", false)
	acceptFor(t, service, invB, partnerB)

	if _, err := db.Exec("DELETE FROM mail_delivery_jobs"); err != nil {
		t.Fatal(err)
	}

	// partnerA removes the device
	if err := service.RecordDeviceRemoved(ctx, partnerA, protectee, "Work Laptop"); err != nil {
		t.Fatalf("RecordDeviceRemoved: %v", err)
	}

	rows, err := db.Query(`SELECT recipient_email, subject, body_text, reply_to, message_kind
		FROM mail_delivery_jobs ORDER BY recipient_email`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type jobRow struct {
		recipient string
		subject   string
		text      string
		replyTo   string
		kind      string
	}
	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.recipient, &j.subject, &j.text, &j.replyTo, &j.kind); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	// Should be sent to protectee and partnerB (NOT partnerA)
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2 (protectee and partnerB)", len(jobs))
	}
	if jobs[0].recipient != "partner-b@example.com" || jobs[1].recipient != "protectee@example.com" {
		t.Errorf("job recipients = %q, %q; want partner-b and protectee", jobs[0].recipient, jobs[1].recipient)
	}

	wantSubject := `[Nightloch] partner-a@example.com removed Device "Work Laptop"`
	for _, j := range jobs {
		if j.kind != MessageDeviceRemoved {
			t.Errorf("kind = %q, want %q", j.kind, MessageDeviceRemoved)
		}
		if j.subject != wantSubject {
			t.Errorf("subject = %q, want %q", j.subject, wantSubject)
		}
		if j.replyTo != alertsAddress {
			t.Errorf("replyTo = %q, want %q", j.replyTo, alertsAddress)
		}
		for _, required := range []string{
			"partner-a@example.com",
			"protectee@example.com",
			`Device "Work Laptop"`,
			"UTC",
			"Protection on that hardware has ended",
		} {
			if !strings.Contains(j.text, required) {
				t.Errorf("job text missing %q:\n%s", required, j.text)
			}
		}
	}
}

func TestRecordDeviceRemoved_Unauthorized(t *testing.T) {
	t.Parallel()
	service, db := newTestService(t)
	ctx := context.Background()

	protectee := createUser(t, db, "protectee@example.com")
	stranger := createUser(t, db, "stranger@example.com")

	err := service.RecordDeviceRemoved(ctx, stranger, protectee, "Work Laptop")
	if err == nil {
		t.Fatal("expected error for unauthorized removal, got nil")
	}
}
