![Logo](images/mithrandir-wide.png)

## 🧙You shall not pass!

`mithrandir` is a lightweight, high-performance reverse proxy written in Go that restricts access to backend services 
unless the client first accesses a predefined **secret path** (e.g., `/13b84d2a-faff-4b02-bef0-9f7898252659`). 
Once accessed, the proxy allows the client’s IP to continue accessing the backend for a configurable time. 
It supports **multiple applications** with host-based routing, allowing you to protect several services with different configurations. 
It can be deployed as a sidecar along with your main containers or separately. 
It uses Redis for multi-instance or clustered deployment support.

This proxy can be useful for:
- Adding another layer of protection for self-hosted apps like **Immich** or **NextCloud**
- Avoiding the use of basic auth or complex login pages
- Maintaining compatibility with mobile apps (no auth headers/IDP)
- Lightweight access gating for dev/staging services
- **Multi-app deployments** where different services need different secret paths and configurations

---

## 🚀 Features

- 🏗️ **Multi-app support** with host-based routing
- 🔐 Restricts backend access unless secret path is visited
- ⏳ TTL-based session tracking (configurable per app)
- 🧠 Redis-backed session store with app isolation
- ⚙️ Configurable via environment variables or JSON
- 🐳 Dockerized for easy deployment as a sidecar
- 📄 Logs all traffic and access attempts with app identification
- 🎯 Per-app IP allow-lists and session management

---

## 🧪 How It Works

1. User accesses: `https://app1.yourdomain.com/secret_path` or `https://app2.yourdomain.com/different_secret`
2. The proxy identifies the app based on the request hostname
3. Their IP is recorded as "allowed" for that specific app (stored in Redis)
4. For the next N minutes, all requests from their IP to that app are allowed
5. Requests from other IPs or to unmapped hostnames are blocked
6. Each app can have different secret paths, upstream URLs, IP allow-lists, and session TTLs

### Real Client IP Handling

When requests pass through proxies or load balancers, the original client IP is often replaced with the proxy's IP. To address this, `mithrandir` extracts the real client IP from custom headers added by these intermediaries. This ensures accurate IP-based session tracking.

The tool supports the following headers to retrieve the real client IP:
- `CF-Connecting-IP` (Cloudflare)
- `True-Client-IP` (Akamai)
- `X-Real-IP` (Common)
- `X-Forwarded-For` (Common)
- `X-Cluster-Client-IP` (Common)
- `Fastly-Client-IP` (Fastly)
- `Forwarded` (RFC 7239)

If none of these headers are present, the proxy falls back to using the IP from the `RemoteAddr` field.

### Supported Proxies and Load Balancers

`mithrandir` is compatible with the following proxies and load balancers:
- Cloudflare
- Akamai
- Fastly
- NGINX
- HAProxy
- AWS Elastic Load Balancer (ELB)
- Traefik

This ensures seamless integration with a wide range of deployment setups.

## 🔒 Security Notes

This is **not** a replacement for authentication, but a lightweight gate:

- Does **not** require login or tokens
- Tracks IPs, which may be shared (e.g., behind NAT)
- Make sure URLs aren’t leaked via referrers, logs, etc.
- Use long, unguessable secret paths like `/a1b2c3d4-e5f6...`
- Always deploy behind HTTPS

> ⚠️ **Important Security Disclaimer**
>
> This proxy relies on *security through obscurity* and should **not** be used as your sole layer of protection. It serves to make applications more difficult to discover, but it does **not** replace proper authentication or authorization mechanisms.
>
> Ensure that your production setup includes:
> - Strong authentication (e.g., SSO, OAuth, identity providers)
> - TLS/HTTPS encryption
> - IP-based firewalls and rate limiting
> - Bot detection and geo-blocking, if applicable
>
> This proxy was built to allow selective internet exposure of self-hosted services—particularly to enable mobile access for trusted users (like friends and family) without requiring additional apps like Cloudflare WARP. Previously, access control was handled using Cloudflare’s Identity Provider (IDP) with WARP policies, but that approach required installing WARP on mobile devices. This proxy offers a lightweight alternative for accessing services from Android and iOS apps without extra setup.
>
> If your application is only accessed via web browsers, consider using more robust solutions like Cloudflare Access with IDP/WARP instead of relying on this proxy alone.
>
> 🔍 **Note:** Users behind NAT (e.g., multiple devices sharing the same public IP) will be indistinguishable using IP-based tracking. For more granular control, consider enabling cookie- or token-based session tracking.

