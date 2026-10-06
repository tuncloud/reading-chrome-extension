package store_test

import (
	"errors"
	"testing"

	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/store/storetest"
)

func newInstallation() *store.Installation {
	return &store.Installation{UserID: "u1", WorkspaceID: "ws", WorkspaceName: "WS", Status: store.StatusNeedsSetup,
		SetupError: "template_not_found", AccessTokenEnc: "v1:a", RefreshTokenEnc: "v1:r", TokenVersion: 1,
		CreatedAt: store.Timestamp(testNow), UpdatedAt: store.Timestamp(testNow)}
}

func TestInstallationTokensVersionGuard(t *testing.T) {
	s := storetest.New(t).Store
	must(t, s.PutInstallation(ctx, "bot-1", newInstallation()))

	must(t, s.UpdateInstallationTokens(ctx, "bot-1", "v1:a2", "v1:r2", 1, testNow))
	inst, err := s.GetInstallation(ctx, "bot-1")
	must(t, err)
	if inst.TokenVersion != 2 || inst.AccessTokenEnc != "v1:a2" || inst.RefreshTokenEnc != "v1:r2" || inst.ID() != "bot-1" {
		t.Fatalf("tokens not rotated: %+v", inst)
	}
	if err := s.UpdateInstallationTokens(ctx, "bot-1", "v1:a3", "v1:r3", 1, testNow); !errors.Is(err, store.ErrConditionFailed) {
		t.Fatalf("stale token_version must fail (C7), got %v", err)
	}
}

func TestSaveInstallationDatabasesActivates(t *testing.T) {
	s := storetest.New(t).Store
	must(t, s.PutInstallation(ctx, "bot-1", newInstallation()))
	db := store.DBConfig{LibraryDBID: "ldb", LibraryDSID: "lds", LibraryURL: "https://notion.so/l", DailyDBID: "ddb", DailyDSID: "dds",
		Props: map[string]string{"title": "p-title", "day_date": "p-date"}}

	must(t, s.SaveInstallationDatabases(ctx, "bot-1", db, testNow))
	inst, _ := s.GetInstallation(ctx, "bot-1")
	if inst.Status != store.StatusActive || inst.SetupError != "" || inst.Props["title"] != "p-title" || !inst.HasDatabases() {
		t.Fatalf("not activated: %+v", inst)
	}

	must(t, s.SetInstallationStatus(ctx, "bot-1", store.StatusNeedsSetup, "database_missing:Reading Library", testNow))
	inst, _ = s.GetInstallation(ctx, "bot-1")
	if inst.Status != store.StatusNeedsSetup || inst.SetupError != "database_missing:Reading Library" {
		t.Fatalf("status not updated: %+v", inst)
	}
}

func TestUserInstallationPointers(t *testing.T) {
	s := storetest.New(t).Store
	must(t, s.PutUserInstallation(ctx, "u1", "bot-1", testNow))
	must(t, s.PutUserInstallation(ctx, "u1", "bot-1", testNow)) // idempotent
	must(t, s.PutUserInstallation(ctx, "u1", "bot-2", testNow))
	ids, err := s.ListUserInstallationIDs(ctx, "u1")
	must(t, err)
	if len(ids) != 2 || ids[0] != "bot-1" || ids[1] != "bot-2" {
		t.Fatalf("got %v", ids)
	}
}
