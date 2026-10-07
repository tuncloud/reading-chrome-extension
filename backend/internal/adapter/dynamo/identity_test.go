package dynamo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

var (
	ctx     = context.Background()
	testNow = time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateUserAndResolveByNotionUser(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(testNow)}))

	uid, err := s.UserIDForNotionUser(ctx, "n1")
	must(t, err)
	p, err := s.GetProfile(ctx, uid)
	must(t, err)
	if uid != "u1" || p.Name != "Tuan Do" || p.ID != "u1" {
		t.Fatalf("got uid=%q profile=%+v", uid, p)
	}
	must(t, s.SetActiveInstallation(ctx, "u1", "bot-1"))
	p, _ = s.GetProfile(ctx, "u1")
	if p.ActiveInstallationID != "bot-1" {
		t.Fatalf("active installation not set: %+v", p)
	}
	if _, err := s.UserIDForNotionUser(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeviceLifecycle(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(testNow), LastSeenAt: domain.Timestamp(testNow)}))

	ref, err := s.GetDeviceToken(ctx, "h1")
	must(t, err)
	if ref.UserID != "u1" || ref.DeviceID != "d1" {
		t.Fatalf("bad token ref %+v", ref)
	}
	devs, err := s.ListDevices(ctx, "u1")
	must(t, err)
	if len(devs) != 1 || devs[0].ID != "d1" {
		t.Fatalf("ListDevices = %+v", devs)
	}

	must(t, s.RevokeDevice(ctx, "u1", "d1", "h1", testNow))
	if _, err := s.GetDeviceToken(ctx, "h1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token lookup must be gone after revoke, got %v", err)
	}
	d, err := s.GetDevice(ctx, "u1", "d1")
	must(t, err)
	if d.RevokedAt == "" {
		t.Fatal("revoked_at not set")
	}
}

func TestTouchDeviceWritesAtMostHourly(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(testNow), LastSeenAt: domain.Timestamp(testNow)}))

	must(t, s.TouchDevice(ctx, "u1", "d1", testNow.Add(10*time.Minute)))
	d, _ := s.GetDevice(ctx, "u1", "d1")
	if d.LastSeenAt != domain.Timestamp(testNow) {
		t.Fatalf("touched within the hour: %s", d.LastSeenAt)
	}
	must(t, s.TouchDevice(ctx, "u1", "d1", testNow.Add(61*time.Minute)))
	d, _ = s.GetDevice(ctx, "u1", "d1")
	if d.LastSeenAt != domain.Timestamp(testNow.Add(61*time.Minute)) {
		t.Fatalf("not touched after an hour: %s", d.LastSeenAt)
	}
}
