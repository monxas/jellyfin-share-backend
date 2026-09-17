-- Optional quality ceiling per share, so a link can be capped for a recipient on a
-- weak connection without touching the library or the server-wide settings.
-- NULL on both means "as good as the source", which is the previous behaviour.
ALTER TABLE shares ADD COLUMN max_video_height INTEGER;
ALTER TABLE shares ADD COLUMN max_video_bitrate INTEGER;

-- The session carries the resolved ceiling, like every other playback parameter.
ALTER TABLE share_sessions ADD COLUMN max_video_height INTEGER;
