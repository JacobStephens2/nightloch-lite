package database

// sendLedgerMigration and deliveryEventMigration are the schema versions that
// introduced the invitation send ledger and the provider delivery-event
// tables. Tests that stand a database up at the version before one of them
// name it here rather than counting from the end of the list, which moves
// every time a migration is appended.
const (
	sendLedgerMigration    = 3
	deliveryEventMigration = 4
	userDeletionMigration  = 5
)

var migrations = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		email TEXT NOT NULL COLLATE NOCASE UNIQUE,
		created_at INTEGER NOT NULL
	);

	CREATE TABLE magic_link_tokens (
		id INTEGER PRIMARY KEY,
		token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
		email TEXT NOT NULL COLLATE NOCASE,
		request_ip TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		used_at INTEGER,
		CHECK(expires_at > created_at)
	);
	CREATE INDEX magic_link_tokens_email_created_idx
		ON magic_link_tokens(email, created_at);
	CREATE INDEX magic_link_tokens_ip_created_idx
		ON magic_link_tokens(request_ip, created_at);

	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		revoked_at INTEGER,
		CHECK(expires_at > created_at)
	);
	CREATE INDEX sessions_user_idx ON sessions(user_id);
	CREATE INDEX sessions_expiry_idx ON sessions(expires_at);

	CREATE TABLE auth_rate_limits (
		kind TEXT NOT NULL CHECK(kind IN ('email', 'ip')),
		key_hash BLOB NOT NULL CHECK(length(key_hash) = 32),
		window_started_at INTEGER NOT NULL,
		request_count INTEGER NOT NULL CHECK(request_count >= 0),
		PRIMARY KEY(kind, key_hash)
	);

	CREATE TABLE health_probe (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		touched_at INTEGER NOT NULL
	);
	INSERT INTO health_probe(id, touched_at) VALUES (1, unixepoch());`,
	`CREATE TABLE partnerships (
		id INTEGER PRIMARY KEY,
		protectee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		partner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		edge_type TEXT NOT NULL CHECK(edge_type IN ('peer', 'guardian')),
		log_sharing INTEGER NOT NULL CHECK(log_sharing IN (0, 1)),
		accepted_at INTEGER NOT NULL,
		cooling_off_requested_at INTEGER,
		cooling_off_ends_at INTEGER,
		ended_at INTEGER,
		ended_reason TEXT,
		CHECK(protectee_id <> partner_id),
		CHECK((cooling_off_requested_at IS NULL) = (cooling_off_ends_at IS NULL)),
		CHECK((ended_at IS NULL) = (ended_reason IS NULL))
	);
	-- One live directional relationship at a time; ended rows stay as receipts,
	-- and the reverse direction is a separate row with its own lifecycle.
	CREATE UNIQUE INDEX partnerships_live_direction_idx
		ON partnerships(protectee_id, partner_id) WHERE ended_at IS NULL;
	CREATE INDEX partnerships_partner_idx ON partnerships(partner_id) WHERE ended_at IS NULL;

	CREATE TABLE partner_invitations (
		id INTEGER PRIMARY KEY,
		protectee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		invitee_email TEXT NOT NULL COLLATE NOCASE,
		log_sharing INTEGER NOT NULL CHECK(log_sharing IN (0, 1)),
		token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash) = 32),
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		last_sent_at INTEGER NOT NULL,
		send_count INTEGER NOT NULL CHECK(send_count >= 1),
		state TEXT NOT NULL CHECK(state IN ('pending', 'accepted', 'declined', 'canceled', 'expired')),
		resolved_at INTEGER,
		resolved_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
		CHECK(expires_at > created_at),
		CHECK((state = 'pending') = (resolved_at IS NULL))
	);
	CREATE UNIQUE INDEX partner_invitations_pending_idx
		ON partner_invitations(protectee_id, invitee_email) WHERE state = 'pending';

	CREATE TABLE activity_entries (
		id INTEGER PRIMARY KEY,
		protectee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		action TEXT NOT NULL,
		-- A NULL actor is Nightloch itself, such as a completed timer.
		actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
		actor_email TEXT,
		target_email TEXT,
		partnership_id INTEGER REFERENCES partnerships(id) ON DELETE SET NULL,
		invitation_id INTEGER REFERENCES partner_invitations(id) ON DELETE SET NULL,
		occurred_at INTEGER NOT NULL,
		effective_at INTEGER,
		-- Structured detail for rendering authorized prose. Never product UI.
		details TEXT NOT NULL
	);
	CREATE INDEX activity_entries_protectee_idx
		ON activity_entries(protectee_id, occurred_at DESC, id DESC);

	CREATE TABLE mail_delivery_jobs (
		id INTEGER PRIMARY KEY,
		activity_id INTEGER REFERENCES activity_entries(id) ON DELETE SET NULL,
		invitation_id INTEGER REFERENCES partner_invitations(id) ON DELETE CASCADE,
		partnership_id INTEGER REFERENCES partnerships(id) ON DELETE CASCADE,
		message_kind TEXT NOT NULL,
		-- Exactly one recipient per job. A combined recipient list is never stored.
		recipient_email TEXT NOT NULL,
		subject TEXT NOT NULL,
		body_text TEXT NOT NULL,
		body_html TEXT NOT NULL,
		reply_to TEXT NOT NULL,
		idempotency_key TEXT NOT NULL UNIQUE,
		state TEXT NOT NULL CHECK(state IN ('queued', 'sent', 'delivered', 'delayed', 'failed')),
		attempts INTEGER NOT NULL CHECK(attempts >= 0),
		next_attempt_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		sent_at INTEGER,
		last_error TEXT
	);
	CREATE INDEX mail_delivery_jobs_due_idx ON mail_delivery_jobs(state, next_attempt_at);`,
	// One row per accepted send or resend, which is what a rolling-window
	// quota needs and neither the invitation's own last_sent_at nor its
	// send_count can answer. Rows outlive the pending state deliberately: a
	// Protectee who cancels an invitation has still spent the send.
	`CREATE TABLE partner_invitation_sends (
		id INTEGER PRIMARY KEY,
		protectee_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		invitation_id INTEGER NOT NULL REFERENCES partner_invitations(id) ON DELETE CASCADE,
		sent_at INTEGER NOT NULL
	);
	CREATE INDEX partner_invitation_sends_window_idx
		ON partner_invitation_sends(protectee_id, sent_at);
	-- Invitations that predate this table have sent at least once, and their
	-- last send is the only one recorded anywhere. Counting it keeps an
	-- upgrade from handing every Protectee a fresh window.
	INSERT INTO partner_invitation_sends(protectee_id, invitation_id, sent_at)
		SELECT protectee_id, id, last_sent_at FROM partner_invitations;`,
	// Provider delivery events are what makes reachability honest: a send
	// Resend accepted can still bounce hours later. Correlating one takes the
	// provider's own message id, and processing one safely takes a record of
	// which events have already been applied, because a provider retries a
	// callback it did not see acknowledged.
	`ALTER TABLE mail_delivery_jobs ADD COLUMN provider_message_id TEXT;
	-- Two jobs cannot share a provider message, so a callback names exactly
	-- one recipient's message. NULL stays free: an unsent job has no id, and a
	-- provider response Nightloch could not parse never gets one.
	CREATE UNIQUE INDEX mail_delivery_jobs_provider_idx
		ON mail_delivery_jobs(provider_message_id) WHERE provider_message_id IS NOT NULL;
	-- last_event_at is the provider's own timestamp for the newest event
	-- already applied to this job, so events that arrive out of order cannot
	-- let a stale delivery overwrite a later bounce.
	ALTER TABLE mail_delivery_jobs ADD COLUMN last_event_at INTEGER;
	CREATE INDEX mail_delivery_jobs_recipient_idx
		ON mail_delivery_jobs(recipient_email, id);

	CREATE TABLE mail_delivery_events (
		id INTEGER PRIMARY KEY,
		-- The provider's own event id. UNIQUE is the idempotency: a redelivered
		-- callback is recognised before it can change any state twice.
		provider_event_id TEXT NOT NULL UNIQUE,
		provider_message_id TEXT NOT NULL,
		-- NULL when the event names a message this control plane never queued,
		-- such as a sign-in link. The event is still recorded, and still
		-- changes no Partnership state.
		job_id INTEGER REFERENCES mail_delivery_jobs(id) ON DELETE CASCADE,
		event_type TEXT NOT NULL,
		-- occurred_at is the provider's clock; received_at is Nightloch's.
		occurred_at INTEGER NOT NULL,
		received_at INTEGER NOT NULL,
		applied INTEGER NOT NULL CHECK(applied IN (0, 1))
	);
	CREATE INDEX mail_delivery_events_job_idx
		ON mail_delivery_events(job_id, occurred_at);`,
	// A deleted User's row survives their deletion. Everything that references
	// it — another Protectee's retained Activity, a former Partner's receipt,
	// the Partnerships both hang off — would otherwise cascade away, erasing
	// attributable events owned by people who still exist. What does not
	// survive is the identity on the row: the verified email is replaced with
	// a value that is not an address, so it can never be matched by a sign-in
	// and the real address is free to register again.
	`ALTER TABLE users ADD COLUMN deleted_at INTEGER;`,
}
