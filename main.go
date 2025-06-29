package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type AppConfig struct {
	Hostname         string
	SecretPathPrefix string
	UpstreamURL      *url.URL
	AllowIPs         []*regexp.Regexp
	SessionTTL       time.Duration
	AutoRenew        bool
}

var (
	ctx          = context.Background()
	redisClient  *redis.Client
	browserRegex = regexp.MustCompile(`(?i)Mozilla|Chrome|Safari|Edge|Opera|Firefox`)
	apps         map[string]*AppConfig
)

func main() {
	// Load environment config
	listenAddress := getenv("LISTEN_ADDRESS", ":8080")
	redisAddress := getenv("REDIS_ADDRESS", "redis:6379")
	redisPassword := getenv("REDIS_PASSWORD", "")

	// Load app configurations
	apps = make(map[string]*AppConfig)
	loadAppConfigurations()

	// Redis client
	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisAddress,
		Password: redisPassword,
		DB:       0,
	})

	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis at %s: %v", redisAddress, err)
	}

	log.Printf("Multi-app proxy started:")
	log.Printf("  Listening on: %s", listenAddress)
	log.Printf("  Redis Address: %s", redisAddress)
	log.Printf("  Configured apps: %d", len(apps))
	for hostname, app := range apps {
		log.Printf("    %s -> %s (secret: %s, ttl: %s)", hostname, app.UpstreamURL, app.SecretPathPrefix, app.SessionTTL)
	}

	handler := http.HandlerFunc(handleRequest)
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

func loadAppConfigurations() {
	// Check for JSON configuration first
	if jsonConfig := os.Getenv("APPS_CONFIG"); jsonConfig != "" {
		loadAppsFromJSON(jsonConfig)
		return
	}

	// Fall back to numbered environment variables
	loadAppsFromEnv()

	if len(apps) == 0 {
		log.Fatal("No app configurations found. Set APPS_CONFIG (JSON) or use numbered environment variables (APP_1_HOSTNAME, etc.)")
	}
}

func loadAppsFromJSON(jsonConfig string) {
	var appConfigs []map[string]string
	if err := json.Unmarshal([]byte(jsonConfig), &appConfigs); err != nil {
		log.Fatalf("Failed to parse APPS_CONFIG JSON: %v", err)
	}

	for i, config := range appConfigs {
		app, err := parseAppConfig(config)
		if err != nil {
			log.Fatalf("Invalid app config in JSON[%d]: %v", i, err)
		}
		apps[app.Hostname] = app
	}
}

func loadAppsFromEnv() {
	for i := 1; ; i++ {
		prefix := fmt.Sprintf("APP_%d_", i)
		hostname := os.Getenv(prefix + "HOSTNAME")
		if hostname == "" {
			break // No more apps
		}

		config := map[string]string{
			"hostname":     hostname,
			"secret_path":  getenv(prefix+"SECRET_PATH", "/secret_path"),
			"upstream_url": os.Getenv(prefix + "UPSTREAM_URL"),
			"allow_ips":    os.Getenv(prefix + "ALLOW_IPS"),
			"session_ttl":  getenv(prefix+"SESSION_TTL", "10m"),
			"auto_renew":   getenv(prefix+"AUTO_RENEW", "true"),
		}

		app, err := parseAppConfig(config)
		if err != nil {
			log.Fatalf("Invalid app config %s: %v", prefix, err)
		}
		apps[app.Hostname] = app
	}
}

func parseAppConfig(config map[string]string) (*AppConfig, error) {
	app := &AppConfig{
		Hostname:         config["hostname"],
		SecretPathPrefix: config["secret_path"],
	}

	if app.Hostname == "" {
		return nil, fmt.Errorf("hostname is required")
	}

	if config["upstream_url"] == "" {
		return nil, fmt.Errorf("upstream_url is required")
	}

	var err error
	app.UpstreamURL, err = url.Parse(config["upstream_url"])
	if err != nil {
		return nil, fmt.Errorf("invalid upstream_url: %v", err)
	}

	app.SessionTTL, err = time.ParseDuration(config["session_ttl"])
	if err != nil {
		return nil, fmt.Errorf("invalid session_ttl: %v", err)
	}

	app.AutoRenew, _ = strconv.ParseBool(config["auto_renew"])

	// Parse allowed IPs
	if allowIPsConfig := config["allow_ips"]; allowIPsConfig != "" {
		for _, pattern := range strings.Split(allowIPsConfig, ",") {
			escapedPattern := strings.ReplaceAll(strings.TrimSpace(pattern), ".", `\.`)
			regex, err := regexp.Compile(escapedPattern)
			if err != nil {
				return nil, fmt.Errorf("invalid IP regex pattern '%s': %v", pattern, err)
			}
			app.AllowIPs = append(app.AllowIPs, regex)
		}
	}

	return app, nil
}

