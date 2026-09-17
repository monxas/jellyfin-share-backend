# Jellyfin Share

A secure, self-hosted solution for creating temporary, shareable links to your Jellyfin media content. Share movies and TV episodes with friends and family without giving them access to your Jellyfin server.

## Features

- **Temporary Share Links** - Create time-limited links that automatically expire
- **Password Protection** - Optionally protect shares with a password
- **Play Limits** - Set maximum total plays and concurrent viewer limits
- **No Account Required** - Recipients don't need a Jellyfin account
- **Secure Streaming** - Media is proxied through the backend; Jellyfin is never exposed
- **Rich Metadata** - Displays poster, backdrop, ratings, cast, and more
- **Admin Dashboard** - Web UI to manage and monitor all shares
- **Session Tracking** - Monitor active viewers and playback sessions
- **HLS Streaming** - Segments are served through the backend, never from Jellyfin
- **Browser-Compatible Playback** - The codec is negotiated with the viewer's browser,
  so an AV1 or HEVC library plays without being needlessly re-encoded for everyone
- **Audio and Subtitle Tracks** - Selectable before playback; text subtitles are
  delivered as a WebVTT sidecar, so the video needs no transcode and the viewer can
  switch them off
- **Per-Share Quality** - Cap a single link at 1080p, 720p or 480p without touching
  the library or the server settings

## Architecture

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────┐
│   Viewer    │────▶│  JF Share API   │────▶│  Jellyfin   │
│  (Browser)  │◀────│   (Go + Svelte) │◀────│   Server    │
└─────────────┘     └─────────────────┘     └─────────────┘
                            │
                            ▼
                    ┌─────────────┐
                    │  PostgreSQL │
                    └─────────────┘
```

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Jellyfin server with API key
- PostgreSQL (included in docker-compose)

### 1. Clone and Configure

```bash
git clone https://github.com/monxas/jellyfin-share-backend.git
cd jellyfin-share-backend

# Copy example environment file
cp .env.example .env
```

### 2. Edit `.env`

```env
# Your Jellyfin server URL
JELLYFIN_URL=http://your-jellyfin-server:8096

# Jellyfin API key (Dashboard → API Keys → Create)
JELLYFIN_API_KEY=your-jellyfin-api-key

# Backend API key for admin access (generate with: openssl rand -hex 32)
BACKEND_API_KEY=your-secure-backend-key

# Public URL where share links will be accessible
PUBLIC_BASE_URL=https://share.yourdomain.com

# Database password
POSTGRES_PASSWORD=your-secure-db-password
```

### 3. Start the Server

```bash
docker-compose up -d
```

The server will be available at `http://localhost:8097`

### 4. Access Admin Dashboard

Navigate to `http://localhost:8097/admin` and enter your `BACKEND_API_KEY`.

## Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `JFSHARE_PORT` | Server port | `8080` |
| `JFSHARE_JELLYFIN_BASE_URL` | Jellyfin server URL | Required |
| `JFSHARE_JELLYFIN_API_KEY` | Jellyfin API key | Required |
| `JFSHARE_BACKEND_API_KEY` | Admin API key | Required |
| `JFSHARE_PUBLIC_BASE_URL` | Public URL for share links | Required |
| `JFSHARE_DB_DSN` | PostgreSQL connection string | Required |
| `JFSHARE_SESSION_HEARTBEAT_TIMEOUT_SECONDS` | Session timeout | `120` |
| `JFSHARE_RATE_LIMIT_REQUESTS` | Rate limit requests | `100` |
| `JFSHARE_RATE_LIMIT_WINDOW_SECONDS` | Rate limit window | `60` |
| `JFSHARE_MAX_TRANSCODE_BITRATE` | Upper bound for a transcode target, in bits per second. Only applies when Jellyfin transcodes; direct stream is unaffected | `20000000` |
| `JFSHARE_STREAM_VIDEO_CODEC` | Fallback video codec, used when the viewer's browser cannot decode the source. A source that already matches is stream-copied, not re-encoded. Empty sends no preference, which lets AV1/HEVC reach the browser untouched | `h264` |
| `JFSHARE_STREAM_AUDIO_CODEC` | Audio codec the share page can play, same rule | `aac` |

## API Reference

### Admin Endpoints

All admin endpoints require the `X-Backend-Key` header with your API key.

#### Create Share
```http
POST /api/admin/shares
Content-Type: application/json
X-Backend-Key: your-api-key

{
  "jellyfinItemId": "abc123",
  "jellyfinUserId": "user123",
  "expiresInMinutes": 1440,
  "neverExpires": false,
  "password": "optional-password",
  "maxTotalPlays": 5,
  "maxConcurrentViewers": 2,
  "maxVideoHeight": 720,
  "maxVideoBitrate": 4000000
}
```

