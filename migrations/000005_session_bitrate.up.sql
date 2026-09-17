-- Target bitrate for a transcode, derived from the source at play time.
-- Without it Jellyfin falls back to 128 kbit/s and downscales to 416x234.
ALTER TABLE share_sessions ADD COLUMN video_bitrate INTEGER;
