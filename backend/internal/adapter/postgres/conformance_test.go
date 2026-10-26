package postgres_test

import (
	"testing"

	"reading-tracker/backend/internal/adapter/postgres"
	"reading-tracker/backend/internal/adapter/storetest"
	"reading-tracker/backend/internal/platform/pgtest"
)

func TestConformanceIdentity(t *testing.T) {
	storetest.RunIdentity(t, func(t *testing.T) storetest.IdentityStore { return postgres.New(pgtest.New(t).Pool) })
}
