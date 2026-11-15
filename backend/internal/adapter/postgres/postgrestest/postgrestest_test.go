package postgrestest_test

import (
	"context"
	"strings"
	"testing"

	"reading-tracker/backend/internal/adapter/postgres/postgrestest"
	"reading-tracker/backend/internal/adapter/storetest"
	"reading-tracker/backend/internal/domain"
)

func TestCountAndDump(t *testing.T) {
	e := postgrestest.New(t)
	storetest.Must(t, e.Store.CreateUser(context.Background(), "u1",
		domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(storetest.TestNow)}))
	if n := e.Count(t, "users", "id = $1", "u1"); n != 1 {
		t.Fatalf("Count users = %d, want 1", n)
	}
	for _, line := range e.Dump(t) {
		if strings.Contains(line, `"u1"`) {
			return
		}
	}
	t.Fatal("no Dump line contains u1")
}
