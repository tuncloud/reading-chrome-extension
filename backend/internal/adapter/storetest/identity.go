package storetest

import (
	"errors"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
)

var identityChecks = []struct {
	name string
	run  func(t *testing.T, s IdentityStore)
}{
	{"CreateUserAndResolveByNotionUser", createUserAndResolveByNotionUser},
	{"DeviceLifecycle", deviceLifecycle},
	{"TouchDeviceWritesAtMostHourly", touchDeviceWritesAtMostHourly},
	{"InstallationTokensVersionGuard", installationTokensVersionGuard},
	{"SaveInstallationDatabasesActivates", saveInstallationDatabasesActivates},
	{"UserInstallationPointers", userInstallationPointers},
	{"OAuthStateConsumedOnce", oauthStateConsumedOnce},
	{"ExpiredAuthCodeRejected", expiredAuthCodeRejected},
	{"AuthCodeRoundTrip", authCodeRoundTrip},
	{"DeleteNotionUserLinkOnlyForOwner", deleteNotionUserLinkOnlyForOwner},
}

func createUserAndResolveByNotionUser(t *testing.T, s IdentityStore) {
	Must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(TestNow)}))

	uid, err := s.UserIDForNotionUser(ctx, "n1")
	Must(t, err)
	p, err := s.GetProfile(ctx, uid)
	Must(t, err)
	if uid != "u1" || p.Name != "Tuan Do" || p.ID != "u1" {
		t.Fatalf("got uid=%q profile=%+v", uid, p)
	}
	Must(t, s.SetActiveInstallation(ctx, "u1", "bot-1"))
	p, _ = s.GetProfile(ctx, "u1")
	if p.ActiveInstallationID != "bot-1" {
		t.Fatalf("active installation not set: %+v", p)
	}
	if _, err := s.UserIDForNotionUser(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func deviceLifecycle(t *testing.T, s IdentityStore) {
	Must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(TestNow), LastSeenAt: domain.Timestamp(TestNow)}))

	ref, err := s.GetDeviceToken(ctx, "h1")
	Must(t, err)
	if ref.UserID != "u1" || ref.DeviceID != "d1" {
		t.Fatalf("bad token ref %+v", ref)
	}
	devs, err := s.ListDevices(ctx, "u1")
	Must(t, err)
	if len(devs) != 1 || devs[0].ID != "d1" {
		t.Fatalf("ListDevices = %+v", devs)
	}

	Must(t, s.RevokeDevice(ctx, "u1", "d1", "h1", TestNow))
	if _, err := s.GetDeviceToken(ctx, "h1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token lookup must be gone after revoke, got %v", err)
	}
	d, err := s.GetDevice(ctx, "u1", "d1")
	Must(t, err)
	if d.RevokedAt == "" {
		t.Fatal("revoked_at not set")
	}
}

func touchDeviceWritesAtMostHourly(t *testing.T, s IdentityStore) {
	Must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(TestNow), LastSeenAt: domain.Timestamp(TestNow)}))

	Must(t, s.TouchDevice(ctx, "u1", "d1", TestNow.Add(10*time.Minute)))
	d, _ := s.GetDevice(ctx, "u1", "d1")
	if d.LastSeenAt != domain.Timestamp(TestNow) {
		t.Fatalf("touched within the hour: %s", d.LastSeenAt)
	}
	Must(t, s.TouchDevice(ctx, "u1", "d1", TestNow.Add(61*time.Minute)))
	d, _ = s.GetDevice(ctx, "u1", "d1")
	if d.LastSeenAt != domain.Timestamp(TestNow.Add(61*time.Minute)) {
		t.Fatalf("not touched after an hour: %s", d.LastSeenAt)
	}
}

func newInstallation() *domain.Installation {
	return &domain.Installation{ID: "bot-1", UserID: "u1", WorkspaceID: "ws", WorkspaceName: "WS", Status: domain.StatusNeedsSetup,
		SetupError: "template_not_found", AccessTokenEnc: "v1:a", RefreshTokenEnc: "v1:r", TokenVersion: 1,
		CreatedAt: domain.Timestamp(TestNow), UpdatedAt: domain.Timestamp(TestNow)}
}

func installationTokensVersionGuard(t *testing.T, s IdentityStore) {
	Must(t, s.PutInstallation(ctx, "bot-1", newInstallation()))

	Must(t, s.UpdateInstallationTokens(ctx, "bot-1", "v1:a2", "v1:r2", 1, TestNow))
	inst, err := s.GetInstallation(ctx, "bot-1")
	Must(t, err)
	if inst.TokenVersion != 2 || inst.AccessTokenEnc != "v1:a2" || inst.RefreshTokenEnc != "v1:r2" || inst.ID != "bot-1" {
		t.Fatalf("tokens not rotated: %+v", inst)
	}
	if err := s.UpdateInstallationTokens(ctx, "bot-1", "v1:a3", "v1:r3", 1, TestNow); !errors.Is(err, domain.ErrConditionFailed) {
		t.Fatalf("stale token_version must fail (C7), got %v", err)
	}
}

