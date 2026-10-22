DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE password_hash IS NULL) THEN
    RAISE EXCEPTION 'cannot roll back 000002: some users have no password hash (they sign in through an external identity provider); delete them first';
  END IF;
END $$;

DROP TABLE IF EXISTS user_identities;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
