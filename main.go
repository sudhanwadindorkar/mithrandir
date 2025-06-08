package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ctx        = context.Background()
	rdb        *redis.Client
	ttl        time.Duration
	secretPath string
	autoRenew  bool
	backendURL *url.URL
)

func main() {
	// Load environment config
	backendStr := getenv("BACKEND_URL", "")
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	secretPath = getenv("SECRET_PATH", "/secret_path")
	ttlStr := getenv("SESSION_TTL", "10m")
	redisAddr := getenv("REDIS_ADDR", "redis:6379")
	redisPassword := getenv("REDIS_PASSWORD", "")
	autoRenew = getenv("AUTO_RENEW", "false") == "true"

	var err error
	ttl, err = time.ParseDuration(ttlStr)
	if err != nil {
		log.Fatalf("Invalid SESSION_TTL: %v", err)
	}

	backendURL, err = url.Parse(backendStr)
	if err != nil {
		log.Fatalf("Invalid BACKEND_URL: %v", err)
	}

	// Redis client
	rdb = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
	})

	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis at %s: %v", redisAddr, err)
	}

	log.Printf("Proxy started:")
	log.Printf("  Listening on     %s", listenAddr)
	log.Printf("  Backend URL      %s", backendURL)
	log.Printf("  Secret path      %s", secretPath)
	log.Printf("  Session TTL      %s", ttl)
	log.Printf("  Redis Addr       %s", redisAddr)
	log.Printf("  Auto-Renew       %t", autoRenew)

	proxy := httputil.NewSingleHostReverseProxy(backendURL)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		log.Printf("Request from %s %s %s", ip, r.Method, r.URL.Path)

		key := "ip:" + ip

		ipExistsInCache, ipExistsCheckError := rdb.Exists(ctx, key).Result()

		// If the IP is not in cache and the request is to the secret path, allow access
		if ipExistsInCache == 0 && r.URL.Path == secretPath {
			err := rdb.Set(ctx, key, "1", ttl).Err()
			if err != nil {
				log.Printf("Redis error: %v", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			log.Printf("Access granted to %s via secret path", ip)
			http.Redirect(w, r, "/", http.StatusFound) // 302 Found
			return
		}

		// If the IP is not in cache and not accessing the secret path, deny access
		if ipExistsCheckError != nil || ipExistsInCache == 0 {
			log.Printf("Access denied to %s", ip)
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}

		// If auto-renew is enabled, renew the session TTL
		if autoRenew {
			_ = rdb.Expire(ctx, key, ttl).Err() // Extend TTL
		}

		// Strip secretPath prefix from URL.Path and URL.RawPath
		r.URL.Path = strings.TrimPrefix(r.URL.Path, secretPath)
		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, secretPath)
		}
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		if r.URL.RawPath == "" {
			r.URL.RawPath = r.URL.Path
		}

		log.Printf("Forwarding request from %s %s %s", ip, r.Method, r.URL.Path)

		proxy.ServeHTTP(w, r)
	})

	log.Fatal(http.ListenAndServe(listenAddr, handler))
}

func clientIP(r *http.Request) string {
	headers := []string{
		"CF-Connecting-IP",    // Cloudflare
		"True-Client-IP",      // Akamai
		"X-Real-IP",           // Common
		"X-Forwarded-For",     // Common
		"X-Cluster-Client-IP", // Common
		"Fastly-Client-IP",    // Fastly
		"Forwarded",           // RFC 7239
	}

	for _, header := range headers {
		if ip := r.Header.Get(header); ip != "" {
			return strings.TrimSpace(strings.Split(ip, ",")[0])
		}
	}

	// Fallback to RemoteAddr
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
