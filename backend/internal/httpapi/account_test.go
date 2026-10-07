package httpapi_test

import "testing"

// T17 AC: logout, then any call with the same token → 401.
func TestLogoutRevokesToken(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	if rec := env.do("POST", "/v1/auth/logout", nil, tok); rec.Code != 204 {
		t.Fatalf("logout %d %s", rec.Code, rec.Body)
	}
	if rec := env.do("GET", "/v1/me", nil, tok); rec.Code != 401 {
		t.Fatalf("after logout %d", rec.Code)
	}
}
