package database

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
}