---

## ⚙️ Configuration

### Multi-App Configuration

Mithrandir supports two methods for configuring multiple applications:

#### Method 1: JSON Configuration (Recommended)

Set the `APPS_CONFIG` environment variable with a JSON array:

```json
[
  {
    "hostname": "immich.example.com",
    "secret_path": "/13b84d2a-faff-4b02-bef0-9f7898252659",
    "upstream_url": "http://immich:2283",
    "allow_ips": "192.168.1.100,10.0.0.0/8",
    "session_ttl": "24h",
    "auto_renew": "true"
  },
  {
    "hostname": "nextcloud.example.com", 
    "secret_path": "/a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    "upstream_url": "http://nextcloud:80",
    "allow_ips": "192.168.1.200",
    "session_ttl": "1h",
    "auto_renew": "false"
  },
  {
    "hostname": "dev-app.example.com",
    "secret_path": "/dev-secret-123",
    "upstream_url": "http://dev-app:3000",
    "session_ttl": "30m",
    "auto_renew": "true"
  }
]
```

#### Method 2: Numbered Environment Variables

Configure each app using numbered environment variables:

```bash
# App 1 Configuration
APP_1_HOSTNAME=immich.example.com
APP_1_UPSTREAM_URL=http://immich:2283
APP_1_SECRET_PATH=/13b84d2a-faff-4b02-bef0-9f7898252659
APP_1_ALLOW_IPS=192.168.1.100,10.0.0.0/8
APP_1_SESSION_TTL=24h
APP_1_AUTO_RENEW=true

# App 2 Configuration
APP_2_HOSTNAME=nextcloud.example.com
APP_2_UPSTREAM_URL=http://nextcloud:80
APP_2_SECRET_PATH=/a1b2c3d4-e5f6-7890-abcd-ef1234567890
APP_2_ALLOW_IPS=192.168.1.200
APP_2_SESSION_TTL=1h
APP_2_AUTO_RENEW=false

# Continue with APP_3_, APP_4_, etc.
```

### Per-App Configuration Parameters

