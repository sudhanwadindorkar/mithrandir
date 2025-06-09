![Logo](images/mithrandir-wide.png)

## 🧙You shall not pass!

`mithrandir` is a lightweight, high-performance reverse proxy written in Go that restricts access to a backend service 
unless the client first accesses a predefined **secret path** (e.g., `/13b84d2a-faff-4b02-bef0-9f7898252659`). 
Once accessed, the proxy allows the client’s IP to continue accessing the backend for a configurable time. 
It can be deployed as a sidecar along with your main container or separately. 
It uses Redis for multi-instance or clustered deployment support.

This proxy can be useful for:
- Adding another layer of protection for self-hosted apps like **Immich** or **NextCloud**
- Avoiding the use of basic auth or complex login pages
- Maintaining compatibility with mobile apps (no auth headers/IDP)
- Lightweight access gating for dev/staging services

---

## 🚀 Features

- 🔐 Restricts backend access unless secret path is visited
- ⏳ TTL-based session tracking (default: 10 minutes)
- 🧠 Redis-backed session store
- ⚙️ Configurable entirely via environment variables
- 🐳 Dockerized for easy deployment as a sidecar
- 📄 Logs all traffic and access attempts

---

## 🧪 How It Works

1. User accesses: `https://yourdomain.com/secret_path`
2. Their IP is recorded as "allowed" (stored in Redis)
3. For the next N minutes, all requests from their IP are allowed
4. Requests from other IPs are blocked with a `403 Forbidden`

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

### Environment Variables

| Variable         | Description                                                                                      | Default        |
|------------------|--------------------------------------------------------------------------------------------------|----------------|
| `LISTEN_ADDRESS` | IP:Port the proxy listens on. By default the proxy listens on all network interfaces             | `:8080`        |
| `UPSTREAM_URL`   | URL of the upstream service                                                                      | None           |
| `SECRET_PATH`    | Secret path prefix clients must visit to unlock access                                           | `/secret_path` |
| `REDIS_ADDRESS`  | Redis address                                                                                    | `redis:6379`   |
| `REDIS_PASSWORD` | Redis password                                                                                   | ``             |
| `AUTO_RENEW`     | Extend the session on every successful access                                                    | true           |
| `SESSION_TTL`    | The time after which an inactive client session will be invalidated                              | `10m`          |
| `ALLOW_IPS`      | A comma separated list of IP regex patterns (Go's RE2 syntax) to allow without the secret prefix | ``             |

---

## 🐳 Docker Deployment

### 1. Add to Your Docker Compose

```yaml
services:
    it-tools:
        image: 'corentinth/it-tools:latest'
        ports:
            - '11923:80'
        restart: always
        container_name: it-tools
    proxy:
      image: 'sudhanwadindorkar/mithrandir:latest'
      ports:
        - "11924:8080"
      environment:
        - UPSTREAM_URL=http://it-tools
        - SECRET_PATH=/13b84d2a-faff-4b02-bef0-9f7898252659
        - SESSION_TTL=24h
        - REDIS_ADDRESS=redis:6379
        - AUTO_RENEW=true
        - ALLOW_IPS=192.168.1.20
      depends_on:
        - it-tools
        - redis
    
    redis:
      image: redis:latest
      restart: always
      volumes:
        - ./redis-data:/data
```

### 2. Build & Run

```bash
docker compose up -d
```

Then visit:

```
http://localhost:11924/13b84d2a-faff-4b02-bef0-9f7898252659
```

After that, your IP is allowed and you can access the backend without re-authenticating for the session duration.

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
./mithrandir
```

### 3. Build Docker Image

```bash
docker build -t mithrandir .
```

---

## 🧼 Logging

The proxy logs:
- Allowed/denied IPs
- Requests to the secret path
- Backend connection failures

---

## 🧩 Future Enhancements

- Prometheus metrics endpoint
- Multi-app support and UI
- Notifications
- Device tracking (for NAT use-cases)

---

## 📬 Questions or Contributions?

PRs welcome! Reach out if you have suggestions to improve this tool 🙏.
