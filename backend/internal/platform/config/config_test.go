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
		"TABLE_NAME":              "reading-test",
		"QUEUE_URL":               "https://sqs.local/notion-sync-test.fifo",
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

// pgEnv builds a postgres-mode environment for role: no TABLE_NAME/QUEUE_URL,
// STORE=postgres, DATABASE_URL set.
func pgEnv(role Role) map[string]string {
	env := apiEnv()
	delete(env, "TABLE_NAME")
	delete(env, "QUEUE_URL")
	env["STORE"] = "postgres"
	env["DATABASE_URL"] = "postgres://u:p@h:5432/d"
	if role == RoleWorker {
		for _, k := range []string{"DEVICE_TOKEN_PEPPER", "ALLOWED_EXTENSION_IDS"} {
			delete(env, k)
		}
	}
	return env
}

func TestStoreDefaultsToDynamo(t *testing.T) {
	c, err := Load(RoleAPI, getenv(apiEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Store != "dynamo" {
		t.Fatalf("want Store=dynamo, got %q", c.Store)
	}
}

func TestPostgresModeDoesNotNeedTableOrQueue(t *testing.T) {
	c, err := Load(RoleAPI, getenv(pgEnv(RoleAPI)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Store != StorePostgres {
		t.Fatalf("want Store=postgres, got %q", c.Store)
	}
	if c.DatabaseURL == "" {
		t.Fatal("want DatabaseURL set")
	}
	if c.QueueDelay != 120*time.Second {
		t.Fatalf("want QueueDelay to default to bucket (120s), got %v", c.QueueDelay)
	}
}

func TestPostgresModeNeedsDatabaseURL(t *testing.T) {
	env := pgEnv(RoleAPI)
	delete(env, "DATABASE_URL")
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "missing env DATABASE_URL") {
		t.Fatalf("want error containing %q, got %v", "missing env DATABASE_URL", err)
	}
}

func TestDynamoModeStillNeedsTableAndQueue(t *testing.T) {
	env := apiEnv()
	delete(env, "TABLE_NAME")
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "TABLE_NAME") {
		t.Fatalf("want error naming TABLE_NAME, got %v", err)
	}
}

func TestMissingEnvListsInOriginalOrder(t *testing.T) {
	env := apiEnv()
	for _, k := range []string{"APP_ENV", "TABLE_NAME", "NOTION_CLIENT_ID"} {
		delete(env, k)
	}
	_, err := Load(RoleAPI, getenv(env))
	want := "config: missing env APP_ENV, TABLE_NAME, NOTION_CLIENT_ID"
	if err == nil || err.Error() != want {
		t.Fatalf("want error %q, got %v", want, err)
	}
}

func TestUnknownStoreRejected(t *testing.T) {
	env := apiEnv()
	env["STORE"] = "mysql"
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "STORE must be dynamo or postgres") {
		t.Fatalf("want error containing %q, got %v", "STORE must be dynamo or postgres", err)
	}
}

func TestQueueDelayMustCoverBucket(t *testing.T) {
	env := pgEnv(RoleAPI)
	env["QUEUE_DELAY_SECONDS"] = "60"
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5)") {
		t.Fatalf("want error containing %q, got %v", "QUEUE_DELAY_SECONDS must be >= DEBOUNCE_BUCKET_SECONDS (C5)", err)
	}

	env2 := pgEnv(RoleAPI)
	env2["QUEUE_DELAY_SECONDS"] = "901"
	_, err = Load(RoleAPI, getenv(env2))
	if err == nil || !strings.Contains(err.Error(), "<= 900") {
		t.Fatalf("want error containing %q, got %v", "<= 900", err)
	}
}

func TestQueueDelayMustBeInteger(t *testing.T) {
	env := pgEnv(RoleAPI)
	env["QUEUE_DELAY_SECONDS"] = "abc"
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "QUEUE_DELAY_SECONDS") {
		t.Fatalf("want error containing %q, got %v", "QUEUE_DELAY_SECONDS", err)
	}
}

func TestPostgresWorkerReadsBucket(t *testing.T) {
	env := pgEnv(RoleWorker)
	c, err := Load(RoleWorker, getenv(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.DebounceBucket != 120*time.Second {
		t.Fatalf("want DebounceBucket=120s, got %v", c.DebounceBucket)
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

func TestLoadMissingTableName(t *testing.T) {
	env := apiEnv()
	delete(env, "TABLE_NAME")
	_, err := Load(RoleAPI, getenv(env))
	if err == nil || !strings.Contains(err.Error(), "TABLE_NAME") {
		t.Fatalf("want error naming TABLE_NAME, got %v", err)
	}
}

func TestLoadWorkerDoesNotNeedAPIOnlyVars(t *testing.T) {
	env := apiEnv()
	for _, k := range []string{"DEVICE_TOKEN_PEPPER", "ALLOWED_EXTENSION_IDS", "DEBOUNCE_BUCKET_SECONDS"} {
		delete(env, k)
	}
	if _, err := Load(RoleWorker, getenv(env)); err != nil {
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