| Parameter      | Description                                                                                      | Default        | Required |
|----------------|--------------------------------------------------------------------------------------------------|----------------|----------|
| `hostname`     | Hostname to match for this app (used for routing)                                               | None           | Yes      |
| `upstream_url` | URL of the upstream service for this app                                                         | None           | Yes      |
| `secret_path`  | Secret path prefix clients must visit to unlock access                                          | `/secret_path` | No       |
| `allow_ips`    | Comma-separated list of IP regex patterns (Go's RE2 syntax) to allow without the secret prefix  | ``             | No       |
| `session_ttl`  | Time after which an inactive client session will be invalidated                                 | `10m`          | No       |
| `auto_renew`   | Extend the session on every successful access                                                   | `true`         | No       |

### Global Configuration Parameters

| Variable         | Description                                                                                      | Default        |
|------------------|--------------------------------------------------------------------------------------------------|----------------|
| `LISTEN_ADDRESS` | IP:Port the proxy listens on. By default the proxy listens on all network interfaces            | `:8080`        |
| `REDIS_ADDRESS`  | Redis address                                                                                    | `redis:6379`   |
| `REDIS_PASSWORD` | Redis password                                                                                   | ``             |
| `LOG_LEVEL`      | Log level for the application (DEBUG, INFO, WARN, ERROR)                                        | `INFO`         |

---

## 🐳 Docker Deployment

The examples below show how to deploy `mithrandir` using Docker Compose, along with other applications in the same compose file.
However, Mithrandir can be run as a standalone service also.

### Multi-App Docker Compose Example

```yaml
services:
  # App 1: Immich
  immich:
    image: ghcr.io/immich-app/immich-server:latest
    ports:
      - "2283:3001"
    restart: always
    container_name: immich

  # App 2: NextCloud  
  nextcloud:
    image: nextcloud:latest
    ports:
      - "8080:80"
    restart: always
    container_name: nextcloud

  # App 3: IT Tools
  it-tools:
    image: 'corentinth/it-tools:latest'
    ports:
      - "8081:80"
    restart: always
    container_name: it-tools

  # Multi-App Proxy
  mithrandir:
    image: 'sudhanwadindorkar/mithrandir:latest'
    ports:
      - "80:8080"  # Public port
    environment:
      - REDIS_ADDRESS=redis:6379
      - LISTEN_ADDRESS=:8080
      - LOG_LEVEL=INFO
      # JSON Configuration for multiple apps
      - |
        APPS_CONFIG=[
          {
            "hostname": "immich.localhost",
            "secret_path": "/13b84d2a-faff-4b02-bef0-9f7898252659",
            "upstream_url": "http://immich:3001",
            "session_ttl": "24h",
            "auto_renew": "true",
            "allow_ips": "192.168.1.100"
          },
          {
            "hostname": "nextcloud.localhost",
            "secret_path": "/a1b2c3d4-e5f6-7890-abcd-ef1234567890",
            "upstream_url": "http://nextcloud:80",
            "session_ttl": "12h",
            "auto_renew": "true"
          },
          {
            "hostname": "tools.localhost",
            "secret_path": "/dev-tools-secret-xyz",
            "upstream_url": "http://it-tools:80",
            "session_ttl": "1h",
            "auto_renew": "false"
          }
        ]
    depends_on:
      - immich
      - nextcloud
      - it-tools
      - redis
    restart: always

  # Redis for session storage
  redis:
    image: redis:latest
    restart: always
    volumes:
      - ./redis-data:/data
```

### Alternative: Environment Variables Configuration

```yaml
services:
  # ... (same app services as above)
  
  mithrandir:
    image: 'sudhanwadindorkar/mithrandir:latest'
    ports:
      - "80:8080"
    environment:
      - REDIS_ADDRESS=redis:6379
      - LOG_LEVEL=INFO
      # App 1
      - APP_1_HOSTNAME=immich.localhost
      - APP_1_UPSTREAM_URL=http://immich:3001
      - APP_1_SECRET_PATH=/13b84d2a-faff-4b02-bef0-9f7898252659
      - APP_1_SESSION_TTL=24h
      - APP_1_AUTO_RENEW=true
      - APP_1_ALLOW_IPS=192.168.1.100
      # App 2
      - APP_2_HOSTNAME=nextcloud.localhost
      - APP_2_UPSTREAM_URL=http://nextcloud:80
      - APP_2_SECRET_PATH=/a1b2c3d4-e5f6-7890-abcd-ef1234567890
      - APP_2_SESSION_TTL=12h
      - APP_2_AUTO_RENEW=true
      # App 3
      - APP_3_HOSTNAME=tools.localhost
      - APP_3_UPSTREAM_URL=http://it-tools:80
      - APP_3_SECRET_PATH=/dev-tools-secret-xyz
      - APP_3_SESSION_TTL=1h
      - APP_3_AUTO_RENEW=false
    depends_on:
      - immich
      - nextcloud
      - it-tools
      - redis
    restart: always
```

### Build & Run

```bash
docker compose up -d
```

### Access Your Apps

1. **Immich**: Visit `http://immich.localhost/13b84d2a-faff-4b02-bef0-9f7898252659`
2. **NextCloud**: Visit `http://nextcloud.localhost/a1b2c3d4-e5f6-7890-abcd-ef1234567890`
3. **IT Tools**: Visit `http://tools.localhost/dev-tools-secret-xyz`

After visiting the secret path, your IP is allowed and you can access each app normally for the configured session duration.

---

## 🧱 Development

### 1. Install Go

Make sure you have Go 1.21+ installed:

```bash
go version
```

### 2. Build Locally

```bash
go mod tidy
go build -o mithrandir main.go
```

### 3. Test Multi-App Configuration

For local testing, you can use environment variables:

```bash
# Set up multi-app configuration
export REDIS_ADDRESS=localhost:6379
export APP_1_HOSTNAME=app1.local
export APP_1_UPSTREAM_URL=http://localhost:3001
export APP_1_SECRET_PATH=/secret1
export APP_2_HOSTNAME=app2.local
export APP_2_UPSTREAM_URL=http://localhost:3002
export APP_2_SECRET_PATH=/secret2

# Start Redis (if not running)
docker run -d -p 6379:6379 redis:latest

# Run the proxy
./mithrandir
```

Add to `/etc/hosts` for testing:
```
127.0.0.1 app1.local
127.0.0.1 app2.local
```

### 4. Test with JSON Configuration

```bash
export REDIS_ADDRESS=localhost:6379
export APPS_CONFIG='[
  {
    "hostname": "test1.local",
    "upstream_url": "http://localhost:8001",
    "secret_path": "/test-secret-1",
    "session_ttl": "5m"
  },
  {
    "hostname": "test2.local", 
    "upstream_url": "http://localhost:8002",
    "secret_path": "/test-secret-2",
    "session_ttl": "10m"
  }
]'

./mithrandir
```

### 5. Build Docker Image

```bash
docker build -t mithrandir .
```

---

## 🧼 Logging

The proxy uses structured logging with configurable log levels. Set the `LOG_LEVEL` environment variable to control log verbosity:

- **ERROR**: Only errors (Redis failures, configuration errors)
- **WARN**: Warnings and errors
- **INFO**: General information, access denials, and startup info (default)
- **DEBUG**: Detailed request tracing and forwarding information

### Log Level Behaviors

- **Successful requests**: Logged at `DEBUG` level only
- **Access denials**: Logged at `INFO` level  
- **Error conditions**: Logged at `ERROR` level
- **Startup and configuration**: Logged at `INFO` level

The structured logs include relevant context fields like hostname, IP, method, path, upstream URL, and error details.

### Example Log Output

With structured logging, the output format has changed to include structured fields:

```
time=2024-01-15T10:30:00.000Z level=INFO msg="Multi-app proxy started" listen_address=:8080 redis_address=redis:6379 app_count=3
time=2024-01-15T10:30:00.001Z level=INFO msg="App configured" hostname=immich.localhost upstream=http://immich:3001 secret_path=/13b84d2a-faff-4b02-bef0-9f7898252659 session_ttl=24h0m0s
time=2024-01-15T10:30:00.002Z level=INFO msg="App configured" hostname=nextcloud.localhost upstream=http://nextcloud:80 secret_path=/a1b2c3d4-e5f6-7890-abcd-ef1234567890 session_ttl=12h0m0s
time=2024-01-15T10:30:00.003Z level=INFO msg="App configured" hostname=tools.localhost upstream=http://it-tools:80 secret_path=/dev-tools-secret-xyz session_ttl=1h0m0s
time=2024-01-15T10:30:15.123Z level=DEBUG msg="Incoming request" hostname=immich.localhost ip=192.168.1.100 method=GET path=/13b84d2a-faff-4b02-bef0-9f7898252659
time=2024-01-15T10:30:15.124Z level=INFO msg="Access granted via secret path" hostname=immich.localhost ip=192.168.1.100
time=2024-01-15T10:30:15.125Z level=DEBUG msg="Browser detected, redirecting after secret path access" hostname=immich.localhost ip=192.168.1.100 user_agent="Mozilla/5.0" redirect_path=/
time=2024-01-15T10:30:16.200Z level=DEBUG msg="Incoming request" hostname=immich.localhost ip=192.168.1.100 method=GET path=/
time=2024-01-15T10:30:16.201Z level=DEBUG msg="Forwarding request to upstream" hostname=immich.localhost ip=192.168.1.100 method=GET path=/ upstream=http://immich:3001
time=2024-01-15T10:30:20.500Z level=INFO msg="No app configured for hostname" hostname=unknown.localhost
```

---

## 🧩 Future Enhancements

- Prometheus metrics endpoint
- Web UI for managing multi-app configurations
- Notifications (webhooks, email alerts)
- Device tracking (for NAT use-cases)
- Cookie-based session tracking (in addition to IP-based)
- Rate limiting per app
- Geographic access restrictions

---

## 📬 Questions or Contributions?

PRs welcome! Reach out if you have suggestions to improve this tool 🙏.
