package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log/slog"
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
	logger       *slog.Logger
)

func main() {
	// Setup logging
	setupLogging()

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
		logger.Error("Failed to connect to Redis", "address", redisAddress, "error", err)
		os.Exit(1)
	}

	logger.Info("Multi-app proxy started", "listen_address", listenAddress, "redis_address", redisAddress, "app_count", len(apps))
	for hostname, app := range apps {
		logger.Info("App configured", "hostname", hostname, "upstream", app.UpstreamURL.String(), "secret_path", app.SecretPathPrefix, "session_ttl", app.SessionTTL.String())
	}

	handler := http.HandlerFunc(handleRequest)
	logger.Info("Starting HTTP server", "address", listenAddress)
	if err := http.ListenAndServe(listenAddress, handler); err != nil {
		logger.Error("HTTP server failed", "error", err)
		os.Exit(1)
	}
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
		logger.Error("No app configurations found. Set APPS_CONFIG (JSON) or use numbered environment variables (APP_1_HOSTNAME, etc.)")
		os.Exit(1)
	}
}

func loadAppsFromJSON(jsonConfig string) {
	var appConfigs []map[string]string
	if err := json.Unmarshal([]byte(jsonConfig), &appConfigs); err != nil {
		logger.Error("Failed to parse APPS_CONFIG JSON", "error", err)
		os.Exit(1)
	}

	for i, config := range appConfigs {
		app, err := parseAppConfig(config)
		if err != nil {
			logger.Error("Invalid app config in JSON", "index", i, "error", err)
			os.Exit(1)
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
			logger.Error("Invalid app config", "prefix", prefix, "error", err)
			os.Exit(1)
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

func setupLogging() {
	logLevel := strings.ToUpper(getenv("LOG_LEVEL", "INFO"))

	var level slog.Level
	switch logLevel {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	logger = slog.New(handler)
	slog.SetDefault(logger)
}

func handleRequest(responseWriter http.ResponseWriter, request *http.Request) {
	hostname := request.Host
	// Remove port from hostname if present
	if colonIndex := strings.Index(hostname, ":"); colonIndex != -1 {
		hostname = hostname[:colonIndex]
	}

	app, exists := apps[hostname]
	if !exists {
		logger.Info("No app configured for hostname", "hostname", hostname)
		http.Error(responseWriter, "Not Found", http.StatusNotFound)
		return
	}

	ip := clientIP(request)
	logger.Debug("Incoming request", "hostname", hostname, "ip", ip, "method", request.Method, "path", request.URL.Path)

	// Check if IP matches any of the app's allowIPs patterns
	isAllowedIP := false
	for _, regex := range app.AllowIPs {
		if regex.MatchString(ip) {
			logger.Debug("IP matches allow list, forwarding directly", "hostname", hostname, "ip", ip)
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
				logger.Error("Redis error while setting session", "hostname", hostname, "error", err)
				http.Error(responseWriter, "Internal error", http.StatusInternalServerError)
				return
			}
			logger.Info("Access granted via secret path", "hostname", hostname, "ip", ip)

			// Check if the request comes from a browser
			userAgent := request.Header.Get("User-Agent")
			if browserRegex.MatchString(userAgent) && !strings.Contains(strings.ToLower(userAgent), "android") {
				// Remove the secretPathPrefix from the URL and redirect
				newPath := strings.TrimPrefix(request.URL.Path, app.SecretPathPrefix)
				if newPath == "" {
					newPath = "/"
				}
				logger.Debug("Browser detected, redirecting after secret path access", "hostname", hostname, "ip", ip, "user_agent", userAgent, "redirect_path", newPath)
				http.Redirect(responseWriter, request, newPath, http.StatusFound)
				return
			}
		}

		// If the IP is not in cache and not accessing the secret path, deny access
		if ipExistsCheckError != nil || ipExistsInCache == 0 {
			if ipExistsCheckError != nil {
				logger.Error("Redis error while checking session", "hostname", hostname, "ip", ip, "error", ipExistsCheckError)
			} else {
				logger.Info("Access denied - no valid session", "hostname", hostname, "ip", ip, "path", request.URL.Path)
			}
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

	logger.Debug("Forwarding request to upstream", "hostname", hostname, "ip", ip, "method", request.Method, "path", request.URL.Path, "upstream", app.UpstreamURL.String())

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
