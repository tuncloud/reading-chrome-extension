// Package config loads and validates the process environment (spec §3.5).
package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // C15: the distroless image ships no zoneinfo.

	"reading-tracker/backend/internal/platform/crypto"
)

// NotionVersion is pinned (C10); any other NOTION_VERSION fails fast.
const NotionVersion = "2025-09-03"

// Role selects which variables are required.
type Role int

const (
	RoleAPI Role = iota
	RoleWorker
)

// Config is the parsed environment shared by cmd/server and cmd/poller.
type Config struct {
	AppEnv              string
	DatabaseURL         string        // DATABASE_URL
	QueueDelay          time.Duration // QUEUE_DELAY_SECONDS, defaults to the debounce bucket
	NotionClientID      string
	NotionClientSecret  string
	NotionVersion       string
	NotionAPIBase       string // NOTION_API_BASE, default https://api.notion.com (tests/local point it at a fake)
	PublicBaseURL       string // PUBLIC_BASE_URL, may be empty (C14)
	Keys                *crypto.Keyring
	DeviceTokenPepper   []byte
	AllowedExtensionIDs map[string]bool
	DebounceBucket      time.Duration
	Public              PublicConfig
}

// Load reads the environment through getenv and fails on the first invalid value,
// listing every missing variable at once.
func Load(role Role, getenv func(string) string) (*Config, error) {
	var missing []string
	req := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	c := &Config{AppEnv: req("APP_ENV")}
	// DATABASE_URL is read right after APP_ENV so a multi-variable "missing env"
	// message keeps a stable order.
	c.DatabaseURL = req("DATABASE_URL")
	c.NotionClientID = req("NOTION_CLIENT_ID")
	c.NotionClientSecret = req("NOTION_CLIENT_SECRET")
	c.NotionVersion = req("NOTION_VERSION")
	c.NotionAPIBase = strings.TrimRight(strings.TrimSpace(getenv("NOTION_API_BASE")), "/")
	c.PublicBaseURL = strings.TrimRight(strings.TrimSpace(getenv("PUBLIC_BASE_URL")), "/")
	keys := req("TOKEN_ENC_KEYS")
	var pepper, extIDs string
	if role == RoleAPI {
		pepper = req("DEVICE_TOKEN_PEPPER")
		extIDs = req("ALLOWED_EXTENSION_IDS")
	}
	// Both roles need the bucket: QUEUE_DELAY_SECONDS defaults from it.
	bucket := req("DEBOUNCE_BUCKET_SECONDS")
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: missing env %s", strings.Join(missing, ", "))
	}
	if c.NotionVersion != NotionVersion {
		return nil, fmt.Errorf("config: NOTION_VERSION must be %s, got %q", NotionVersion, c.NotionVersion)
	}
	if c.NotionAPIBase == "" {
		c.NotionAPIBase = "https://api.notion.com"
	}
	var err error
	if c.Keys, err = crypto.ParseKeyring(keys); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if role == RoleAPI {
		c.DeviceTokenPepper, err = base64.StdEncoding.DecodeString(pepper)
		if err != nil || len(c.DeviceTokenPepper) < 32 {
			return nil, fmt.Errorf("config: DEVICE_TOKEN_PEPPER must be base64 of >= 32 bytes")
		}
		c.AllowedExtensionIDs = map[string]bool{}
		for _, id := range strings.Split(extIDs, ",") {
			if id = strings.TrimSpace(id); id != "" {
				c.AllowedExtensionIDs[id] = true
			}
		}
		if len(c.AllowedExtensionIDs) == 0 {
			return nil, fmt.Errorf("config: ALLOWED_EXTENSION_IDS has no ids")
		}
	}
	secs, err := strconv.Atoi(bucket)
	if err != nil || secs < 1 || secs > 300 {
		return nil, fmt.Errorf("config: DEBOUNCE_BUCKET_SECONDS must be 1..300 (C5), got %q", bucket)
	}
	c.DebounceBucket = time.Duration(secs) * time.Second
	delay := c.DebounceBucket
	if v := strings.TrimSpace(getenv("QUEUE_DELAY_SECONDS")); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: QUEUE_DELAY_SECONDS must be an integer, got %q", v)
		}
		delay = time.Duration(secs) * time.Second
	}
	if delay < c.DebounceBucket {
		return nil, fmt.Errorf("config: QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5), got %v", delay)
	}
	if delay > 900*time.Second {
		return nil, fmt.Errorf("config: QUEUE_DELAY_SECONDS must be <= 900, got %v", delay)
	}
	c.QueueDelay = delay
	if c.Public, err = LoadPublic(getenv("TRACKING_CONFIG_JSON")); err != nil {
		return nil, err
	}
	return c, nil
}
