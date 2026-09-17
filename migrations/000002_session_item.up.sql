-- Pin the item a session is allowed to stream.
-- Without this, ServeStream had to take the item id from the viewer's query string,
-- which let any share link stream any item in the library.
ALTER TABLE share_sessions ADD COLUMN jellyfin_item_id VARCHAR(64);

-- Backfill existing sessions with their share's item so they keep playing.
UPDATE share_sessions s
   SET jellyfin_item_id = sh.jellyfin_item_id
  FROM shares sh
 WHERE s.share_id = sh.id
   AND s.jellyfin_item_id IS NULL;
