package main

import (
	"context"
	"github.com/redis/go-redis/v9"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	ctx              = context.Background()
	redisClient      *redis.Client
	ttl              time.Duration
	secretPathPrefix string
	autoRenew        bool
	upstreamURL      *url.URL
	browserRegex     = regexp.MustCompile(`(?i)Mozilla|Chrome|Safari|Edge|Opera|Firefox`)
)

func main() {
	// Load environment config
	upstreamUrlConfig := getenv("UPSTREAM_URL", "")
	listenAddress := getenv("LISTEN_ADDRESS", ":8080")
	secretPathPrefix = getenv("SECRET_PATH", "/secret_path")
	ttlConfig := getenv("SESSION_TTL", "10m")
	redisAddress := getenv("REDIS_ADDRESS", "redis:6379")
	redisPassword := getenv("REDIS_PASSWORD", "")
	autoRenew = getenv("AUTO_RENEW", "false") == "true"

	var err error
	ttl, err = time.ParseDuration(ttlConfig)
	if err != nil {
		log.Fatalf("Invalid SESSION_TTL: %v", err)
	}

	upstreamURL, err = url.Parse(upstreamUrlConfig)
	if err != nil {
		log.Fatalf("Invalid UPSTREAM_URL: %v", err)
	}

	// Redis client
	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisAddress,
		Password: redisPassword,
		DB:       0,
	})

	_, err = redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis at %s: %v", redisAddress, err)
	}

	log.Printf("Proxy started:")
	log.Printf("  Listening on     %s", listenAddress)
	log.Printf("  Upstream URL      %s", upstreamURL)
	log.Printf("  Secret path prefix      %s", secretPathPrefix)
	log.Printf("  Session TTL      %s", ttl)
	log.Printf("  Redis Address       %s", redisAddress)
	log.Printf("  Auto-Renew       %t", autoRenew)

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		log.Printf("Request from %s %s %s", ip, r.Method, r.URL.Path)

		cacheKey := "ip:" + ip

		ipExistsInCache, ipExistsCheckError := redisClient.Exists(ctx, cacheKey).Result()

		// If the IP is not in cache and the request is to the secret path, allow access
		if ipExistsInCache == 0 && strings.HasPrefix(r.URL.Path, secretPathPrefix) {
			err := redisClient.Set(ctx, cacheKey, "1", ttl).Err()
			if err != nil {
				log.Printf("Redis error: %v", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			log.Printf("Access granted to %s via secret path", ip)
			// Check if the request comes from a browser
			userAgent := r.Header.Get("User-Agent")
			if browserRegex.MatchString(userAgent) {
				// Remove the secretPathPrefix from the URL and redirect
				newPath := strings.TrimPrefix(r.URL.Path, secretPathPrefix)
				if newPath == "" {
					newPath = "/"
				}
				log.Printf("Detected User-Agent %s. Redirecting %s to %s", userAgent, ip, newPath)
				http.Redirect(w, r, newPath, http.StatusFound) // 302 Found
				return
			}
		}

		// If the IP is not in cache and not accessing the secret path, deny access
		if ipExistsCheckError != nil || ipExistsInCache == 0 {
			log.Printf("Access denied to %s", ip)
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}

		// If auto-renew is enabled, renew the session TTL
		if autoRenew {
			_ = redisClient.Expire(ctx, cacheKey, ttl).Err() // Extend TTL
		}

		// Strip secretPathPrefix prefix from URL.Path and URL.RawPath
		r.URL.Path = strings.TrimPrefix(r.URL.Path, secretPathPrefix)
		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, secretPathPrefix)
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

	log.Fatal(http.ListenAndServe(listenAddress, handler))
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
