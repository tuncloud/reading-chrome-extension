package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func apiEnv() map[string]string {
	return map[string]string{
		"APP_ENV":                 "test",
		"DATABASE_URL":            "postgres://u:p@h:5432/d",
		"NOTION_CLIENT_ID":        "cid",
		"NOTION_CLIENT_SECRET":    "csecret",
		"NOTION_VERSION":          "2025-09-03",
		"TOKEN_ENC_KEYS":          "v1:" + base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"DEVICE_TOKEN_PEPPER":     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"ALLOWED_EXTENSION_IDS":   "abcdefghijklmnopabcdefghijklmnop, ponmlkjihgfedcbaponmlkjihgfedcba",
		"DEBOUNCE_BUCKET_SECONDS": "120",
	}
}

func getenv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// workerEnv is apiEnv without the API-only variables.
func workerEnv() map[string]string {
	env := apiEnv()
	for _, k := range []string{"DEVICE_TOKEN_PEPPER", "ALLOWED_EXTENSION_IDS"} {
		delete(env, k)
	}
	return env
}

// A cluster ConfigMap applied before this change still sets STORE=postgres, and an old
// .env.local may say dynamo.
func TestStoreEnvIsIgnored(t *testing.T) {
	for _, v := range []string{"", "postgres", "dynamo", "anything"} {
		env := apiEnv()
		env["STORE"] = v
		env["TABLE_NAME"] = "t"
		env["QUEUE_URL"] = "q"
		c, err := Load(RoleAPI, getenv(env))
		if err != nil {
			t.Fatalf("STORE=%q: %v", v, err)
		}
		if c.DatabaseURL != apiEnv()["DATABASE_URL"] {
			t.Fatalf("STORE=%q: DatabaseURL = %q", v, c.DatabaseURL)
		}
	}
}

func TestLoadDoesNotNeedTableOrQueue(t *testing.T) {
	c, err := Load(RoleAPI, getenv(apiEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL == "" {
		t.Fatal("want DatabaseURL set")
	}
	if c.QueueDelay != 120*time.Second {
		t.Fatalf("want QueueDelay to default to bucket (120s), got %v", c.QueueDelay)
	}
}

func TestLoadNeedsDatabaseURL(t *testing.T) {
	for _, role := range []Role{RoleAPI, RoleWorker} {
		env := apiEnv()
		delete(env, "DATABASE_URL")
		_, err := Load(role, getenv(env))
		if err == nil || !strings.Contains(err.Error(), "missing env") || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("role %d: want missing env DATABASE_URL, got %v", role, err)
		}
	}
}

func TestMissingEnvListsInOriginalOrder(t *testing.T) {
	env := apiEnv()
	for _, k := range []string{"APP_ENV", "DATABASE_URL", "NOTION_CLIENT_ID"} {
		delete(env, k)
	}
	_, err := Load(RoleAPI, getenv(env))
	want := "config: missing env APP_ENV, DATABASE_URL, NOTION_CLIENT_ID"
	if err == nil || err.Error() != want {
		t.Fatalf("want error %q, got %v", want, err)
	}
}

func TestQueueDelayMustCoverBucket(t *testing.T) {
	env := apiEnv()
	env["QUEUE_DELAY_SECONDS"] = "60"
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5)") {
		t.Fatalf("want error containing %q, got %v", "QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5)", err)
	}

	env2 := apiEnv()
	env2["QUEUE_DELAY_SECONDS"] = "901"
	_, err = Load(RoleAPI, getenv(env2))
	if err == nil || !strings.Contains(err.Error(), "<= 900") {
		t.Fatalf("want error containing %q, got %v", "<= 900", err)
	}
}

func TestQueueDelayMustBeInteger(t *testing.T) {
	env := apiEnv()
	env["QUEUE_DELAY_SECONDS"] = "abc"
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "QUEUE_DELAY_SECONDS") {
		t.Fatalf("want error containing %q, got %v", "QUEUE_DELAY_SECONDS", err)
	}
}