#### List Shares
```http
GET /api/admin/shares
X-Backend-Key: your-api-key
```

#### Get Share Details
```http
GET /api/admin/shares/{id}
X-Backend-Key: your-api-key
```

#### Revoke Share
```http
POST /api/admin/shares/{id}/revoke
X-Backend-Key: your-api-key
```

Add `?jellyfinUserId=...` to scope the call to one owner: the share is only revoked
if it belongs to that user, otherwise the call is refused with 403. The plugin uses
this so one Jellyfin user cannot revoke another user's share. Omitting it keeps full
admin reach. The same applies to `GET /api/admin/shares/{id}/analytics`.

#### Update Share
```http
PATCH /api/admin/shares/{id}
Content-Type: application/json
X-Backend-Key: your-api-key

{
  "maxTotalPlays": 10,
  "extendMinutes": 1440
}
```

#### Share Analytics
```http
GET /api/admin/shares/{id}/analytics
X-Backend-Key: your-api-key
```

Returns total views, unique viewers, average watch time and views per day for the
last 30 days.

#### Server Stats
```http
GET /api/admin/stats
X-Backend-Key: your-api-key
```

### Public Endpoints

#### Get Share Info
```http
GET /api/public/shares/{token}
```

#### Validate Password
```http
POST /api/public/shares/{token}/password
Content-Type: application/json

{
  "password": "share-password"
}
```

#### Start Playback
```http
POST /api/public/shares/{token}/play
```

Optional query parameters, all validated against the item and then pinned to the
session - the stream proxy never reads them from a later request:
`videoCodecs` (comma-separated list the browser can decode, e.g. `h264,hevc`),
`audioStreamIndex`, `subtitleStreamIndex`, and `audioLanguage` / `subtitleLanguage`,
which take precedence for an episode because the track list was probed on a
different one.

The response carries `subtitleUrl` when a text subtitle was selected.

#### List Episodes (Season and Series shares)
```http
GET /api/public/shares/{token}/episodes
```

#### Start Episode Playback
```http
POST /api/public/shares/{token}/episodes/{episodeId}/play
```

#### Finish Playback
```http
POST /api/public/sessions/{sessionId}/finish
```

#### Media, Images and Subtitles
```http
GET /api/public/stream/{sessionId}/{path}
GET /api/public/images/{token}/{type}
GET /api/public/subtitles/{sessionId}/{index}.vtt
```

#### Heartbeat (keep session alive)
```http
POST /api/public/sessions/{sessionId}/heartbeat
Content-Type: application/json

{
  "positionSeconds": 120
}
```

## Development

### Prerequisites

- Go 1.23+ (see `go.mod`)
- Node.js 20+ (the production image builds the frontend with `node:20`)
- Docker and Docker Compose

### Setup

```bash
# Start development environment with hot reload
docker-compose -f docker-compose.dev.yml up

# Backend runs on http://localhost:8097
# Vite dev server runs on http://localhost:5173
```

### Project Structure

```
.
├── cmd/server/          # Application entrypoint
├── internal/
│   ├── config/          # Configuration management
│   ├── database/        # Database operations & migrations
│   ├── handlers/        # HTTP handlers (admin & public)
│   ├── jellyfin/        # Jellyfin API client
│   ├── middleware/      # Auth, rate limiting, sessions
│   ├── models/          # Data models
│   └── proxy/           # Stream, image & subtitle proxy
├── migrations/          # SQL migrations
├── web/
│   └── src/
│       └── components/  # Svelte components
└── docker-compose.yml   # Production compose
```

### Building

```bash
# Build production Docker image
docker build -t jellyfin-share .

# Build frontend only
cd web && npm run build
```

## Security Considerations

- **Jellyfin is never exposed** - All media requests are proxied through the backend
- **HMAC-signed session cookies** - Password sessions use cryptographically signed tokens
- **Bcrypt password hashing** - Share passwords are securely hashed
- **Rate limiting** - Public endpoints are rate-limited to prevent abuse
- **IP hashing** - Client IPs are hashed for privacy in audit logs
- **Automatic session cleanup** - Stale sessions are automatically terminated
- **Credentials stay server-side** - The Jellyfin key travels as a request header, never
  in a URL that could be echoed back into a manifest the viewer receives
- **Playback is pinned to the session** - The item, media source, codec, bitrate and
  track selection are resolved once when playback starts. The proxy never takes them
  from a request, so a share link cannot be pointed at another item

## Companion Plugin

For seamless integration, install the [Jellyfin Share Plugin](https://github.com/monxas/jellyfin-share-plugin) to create share links directly from the Jellyfin UI.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.
