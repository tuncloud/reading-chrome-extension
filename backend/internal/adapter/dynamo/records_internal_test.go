package dynamo

import (
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"

	"reading-tracker/backend/internal/domain"
)

// Review focus 1: attribute names are the stored format (spec §3.1); items written
// before the refactor must keep loading.
func TestRecordAttributeNames(t *testing.T) {
	cases := map[string]struct {
		rec  any
		want []string
	}{
		"user": {newUserRecord("u", domain.User{NotionUserID: "n", Name: "x", Email: "e", ActiveInstallationID: "b", CreatedAt: "c", DeletedAt: "d"}),
			[]string{"pk", "sk", "notion_user_id", "name", "email", "active_installation_id", "created_at", "deleted_at"}},
		"device": {newDeviceRecord("u", "d", domain.Device{Name: "n", TokenHMAC: "h", CreatedAt: "c", LastSeenAt: "l", RevokedAt: "r"}),
			[]string{"pk", "sk", "name", "token_hmac", "created_at", "last_seen_at", "revoked_at"}},
		"devtoken": {deviceTokenRecord{PK: "p", SK: "s", UserID: "u", DeviceID: "d"},
			[]string{"pk", "sk", "user_id", "device_id"}},
		"oauthstate": {oauthStateRecord{PK: "p", SK: "s", ExtRedirect: "e", CodeChallenge: "c", TTL: 1},
			[]string{"pk", "sk", "ext_redirect", "code_challenge", "ttl"}},
		"authcode": {authCodeRecord{PK: "p", SK: "s", UserID: "u", InstallationID: "i", CodeChallenge: "c", ExtRedirect: "e", TTL: 1},
			[]string{"pk", "sk", "user_id", "installation_id", "code_challenge", "ext_redirect", "ttl"}},
		"installation": {newInstallationRecord("b", &domain.Installation{UserID: "u", WorkspaceID: "w", WorkspaceName: "n", WorkspaceIcon: "i",
			Status: "s", SetupError: "e", AccessTokenEnc: "a", RefreshTokenEnc: "r", TokenExpiresAt: "x", TokenVersion: 1,
			DuplicatedTemplateID: "t", LibraryDBID: "l", LibraryDSID: "ld", LibraryURL: "lu", DailyDBID: "d", DailyDSID: "dd",
			Props: map[string]string{"k": "v"}, CreatedAt: "c", UpdatedAt: "up"}),
			[]string{"pk", "sk", "user_id", "workspace_id", "workspace_name", "workspace_icon", "status", "setup_error",
				"access_token_enc", "refresh_token_enc", "token_expires_at", "token_version", "duplicated_template_id",
				"library_db_id", "library_ds_id", "library_url", "daily_db_id", "daily_ds_id", "props", "created_at", "updated_at"}},
		"article": {articleRecord{PK: "p", SK: "s", CanonicalURL: "u", Source: "s", Title: "t", Author: "a", SiteName: "n",
			WordCount: 1, PublishedAt: "p", FirstReadAt: "f", LastReadAt: "l", TotalSeconds: 1, Sessions: 1, MaxProgress: 1,
			Completed: true, Version: 1, NotionPageID: "np", NotionURL: "nu", NotionDataSourceID: "nd", NotionSyncedVersion: 1, NotionSyncedAt: "ns"},
			[]string{"pk", "sk", "canonical_url", "source", "title", "author", "site_name", "word_count", "published_at",
				"first_read_at", "last_read_at", "total_seconds", "sessions", "max_progress", "completed", "version",
				"notion_page_id", "notion_url", "notion_ds_id", "notion_synced_version", "notion_synced_at"}},
		"day": {dayRecord{PK: "p", SK: "s", Date: "d", Seconds: 1, ArticleHashes: []string{"h"}, Version: 1, NotionPageID: "np",
			NotionDataSourceID: "nd", NotionSyncedVersion: 1},
			[]string{"pk", "sk", "date", "seconds", "article_hashes", "version", "notion_page_id", "notion_ds_id", "notion_synced_version"}},
	}
	for name, c := range cases {
		av, err := attributevalue.MarshalMap(c.rec)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range av {
			got = append(got, k)
		}
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s attributes = %v, want %v", name, got, want)
		}
	}
}

func TestRecordsRoundTripIDs(t *testing.T) {
	if u := newUserRecord("u1", domain.User{}).toDomain(); u.ID != "u1" {
		t.Fatalf("user id %q", u.ID)
	}
	if d := newDeviceRecord("u1", "d1", domain.Device{}).toDomain(); d.ID != "d1" || d.UserID != "u1" {
		t.Fatalf("device %+v", d)
	}
	if i := newInstallationRecord("b1", &domain.Installation{}).toDomain(); i.ID != "b1" {
		t.Fatalf("installation id %q", i.ID)
	}
	if a := (articleRecord{SK: "ART#h1"}).toDomain(); a.Hash != "h1" {
		t.Fatalf("article hash %q", a.Hash)
	}
}
