-- A NULL expires_at means the share never expires.
ALTER TABLE shares ALTER COLUMN expires_at DROP NOT NULL;
