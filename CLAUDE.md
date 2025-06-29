# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mithrandir is a lightweight, high-performance reverse proxy written in Go that implements access control through a "secret path" authentication mechanism. The proxy supports multiple applications with host-based routing, only allowing clients to access backend services after they first visit a predefined secret URL path for each app.

## Architecture

This is a single-file Go application (`main.go`) with the following key components:

- **Multi-App Configuration**: Support for multiple applications with host-based routing
- **Reverse Proxy**: Built using Go's `net/http/httputil.ReverseProxy` to forward requests to upstream services
- **Session Management**: Redis-backed IP-based session tracking with per-app configurable TTL
- **Client IP Detection**: Extracts real client IPs from various proxy headers (Cloudflare, Akamai, etc.)
- **Access Control**: Dual-layer access control via per-app allow-list IPs and secret path authentication

### Key Data Flow
1. Client requests arrive at the proxy
2. Request hostname is used to identify the target application
3. If no app is configured for the hostname, return 404
4. Client IP is extracted from headers or RemoteAddr
5. IP is checked against the app's allow-list patterns (if configured)
6. If not in allow-list, check Redis for existing app-specific session
7. If no session exists, require secret path access to create session
8. Forward authenticated requests to the app's upstream service

## Development Commands

### Building
```bash
# Install dependencies
go mod tidy

# Build binary
go build -o mithrandir main.go

# Run locally
./mithrandir
```

### Docker
```bash
# Build Docker image
docker build -t mithrandir .

# Multi-stage build creates optimized Alpine-based runtime image
```

### Testing
- No formal test suite exists - manual testing with Redis backend required
- Test with different proxy configurations and IP scenarios

## Configuration

Configuration supports multiple apps via two methods:

### Method 1: JSON Configuration (Recommended)
Set `APPS_CONFIG` environment variable with JSON array:
```json
[
  {
    "hostname": "app1.example.com",
    "secret_path": "/secret123",
    "upstream_url": "http://app1:8080",
    "allow_ips": "192.168.1.100,10.0.0.0/8",
    "session_ttl": "24h",
    "auto_renew": "true"
  },
  {
    "hostname": "app2.example.com", 
    "secret_path": "/different-secret",
    "upstream_url": "http://app2:3000",
    "session_ttl": "1h",
    "auto_renew": "false"
  }
]
```

### Method 2: Numbered Environment Variables
- `APP_1_HOSTNAME`: Hostname for first app (required)
- `APP_1_UPSTREAM_URL`: Upstream URL for first app (required)
- `APP_1_SECRET_PATH`: Secret path prefix (default: `/secret_path`)
- `APP_1_ALLOW_IPS`: Comma-separated IP regex patterns
- `APP_1_SESSION_TTL`: Session duration (default: `10m`)
- `APP_1_AUTO_RENEW`: Extend session on each request (default: `true`)
- Continue with `APP_2_*`, `APP_3_*`, etc.

### Global Configuration
- `LISTEN_ADDRESS`: Proxy listen address (default: `:8080`)
- `REDIS_ADDRESS`: Redis connection string (default: `redis:6379`)
- `REDIS_PASSWORD`: Redis password (default: empty)

## Code Structure

- **main()**: Application initialization and HTTP server setup
- **loadAppConfigurations()**: Load app configs from JSON or environment variables
- **loadAppsFromJSON()**: Parse JSON configuration for multiple apps
- **loadAppsFromEnv()**: Parse numbered environment variables for apps
- **parseAppConfig()**: Parse individual app configuration with validation
- **handleRequest()**: Core request processing with host-based routing and session management
- **clientIP()**: Real IP extraction from various proxy headers
- **getenv()**: Environment variable helper with defaults

## Key Implementation Details

- Host-based routing using `request.Host` with port stripping
- Per-app configuration stored in `map[string]*AppConfig`
- App-specific Redis cache keys: `app:{hostname}:ip:{ip}`
- Uses Go's regex compilation for per-app IP allow-list matching
- Browser detection via User-Agent regex for redirect behavior
- Redis operations are synchronous with basic error handling
- URL path manipulation to strip app-specific secret prefix before forwarding
- Support for both `URL.Path` and `URL.RawPath` handling
- 404 response for unmapped hostnames

## Security Considerations

This is a defensive security tool that implements access control through obscurity. It should be used as an additional layer of protection, not as a replacement for proper authentication. The codebase handles IP-based sessions and does not store sensitive user data.