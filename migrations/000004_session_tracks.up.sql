-- Audio/subtitle selection is pinned to the session, like jellyfin_item_id, so the
-- stream proxy never has to take a stream index from the viewer's query string.
ALTER TABLE share_sessions ADD COLUMN audio_stream_index INTEGER;
ALTER TABLE share_sessions ADD COLUMN subtitle_stream_index INTEGER;
