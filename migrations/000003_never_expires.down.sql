-- Non-expiring shares have no sensible timestamp to fall back to; give them a
-- year from now so the NOT NULL constraint can be restored without data loss.
UPDATE shares SET expires_at = NOW() + INTERVAL '365 days' WHERE expires_at IS NULL;
ALTER TABLE shares ALTER COLUMN expires_at SET NOT NULL;
