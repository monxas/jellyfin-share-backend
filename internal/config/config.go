package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                    int
	DatabaseDSN             string
	JellyfinBaseURL         string
	JellyfinAPIKey          string
	BackendAPIKey           string
	PublicBaseURL           string
	SessionHeartbeatTimeout time.Duration
	LogLevel                string
	RateLimitRequests       int
	RateLimitWindow         time.Duration
	// MaxTranscodeBitrate caps the target bitrate handed to Jellyfin for a
	// transcode. It never applies to direct stream, which is untouched.
	MaxTranscodeBitrate int
	// StreamVideoCodec / StreamAudioCodec tell Jellyfin what the share page can
	// actually play. Naming a codec does not force a transcode: a source that
	// already matches is stream-copied. Set either to an empty string to send no
	// preference, which is what the backend did before and what leaves AV1 or HEVC
	// to reach the browser untouched.
	StreamVideoCodec string
	StreamAudioCodec string
}

func Load() *Config {
	return &Config{
		Port:                    getEnvInt("JFSHARE_PORT", 8080),
		DatabaseDSN:             getEnv("JFSHARE_DB_DSN", "postgres://jfshare:jfshare@localhost:5432/jfshare?sslmode=disable"),
		JellyfinBaseURL:         getEnv("JFSHARE_JELLYFIN_BASE_URL", "http://localhost:8096"),
		JellyfinAPIKey:          getEnv("JFSHARE_JELLYFIN_API_KEY", ""),
		BackendAPIKey:           getEnv("JFSHARE_BACKEND_API_KEY", ""),
		PublicBaseURL:           getEnv("JFSHARE_PUBLIC_BASE_URL", "http://localhost:8080"),
		SessionHeartbeatTimeout: time.Duration(getEnvInt("JFSHARE_SESSION_HEARTBEAT_TIMEOUT_SECONDS", 120)) * time.Second,
		LogLevel:                getEnv("JFSHARE_LOG_LEVEL", "info"),
		RateLimitRequests:       getEnvInt("JFSHARE_RATE_LIMIT_REQUESTS", 100),
		RateLimitWindow:         time.Duration(getEnvInt("JFSHARE_RATE_LIMIT_WINDOW_SECONDS", 60)) * time.Second,
		MaxTranscodeBitrate:     getEnvInt("JFSHARE_MAX_TRANSCODE_BITRATE", 20000000),
		StreamVideoCodec:        getEnvAllowEmpty("JFSHARE_STREAM_VIDEO_CODEC", "h264"),
		StreamAudioCodec:        getEnvAllowEmpty("JFSHARE_STREAM_AUDIO_CODEC", "aac"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAllowEmpty differs from getEnv: an explicitly empty value is honoured
// rather than falling back to the default, so a codec preference can be disabled.
func getEnvAllowEmpty(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
