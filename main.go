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
	allowIPs         []*regexp.Regexp
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
	allowIPsConfig := getenv("ALLOW_IPS", "")

	var err error
	ttl, err = time.ParseDuration(ttlConfig)
	if err != nil {
		log.Fatalf("Invalid SESSION_TTL: %v", err)
	}

	upstreamURL, err = url.Parse(upstreamUrlConfig)
	if err != nil {
		log.Fatalf("Invalid UPSTREAM_URL: %v", err)
	}

	if allowIPsConfig != "" {
		for _, pattern := range strings.Split(allowIPsConfig, ",") {
			escapedPattern := strings.ReplaceAll(strings.TrimSpace(pattern), ".", `\.`)
			regex, err := regexp.Compile(escapedPattern)
			if err != nil {
				log.Fatalf("Invalid IP regex pattern: %v", err)
			}
			allowIPs = append(allowIPs, regex)
		}
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
	log.Printf("  Allow IPS          %s", allowIPs)

	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		ip := clientIP(request)
		log.Printf("Request from %s %s %s", ip, request.Method, request.URL.Path)
		isAllowedIP := false
		// Check if IP matches any of the allowIPs patterns
		for _, regex := range allowIPs {
			if regex.MatchString(ip) {
				log.Printf("IP %s matches allow list. Forwarding directly to upstream.", ip)
				isAllowedIP = true
				break
			}
		}
		if !isAllowedIP {
			cacheKey := "ip:" + ip
			ipExistsInCache, ipExistsCheckError := redisClient.Exists(ctx, cacheKey).Result()
			// If the IP is not in cache and the request is to the secret path, allow access
			if ipExistsInCache == 0 && strings.HasPrefix(request.URL.Path, secretPathPrefix) {
				err := redisClient.Set(ctx, cacheKey, "1", ttl).Err()
				if err != nil {
					log.Printf("Redis error: %v", err)
					http.Error(responseWriter, "Internal error", http.StatusInternalServerError)
					return
				}
				log.Printf("Access granted to %s via secret path", ip)
				// Check if the request comes from a browser
				userAgent := request.Header.Get("User-Agent")
				if browserRegex.MatchString(userAgent) && !strings.Contains(strings.ToLower(userAgent), "android") {
					// Remove the secretPathPrefix from the URL and redirect
					newPath := strings.TrimPrefix(request.URL.Path, secretPathPrefix)
					if newPath == "" {
						newPath = "/"
					}
					log.Printf("Detected User-Agent %s. Redirecting %s to %s", userAgent, ip, newPath)
					http.Redirect(responseWriter, request, newPath, http.StatusFound) // 302 Found
					return
				}
			}

			// If the IP is not in cache and not accessing the secret path, deny access
			if ipExistsCheckError != nil || ipExistsInCache == 0 {
				log.Printf("Access denied to %s", ip)
				http.Error(responseWriter, "Access denied", http.StatusForbidden)
				return
			}

			// If auto-renew is enabled, renew the session TTL
			if autoRenew {
				_ = redisClient.Expire(ctx, cacheKey, ttl).Err() // Extend TTL
			}

			// Strip secretPathPrefix prefix from URL.Path and URL.RawPath
			request.URL.Path = strings.TrimPrefix(request.URL.Path, secretPathPrefix)
			if request.URL.RawPath != "" {
				request.URL.RawPath = strings.TrimPrefix(request.URL.RawPath, secretPathPrefix)
			}
			if request.URL.Path == "" {
				request.URL.Path = "/"
			}
			if request.URL.RawPath == "" {
				request.URL.RawPath = request.URL.Path
			}
		}
		log.Printf("Forwarding request from %s %s %s", ip, request.Method, request.URL.Path)
		proxy.ServeHTTP(responseWriter, request)
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