func TestWorkerReadsBucket(t *testing.T) {
	c, err := Load(RoleWorker, getenv(workerEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.DebounceBucket != 120*time.Second {
		t.Fatalf("want DebounceBucket=120s, got %v", c.DebounceBucket)
	}
}

func TestWorkerNeedsBucket(t *testing.T) {
	env := workerEnv()
	delete(env, "DEBOUNCE_BUCKET_SECONDS")
	_, err := Load(RoleWorker, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "missing env") || !strings.Contains(err.Error(), "DEBOUNCE_BUCKET_SECONDS") {
		t.Fatalf("want missing env DEBOUNCE_BUCKET_SECONDS, got %v", err)
	}
}

func TestLoadAPIHappyPath(t *testing.T) {
	c, err := Load(RoleAPI, getenv(apiEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.DebounceBucket != 120*time.Second || len(c.AllowedExtensionIDs) != 2 || !c.AllowedExtensionIDs["abcdefghijklmnopabcdefghijklmnop"] {
		t.Fatalf("unexpected config %+v", c)
	}
	if c.NotionAPIBase != "https://api.notion.com" || c.Keys.CurrentVersion() != "v1" {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.Public.Tracking.IdleSeconds != 60 || len(c.Public.FreediumHosts) != 2 || c.Public.Version != 1 {
		t.Fatalf("embedded defaults not loaded: %+v", c.Public)
	}
}

func TestLoadWorkerDoesNotNeedAPIOnlyVars(t *testing.T) {
	if _, err := Load(RoleWorker, getenv(workerEnv())); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsBucketOver300(t *testing.T) {
	env := apiEnv()
	env["DEBOUNCE_BUCKET_SECONDS"] = "301"
	if _, err := Load(RoleAPI, getenv(env)); err == nil {
		t.Fatal("bucket > 300 must fail (C5)")
	}
}

func TestLoadRejectsOtherNotionVersion(t *testing.T) {
	env := apiEnv()
	env["NOTION_VERSION"] = "2022-06-28"
	if _, err := Load(RoleAPI, getenv(env)); err == nil {
		t.Fatal("NOTION_VERSION other than 2025-09-03 must fail (C10)")
	}
}

func TestLoadRejectsShortPepper(t *testing.T) {
	env := apiEnv()
	env["DEVICE_TOKEN_PEPPER"] = base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := Load(RoleAPI, getenv(env)); err == nil {
		t.Fatal("pepper < 32 bytes must fail")
	}
}

func TestTrackingOverrideMerges(t *testing.T) {
	env := apiEnv()
	env["TRACKING_CONFIG_JSON"] = `{"tracking":{"idle_seconds":90},"freedium_hosts":["new-mirror.example"]}`
	c, err := Load(RoleAPI, getenv(env))
	if err != nil {
		t.Fatal(err)
	}
	tr := c.Public.Tracking
	if tr.IdleSeconds != 90 || tr.SegmentSeconds != 60 || c.Public.FreediumHosts[0] != "new-mirror.example" {
		t.Fatalf("override not merged: %+v %v", tr, c.Public.FreediumHosts)
	}
}

func TestTrackingOverrideRejectsUnknownField(t *testing.T) {
	if _, err := LoadPublic(`{"tracking":{"idle_secs":90}}`); err == nil {
		t.Fatal("typo in override must fail")
	}
}

func TestTrackingOverrideRejectsZeroWordsPerMinute(t *testing.T) {
	if _, err := LoadPublic(`{"tracking":{"words_per_minute":0}}`); err == nil {
		t.Fatal("words_per_minute 0 would divide by zero in the worker")
	}
}

func TestTimezoneDataEmbedded(t *testing.T) {
	if _, err := time.LoadLocation("Asia/Ho_Chi_Minh"); err != nil {
		t.Fatalf("time/tzdata must be embedded (C15): %v", err)
	}
}
