-- Users that sign in through an external identity provider have no password.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- External identities are keyed on (issuer, subject), never on the email, so a
-- matching email at the provider cannot take over a local account.
CREATE TABLE user_identities (
  issuer        TEXT NOT NULL,
  subject       TEXT NOT NULL,
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ,
  PRIMARY KEY (issuer, subject)
);
CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);
