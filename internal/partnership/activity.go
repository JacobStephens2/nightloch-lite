package partnership

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// activityEntry is one append-only chronology entry for a Protectee. Entries
// are never edited; corrections append (S3-17). Details holds structured
// fields used to render authorized prose and is never product UI (S3-49).
type activityEntry struct {
	ProtecteeID   int64
	Action        string
	ActorUserID   *int64
	ActorEmail    string
	TargetEmail   string
	PartnershipID *int64
	InvitationID  *int64
	OccurredAt    time.Time
	EffectiveAt   *time.Time
	Details       map[string]any
}

func appendActivity(ctx context.Context, tx *sql.Tx, entry activityEntry) (int64, error) {
	details, err := json.Marshal(entry.Details)
	if err != nil {
		return 0, fmt.Errorf("encode Activity details: %w", err)
	}
	var effectiveAt any
	if entry.EffectiveAt != nil {
		effectiveAt = entry.EffectiveAt.UTC().Unix()
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO activity_entries(
			protectee_id, action, actor_user_id, actor_email, target_email,
			partnership_id, invitation_id, occurred_at, effective_at, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ProtecteeID, entry.Action, entry.ActorUserID, nullString(entry.ActorEmail),
		nullString(entry.TargetEmail), entry.PartnershipID, entry.InvitationID,
		entry.OccurredAt.UTC().Unix(), effectiveAt, string(details))
	if err != nil {
		return 0, fmt.Errorf("append Activity entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read Activity entry id: %w", err)
	}
	return id, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
