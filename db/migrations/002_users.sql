-- users who can sign in. passwords are never stored, only bcrypt hashes.
-- postgres runs this automatically on a fresh database volume; on an existing
-- one, apply it by hand (see the readme).

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,               -- like "u_3f2a...", used as user_id in orders
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- emails are stored lower case, so "Alice@X.com" and "alice@x.com" are one user
    CONSTRAINT email_is_lower_case CHECK (email = lower(email))
);