func saveInstallationDatabasesActivates(t *testing.T, s IdentityStore) {
	Must(t, s.PutInstallation(ctx, "bot-1", newInstallation()))
	db := domain.DBConfig{LibraryDBID: "ldb", LibraryDSID: "lds", LibraryURL: "https://notion.so/l", DailyDBID: "ddb", DailyDSID: "dds",
		Props: map[string]string{"title": "p-title", "day_date": "p-date"}}

	Must(t, s.SaveInstallationDatabases(ctx, "bot-1", db, TestNow))
	inst, _ := s.GetInstallation(ctx, "bot-1")
	if inst.Status != domain.StatusActive || inst.SetupError != "" || inst.Props["title"] != "p-title" || !inst.HasDatabases() {
		t.Fatalf("not activated: %+v", inst)
	}

	Must(t, s.SetInstallationStatus(ctx, "bot-1", domain.StatusNeedsSetup, "database_missing:Reading Library", TestNow))
	inst, _ = s.GetInstallation(ctx, "bot-1")
	if inst.Status != domain.StatusNeedsSetup || inst.SetupError != "database_missing:Reading Library" {
		t.Fatalf("status not updated: %+v", inst)
	}
}

func userInstallationPointers(t *testing.T, s IdentityStore) {
	Must(t, s.PutUserInstallation(ctx, "u1", "bot-1", TestNow))
	Must(t, s.PutUserInstallation(ctx, "u1", "bot-1", TestNow)) // idempotent
	Must(t, s.PutUserInstallation(ctx, "u1", "bot-2", TestNow))
	ids, err := s.ListUserInstallationIDs(ctx, "u1")
	Must(t, err)
	if len(ids) != 2 || ids[0] != "bot-1" || ids[1] != "bot-2" {
		t.Fatalf("got %v", ids)
	}
}

func oauthStateConsumedOnce(t *testing.T, s IdentityStore) {
	Must(t, s.PutOAuthState(ctx, "st1", domain.OAuthState{ExtRedirect: "https://x.chromiumapp.org/", CodeChallenge: "c"}, TestNow.Add(10*time.Minute)))

	st, err := s.ConsumeOAuthState(ctx, "st1", TestNow)
	Must(t, err)
	if st.ExtRedirect != "https://x.chromiumapp.org/" || st.CodeChallenge != "c" {
		t.Fatalf("got %+v", st)
	}
	if _, err := s.ConsumeOAuthState(ctx, "st1", TestNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second consume must fail, got %v", err)
	}
}

// Scenario 18: an expired item that DynamoDB has not reaped yet is still rejected (C12).
func expiredAuthCodeRejected(t *testing.T, s IdentityStore) {
	Must(t, s.PutAuthCode(ctx, "code1", domain.AuthCode{UserID: "u1", InstallationID: "bot-1", CodeChallenge: "c"}, TestNow.Add(-time.Second)))
	if _, err := s.ConsumeAuthCode(ctx, "code1", TestNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired code must be rejected, got %v", err)
	}
}

func authCodeRoundTrip(t *testing.T, s IdentityStore) {
	Must(t, s.PutAuthCode(ctx, "code1", domain.AuthCode{UserID: "u1", InstallationID: "bot-1", CodeChallenge: "c", ExtRedirect: "e"}, TestNow.Add(2*time.Minute)))
	ac, err := s.ConsumeAuthCode(ctx, "code1", TestNow)
	Must(t, err)
	if ac.UserID != "u1" || ac.InstallationID != "bot-1" || ac.ExtRedirect != "e" {
		t.Fatalf("got %+v", ac)
	}
}

func deleteNotionUserLinkOnlyForOwner(t *testing.T, s IdentityStore) {
	Must(t, s.CreateUser(ctx, "u2", domain.User{NotionUserID: "n1", Name: "new owner", CreatedAt: domain.Timestamp(TestNow)}))
	Must(t, s.DeleteNotionUserLink(ctx, "n1", "u1")) // stale owner: no-op
	if uid, err := s.UserIDForNotionUser(ctx, "n1"); err != nil || uid != "u2" {
		t.Fatalf("link of another user deleted: %q %v", uid, err)
	}
	Must(t, s.DeleteNotionUserLink(ctx, "n1", "u2"))
	if _, err := s.UserIDForNotionUser(ctx, "n1"); err == nil {
		t.Fatal("link not deleted")
	}
}
