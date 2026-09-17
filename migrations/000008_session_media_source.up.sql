-- The media source actually played. For most items this equals the item id, but
-- an item with alternate versions has a distinct id per source, and the subtitle
-- indices we hand out are read from that source.
ALTER TABLE share_sessions ADD COLUMN media_source_id VARCHAR(64);
