-- Video codec negotiated with the viewer's browser at play time. When the source
-- codec is one the browser can decode it is stored here and Jellyfin copies the
-- stream instead of re-encoding it.
ALTER TABLE share_sessions ADD COLUMN video_codec VARCHAR(32);
