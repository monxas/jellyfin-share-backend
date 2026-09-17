-- Text subtitles are delivered as a WebVTT sidecar instead of being burned in,
-- which avoids forcing a video transcode and lets the viewer toggle them.
-- subtitle_stream_index keeps its meaning: burn this one in (image subtitles).
ALTER TABLE share_sessions ADD COLUMN vtt_subtitle_index INTEGER;