func handleRequest(responseWriter http.ResponseWriter, request *http.Request) {
	hostname := request.Host
	// Remove port from hostname if present
	if colonIndex := strings.Index(hostname, ":"); colonIndex != -1 {
		hostname = hostname[:colonIndex]
	}

	app, exists := apps[hostname]
	if !exists {
		log.Printf("No app configured for hostname: %s", hostname)
		http.Error(responseWriter, "Not Found", http.StatusNotFound)
		return
	}

	ip := clientIP(request)
	log.Printf("[%s] Request from %s %s %s", hostname, ip, request.Method, request.URL.Path)

	// Check if IP matches any of the app's allowIPs patterns
	isAllowedIP := false
	for _, regex := range app.AllowIPs {
		if regex.MatchString(ip) {
			log.Printf("[%s] IP %s matches allow list. Forwarding directly to upstream.", hostname, ip)
			isAllowedIP = true
			break
		}
	}

	if !isAllowedIP {
		cacheKey := fmt.Sprintf("app:%s:ip:%s", hostname, ip)
		ipExistsInCache, ipExistsCheckError := redisClient.Exists(ctx, cacheKey).Result()

		// If the IP is not in cache and the request is to the secret path, allow access
		if ipExistsInCache == 0 && strings.HasPrefix(request.URL.Path, app.SecretPathPrefix) {
			err := redisClient.Set(ctx, cacheKey, "1", app.SessionTTL).Err()
			if err != nil {
				log.Printf("[%s] Redis error: %v", hostname, err)
				http.Error(responseWriter, "Internal error", http.StatusInternalServerError)
				return
			}
			log.Printf("[%s] Access granted to %s via secret path", hostname, ip)

			// Check if the request comes from a browser
			userAgent := request.Header.Get("User-Agent")
			if browserRegex.MatchString(userAgent) && !strings.Contains(strings.ToLower(userAgent), "android") {
				// Remove the secretPathPrefix from the URL and redirect
				newPath := strings.TrimPrefix(request.URL.Path, app.SecretPathPrefix)
				if newPath == "" {
					newPath = "/"
				}
				log.Printf("[%s] Detected User-Agent %s. Redirecting %s to %s", hostname, userAgent, ip, newPath)
				http.Redirect(responseWriter, request, newPath, http.StatusFound)
				return
			}
		}

		// If the IP is not in cache and not accessing the secret path, deny access
		if ipExistsCheckError != nil || ipExistsInCache == 0 {
			log.Printf("[%s] Access denied to %s", hostname, ip)
			http.Error(responseWriter, "Access denied", http.StatusForbidden)
			return
		}

		// If auto-renew is enabled, renew the session TTL
		if app.AutoRenew {
			_ = redisClient.Expire(ctx, cacheKey, app.SessionTTL).Err()
		}

		// Strip secretPathPrefix from URL.Path and URL.RawPath
		request.URL.Path = strings.TrimPrefix(request.URL.Path, app.SecretPathPrefix)
		if request.URL.RawPath != "" {
			request.URL.RawPath = strings.TrimPrefix(request.URL.RawPath, app.SecretPathPrefix)
		}
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
		if request.URL.RawPath == "" {
			request.URL.RawPath = request.URL.Path
		}
	}

	log.Printf("[%s] Forwarding request from %s %s %s", hostname, ip, request.Method, request.URL.Path)

	// Create a reverse proxy for this specific app
	proxy := httputil.NewSingleHostReverseProxy(app.UpstreamURL)
	proxy.ServeHTTP(responseWriter, request)
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
