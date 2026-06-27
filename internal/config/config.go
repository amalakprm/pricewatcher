package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	FeedURL           string        // PRICEWATCHER_FEED_URL (GAS JSON endpoint)
	AppriseURL        string        // APPRISE_URL, default "http://localhost:8000/notify/apprise"
	CloakBrowserCDP   string        // CLOAKBROWSER_CDP, default "http://192.168.1.9:9222"
	DBPath            string        // DB_PATH, default "/app/data/pricewatcher.db"
	WebPort           string        // WEB_PORT, default ":8420"
	CronSchedule      string        // CRON_SCHEDULE, default "0 3,12 * * *"
	AlertCooldownHrs  int           // ALERT_COOLDOWN_HOURS, default 20
	HTTPTimeout       time.Duration // HTTP_TIMEOUT_SEC (seconds), default 15s
	CDPTimeout        time.Duration // CDP_TIMEOUT_SEC, default 30s
	MaxHTTPConcurrent int           // MAX_HTTP_CONCURRENCY, default 5
	LegacyAlertFile   string        // LEGACY_ALERT_FILE for one-time migration
}

func LoadConfig() *Config {
	cfg := &Config{
		FeedURL:           os.Getenv("PRICEWATCHER_FEED_URL"),
		AppriseURL:        getEnv("APPRISE_URL", "http://localhost:8000/notify/apprise"),
		CloakBrowserCDP:   getEnv("CLOAKBROWSER_CDP", "http://192.168.1.9:9222"),
		DBPath:            getEnv("DB_PATH", "/app/data/pricewatcher.db"),
		WebPort:           getEnv("WEB_PORT", ":8420"),
		CronSchedule:      getEnv("CRON_SCHEDULE", "0 3,12 * * *"),
		AlertCooldownHrs:  getEnvInt("ALERT_COOLDOWN_HOURS", 20),
		HTTPTimeout:       time.Duration(getEnvInt("HTTP_TIMEOUT_SEC", 15)) * time.Second,
		CDPTimeout:        time.Duration(getEnvInt("CDP_TIMEOUT_SEC", 30)) * time.Second,
		MaxHTTPConcurrent: getEnvInt("MAX_HTTP_CONCURRENCY", 5),
		LegacyAlertFile:   os.Getenv("LEGACY_ALERT_FILE"),
	}
	return cfg
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
