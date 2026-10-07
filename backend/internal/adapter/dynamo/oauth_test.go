package dynamo_test

import (
	"errors"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

func TestOAuthStateConsumedOnce(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.PutOAuthState(ctx, "st1", domain.OAuthState{ExtRedirect: "https://x.chromiumapp.org/", CodeChallenge: "c"}, testNow.Add(10*time.Minute)))

	st, err := s.ConsumeOAuthState(ctx, "st1", testNow)
	must(t, err)
	if st.ExtRedirect != "https://x.chromiumapp.org/" || st.CodeChallenge != "c" {
		t.Fatalf("got %+v", st)
	}
	if _, err := s.ConsumeOAuthState(ctx, "st1", testNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second consume must fail, got %v", err)
	}
}

// Scenario 18: an expired item that DynamoDB has not reaped yet is still rejected (C12).
func TestExpiredAuthCodeRejected(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.PutAuthCode(ctx, "code1", domain.AuthCode{UserID: "u1", InstallationID: "bot-1", CodeChallenge: "c"}, testNow.Add(-time.Second)))
	if _, err := s.ConsumeAuthCode(ctx, "code1", testNow); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired code must be rejected, got %v", err)
	}
}

func TestAuthCodeRoundTrip(t *testing.T) {
	s := dynamotest.New(t).Store
	must(t, s.PutAuthCode(ctx, "code1", domain.AuthCode{UserID: "u1", InstallationID: "bot-1", CodeChallenge: "c", ExtRedirect: "e"}, testNow.Add(2*time.Minute)))
	ac, err := s.ConsumeAuthCode(ctx, "code1", testNow)
	must(t, err)
	if ac.UserID != "u1" || ac.InstallationID != "bot-1" || ac.ExtRedirect != "e" {
		t.Fatalf("got %+v", ac)
	}
}
