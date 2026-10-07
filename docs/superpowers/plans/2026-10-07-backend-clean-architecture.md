# Backend Clean-Architecture Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `backend/` into clean architecture (domain → use cases → adapters, with a composition root) without changing externally visible behaviour, and fix the two parked defects R1 and R2 along the way.

**Architecture:** Dependencies point inward only, and an architecture test enforces this.
- `internal/domain` holds entities and rules and imports only the standard library.
- `internal/usecase/*` holds application services. They depend on interfaces in `internal/usecase/port`, never on adapters or frameworks.
- `internal/adapter/{dynamo,notion,queue,httpapi}` implement those ports or drive the use cases.
- `internal/platform/{crypto,config,textutil}` are shared libraries.
- `internal/wire` composes everything for `cmd/{api,worker,local}`.

**Tech Stack:** Go 1.26, Echo v4, aws-lambda-go, aws-sdk-go-v2 (DynamoDB, SQS), DynamoDB Local via Podman, `go/parser` for the architecture test.

**Spec:** [`docs/reading-tracker-agent-spec.md`](../../reading-tracker-agent-spec.md) is still the behaviour authority. The previous plan [`2026-10-06-backend.md`](2026-10-06-backend.md) and its execution ledger define the current behaviour that this refactor must keep. The target design was approved in conversation on 2026-10-07 and is reproduced under "Design" below.

## Global Constraints

- **Behaviour is preserved.** Every HTTP route, status code, error code, JSON body, SQS message body, DynamoDB key and attribute name, and Notion request stays byte-for-byte compatible. Existing DynamoDB items must still be readable.
- **The suite is green after every task.** `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`: gofmt prints nothing and every package is `ok`. DynamoDB Local runs on :8000 (`make ddb-up`, via Podman, never Docker).
- **Existing tests are moved, not weakened.** A test may change package, imports, identifiers and constructors, but its assertions keep their meaning. Any assertion that has to change must be listed in the task report with a reason.
- **Domain:** standard library only. No struct tags, no AWS, Echo, Lambda or Notion types.
- **Use cases:** may import `domain`, `usecase/...`, `platform/crypto`, `platform/textutil` and `github.com/oklog/ulid/v2`. They must not import `platform/config`, any `adapter`, `wire`, Echo, AWS or Lambda.
- **Adapters:** may import `domain`, `usecase/...` and `platform/...`. They must not import another adapter (an adapter's own subpackages such as `notion/notiontest` are fine) or `wire`.
- **Platform:** may import only `platform/...` and third-party code.
- All spec constraints from the previous plan still bind: C1, C2, C4–C9, C12, C13, C18–C22. In short:
  - user-owned Notion properties are never written;
  - every second is counted exactly once;
  - Notion properties are addressed by stored id;
  - only the worker refreshes tokens and writes pages;
  - logs never contain tokens or query strings;
  - the first failed message fails the rest of its batch;
  - Notion calls are paced at ≥ 350 ms in the worker.
- **Module path:** `reading-tracker/backend`.
- **Tooling:** use `perl -pi -e` for in-place rewrites, because macOS `sed` lacks `\b`. For imports, run `go run golang.org/x/tools/cmd/goimports@v0.34.0 -w <files>`.

## Design (approved)

```
backend/internal/
  domain/            entities + rules (User, Device, Installation, DBConfig, Article, Day, SegmentWrite,
                     SyncPolicy.IsCompleted, URLHash, Timestamp, SameID, RoundMinutes, SetupError, errors)
  usecase/
    port/            interfaces: Users, Devices, OAuthGrants, Installations, Reading, DataPurger,
                     NotionAuth, NotionWorkspace, NotionPages, SyncQueue, Sealer, Job, NotionError, RetryAfterError
    apperr/          application error kinds the HTTP adapter maps to spec §2.0 codes
    auth/            authenticate device, exchange code → device token, logout
    connect/         OAuth start + callback (resolve user, upsert installation)
    setup/           installation token, search pages, set up databases, Activator (resync + retry/fallback)
    readsync/        push validation + exactly-once aggregation (was syncsvc)
    library/         article status
    account/         me, delete account
    notionsync/      article / day / purge jobs, token refresh session, error → ack / retry / back-off
  adapter/
    dynamo/          was store; private tagged records ↔ domain; throttle tagging; DataPurger
      dynamotest/    was store/storetest
    notion/          was notion client + Gateway (auth, workspace: template + provisioning, pages: all Notion JSON)
      notiontest/    fake Notion
    queue/           SQS/Local/Recorder senders, SyncQueue (dedup policy), Consumer (SQS batch → job handler)
    httpapi/         Echo: routing, DTOs, auth middleware, error mapping — calls use cases only
  platform/          crypto, config, textutil
  wire/              composition (API, worker, local) used by cmd/* and integration tests
  archtest/          dependency-rule test
  e2e/               end-to-end test
```

The design note says `adapter/lambda`. During planning the SQS consumer moved into `adapter/queue` instead, because one adapter owning the SQS wire format for both producing and consuming avoids an adapter-to-adapter import.

## Review Focus

These are the five regressions a refactor of this kind is most likely to cause where no existing test would catch it, most likely first:

1. **Stored-data compatibility.** A record's DynamoDB attribute names drift during the move to private records, so items written before the refactor stop loading. Covered by `TestRecordAttributeNames` (Task 2).
2. **Throttle mapping lost.** A DynamoDB throttle anywhere on the API path must still return 503 `capacity_exceeded` once errors no longer carry SDK types up to HTTP. Covered by `TestThrottledReadIs503` (Task 7).
3. **Notion error classification drift.** 429/529, 5xx, 409, 401, page-gone, data-source-missing and poison 400 must keep the same ack/retry/back-off outcome once translation moves into `adapter/notion`. Covered by `TestTranslateKinds` (Task 3) plus the ported worker tests (Task 5).
4. **R1, a stale page in a surviving old database.** Notion would 400 on the old property ids, so the item must be re-created in the current database rather than acked as poison. Covered by `TestArticleInOldDatabaseIsRecreatedBeforePatch` and `TestDayInOldDatabaseIsRecreatedBeforePatch` (Task 5).
5. **R2, resync swallowed by dedup.** A resync sent in the same debounce bucket as the last push must still be delivered. Covered by `TestResyncIDsNeverCollideWithPushIDs` (Task 4) and `TestResyncAfterPushInSameBucketIsDelivered` (Task 6).

---

### Task 1: Platform packages, domain package, architecture test

**Files:**
- Move: `internal/crypto` → `internal/platform/crypto`, `internal/config` → `internal/platform/config`, `internal/textutil` → `internal/platform/textutil`
- Create: `internal/domain/doc.go`, `identity.go`, `installation.go`, `reading.go`, `rules.go`, `errors.go`
- Test: `internal/domain/rules_test.go`, `internal/archtest/arch_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - Domain entities:
    - `domain.User{ID, NotionUserID, Name, Email, ActiveInstallationID, CreatedAt, DeletedAt string}`, with `Deleted() bool`
    - `domain.Device{ID, UserID, Name, TokenHMAC, CreatedAt, LastSeenAt, RevokedAt string}`, with `Revoked() bool`
    - `domain.DeviceToken{UserID, DeviceID}`
    - `domain.OAuthState{ExtRedirect, CodeChallenge}`
    - `domain.AuthCode{UserID, InstallationID, CodeChallenge, ExtRedirect}`
    - `domain.DBConfig{LibraryDBID, LibraryDSID, LibraryURL, DailyDBID, DailyDSID string; Props map[string]string}`
    - `domain.Installation` has the same fields as today's `store.Installation` minus `PK`/`SK`, plus `ID string`. Its methods are `HasDatabases()` and `ApplyDatabases(DBConfig)`.
    - `domain.Article` has today's fields minus `PK`/`SK`, plus `Hash string` and `NotionDataSourceID string`.
    - `domain.Day` has today's fields minus `PK`/`SK`, plus `NotionDataSourceID string`.
    - `domain.ArticleMeta` and `domain.SegmentWrite` are unchanged.
    - `domain.PushOutcome` has the values `PushAccepted`, `PushDuplicate` and `PushRetry`.
  - Domain constants: `domain.StatusActive`, `StatusNeedsSetup`, `StatusRevoked` and `SetupErrResyncFailed`.
  - Domain rules:
    - `domain.URLHash(string) string`
    - `domain.Timestamp(time.Time) string`
    - `domain.SameID(a, b string) bool`
    - `domain.RoundMinutes(int64) int64`
    - `domain.SyncPolicy{MinCreateSeconds int; CompleteProgress, CompleteTimeRatio float64; WordsPerMinute int}`, with `IsCompleted(Article) bool`
  - Domain errors:
    - `domain.ErrNotFound`, `domain.ErrConditionFailed`, `domain.ErrThrottled`
    - `domain.SetupError{Reason string}` and `domain.SetupReason(error) string`
  - Import paths: `reading-tracker/backend/internal/platform/{crypto,config,textutil}`.

- [ ] **Step 1: Move the platform packages**

```bash
cd backend
mkdir -p internal/platform
git mv internal/crypto internal/platform/crypto
git mv internal/config internal/platform/config
git mv internal/textutil internal/platform/textutil
grep -rlE 'reading-tracker/backend/internal/(crypto|config|textutil)"' --include='*.go' . \
  | xargs perl -pi -e 's#reading-tracker/backend/internal/(crypto|config|textutil)"#reading-tracker/backend/internal/platform/$1"#g'
go build ./... && go vet ./...
```

Expected: the build and vet pass, since only import paths changed.

- [ ] **Step 2: Write the failing domain and architecture tests**

`backend/internal/domain/rules_test.go`:

```go
package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsCompleted(t *testing.T) {
	p := SyncPolicy{CompleteProgress: 0.9, CompleteTimeRatio: 0.3, WordsPerMinute: 230}
	cases := []struct {
		a    Article
		want bool
	}{
		{Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 100}, true}, // 100 ≥ 0.3 × 260.9
		{Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 50}, false}, // skimmed
		{Article{MaxProgress: 0.95, WordCount: 0, TotalSeconds: 5}, true},      // unknown length
		{Article{MaxProgress: 0.5, WordCount: 0, TotalSeconds: 999}, false},
		{Article{MaxProgress: 0.1, Completed: true}, true}, // sticky
	}
	for i, c := range cases {
		if got := p.IsCompleted(c.a); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestURLHash(t *testing.T) {
	h := URLHash("https://medium.com/p/8f3c2a1b9d4e")
	if len(h) != 32 || h != URLHash("https://medium.com/p/8f3c2a1b9d4e") || h == URLHash("https://medium.com/p/other") {
		t.Fatalf("hash %q", h)
	}
}

func TestTimestampIsUTCSeconds(t *testing.T) {
	ts := Timestamp(time.Date(2026, 10, 6, 10, 1, 5, 999, time.FixedZone("ICT", 7*3600)))
	if ts != "2026-10-06T03:01:05Z" {
		t.Fatalf("got %s", ts)
	}
}

func TestSameIDIgnoresDashesAndCase(t *testing.T) {
	if !SameID("1F2E-ab", "1f2eAB") || SameID("1f2e", "1f2f") {
		t.Fatal("SameID")
	}
}

func TestRoundMinutes(t *testing.T) {
	for secs, want := range map[int64]int64{0: 0, 29: 0, 30: 1, 89: 1, 90: 2, 150: 3} {
		if got := RoundMinutes(secs); got != want {
			t.Errorf("RoundMinutes(%d) = %d, want %d", secs, got, want)
		}
	}
}

func TestSetupReason(t *testing.T) {
	if r := SetupReason(fmt.Errorf("wrap: %w", &SetupError{Reason: "property_missing:Progress"})); r != "property_missing:Progress" {
		t.Fatalf("got %q", r)
	}
	if r := SetupReason(errors.New("boom")); r != "notion_error" {
		t.Fatalf("got %q", r)
	}
}

func TestInstallationDatabases(t *testing.T) {
	var i Installation
	if i.HasDatabases() {
		t.Fatal("empty installation has no databases")
	}
	i.ApplyDatabases(DBConfig{LibraryDSID: "l", DailyDSID: "d", Props: map[string]string{"title": "t"}})
	if !i.HasDatabases() {
		t.Fatal("databases not applied")
	}
}
```

`backend/internal/archtest/arch_test.go`:

```go
// Package archtest enforces the clean-architecture dependency rule on non-test code.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const module = "reading-tracker/backend"

// packageImports maps "internal/<dir>" to the imports of its non-test .go files.
func packageImports(t *testing.T) map[string][]string {
	t.Helper()
	root := ".." // backend/internal
	pkgs := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "internal/" + filepath.ToSlash(rel)
		if _, ok := pkgs[pkg]; !ok {
			pkgs[pkg] = nil
		}
		for _, imp := range f.Imports {
			pkgs[pkg] = append(pkgs[pkg], strings.Trim(imp.Path.Value, `"`))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func under(p, prefix string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }

func isStdlib(imp string) bool { return !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".") }

// adapterRoot returns "internal/adapter/<name>" for any package inside that adapter.
func adapterRoot(pkg string) string {
	parts := strings.SplitN(pkg, "/", 4)
	if len(parts) < 3 {
		return pkg
	}
	return strings.Join(parts[:3], "/")
}

// violation explains why pkg may not import imp, or returns "".
func violation(pkg, imp string) string {
	target, internal := strings.CutPrefix(imp, module+"/")
	switch {
	case under(pkg, "internal/domain"):
		if internal && !under(target, "internal/domain") {
			return "domain may import only domain"
		}
		if !internal && !isStdlib(imp) {
			return "domain may import only the standard library"
		}
	case under(pkg, "internal/usecase"):
		if internal && !(under(target, "internal/domain") || under(target, "internal/usecase") ||
			under(target, "internal/platform/crypto") || under(target, "internal/platform/textutil")) {
			return "use cases may import only domain, usecase and platform/{crypto,textutil}"
		}
		if !internal && !isStdlib(imp) && imp != "github.com/oklog/ulid/v2" {
			return "use cases may not import frameworks or SDKs"
		}
	case under(pkg, "internal/adapter"):
		if internal && under(target, "internal/adapter") && adapterRoot(target) != adapterRoot(pkg) {
			return "an adapter may not import another adapter"
		}
		if internal && under(target, "internal/wire") {
			return "adapters may not import wire"
		}
	case under(pkg, "internal/platform"):
		if internal && !under(target, "internal/platform") {
			return "platform may import only platform"
		}
	}
	return ""
}

func TestDependencyRule(t *testing.T) {
	for pkg, imps := range packageImports(t) {
		for _, imp := range imps {
			if v := violation(pkg, imp); v != "" {
				t.Errorf("%s imports %s: %s", pkg, imp, v)
			}
		}
	}
}

func TestViolationRules(t *testing.T) {
	cases := []struct {
		pkg, imp string
		bad      bool
	}{
		{"internal/domain", "time", false},
		{"internal/domain", "github.com/aws/aws-sdk-go-v2/aws", true},
		{"internal/domain", module + "/internal/platform/crypto", true},
		{"internal/usecase/auth", module + "/internal/domain", false},
		{"internal/usecase/auth", module + "/internal/usecase/port", false},
		{"internal/usecase/auth", module + "/internal/platform/crypto", false},
		{"internal/usecase/auth", module + "/internal/platform/config", true},
		{"internal/usecase/auth", module + "/internal/adapter/dynamo", true},
		{"internal/usecase/auth", "github.com/labstack/echo/v4", true},
		{"internal/usecase/auth", "github.com/oklog/ulid/v2", false},
		{"internal/adapter/httpapi", module + "/internal/usecase/auth", false},
		{"internal/adapter/httpapi", module + "/internal/adapter/dynamo", true},
		{"internal/adapter/notion/notiontest", module + "/internal/adapter/notion", false},
		{"internal/adapter/queue", module + "/internal/wire", true},
		{"internal/platform/config", module + "/internal/platform/crypto", false},
		{"internal/platform/config", module + "/internal/domain", true},
		{"internal/wire", module + "/internal/adapter/dynamo", false},
	}
	for _, c := range cases {
		if got := violation(c.pkg, c.imp) != ""; got != c.bad {
			t.Errorf("violation(%s, %s) = %v, want %v", c.pkg, c.imp, got, c.bad)
		}
	}
}
```

- [ ] **Step 3: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/domain/... ./internal/archtest/...`
Expected: the domain tests fail to compile (`undefined: SyncPolicy`, etc.). Archtest passes, because its rules only cover packages that exist and the platform packages comply.

- [ ] **Step 4: Implement the domain package**

`backend/internal/domain/doc.go`:

```go
// Package domain holds the reading tracker's entities and business rules. It imports
// only the standard library: no storage tags and no Notion, AWS or HTTP types.
// Timestamps are RFC 3339 UTC strings with second precision (see Timestamp), which keeps
// them comparable as strings in storage conditions.
package domain
```

`backend/internal/domain/identity.go`:

```go
package domain

// User is a person who connected Notion (spec §3.1 PROFILE).
type User struct {
	ID                   string
	NotionUserID         string
	Name                 string
	Email                string
	ActiveInstallationID string
	CreatedAt            string
	DeletedAt            string
}

// Deleted reports whether account deletion is pending.
func (u User) Deleted() bool { return u.DeletedAt != "" }

// Device is one extension install. Its bearer token is kept only as TokenHMAC (C20).
type Device struct {
	ID         string
	UserID     string
	Name       string
	TokenHMAC  string
	CreatedAt  string
	LastSeenAt string
	RevokedAt  string
}

// Revoked reports whether the device was logged out or its account deleted.
func (d Device) Revoked() bool { return d.RevokedAt != "" }

// DeviceToken resolves a token HMAC to its device.
type DeviceToken struct {
	UserID   string
	DeviceID string
}

// OAuthState is a pending Notion authorization (single use, spec §2.1).
type OAuthState struct {
	ExtRedirect   string
	CodeChallenge string
}

// AuthCode is the one-time code the extension redeems for a device token (spec §2.2–§2.3).
type AuthCode struct {
	UserID         string
	InstallationID string
	CodeChallenge  string
	ExtRedirect    string
}
```

`backend/internal/domain/installation.go`:

```go
package domain

// Installation statuses (spec §3.1).
const (
	StatusActive     = "active"
	StatusNeedsSetup = "needs_setup"
	StatusRevoked    = "revoked"
)

// SetupErrResyncFailed marks an installation whose backlog could not be enqueued.
const SetupErrResyncFailed = "resync_failed"

// DBConfig is the resolved Notion target of an installation (spec §2.11).
type DBConfig struct {
	LibraryDBID string
	LibraryDSID string
	LibraryURL  string
	DailyDBID   string
	DailyDSID   string
	Props       map[string]string // logical key → Notion property id (C9)
}

// Installation is one Notion authorization (bot) for one workspace (spec §3.1 META).
type Installation struct {
	ID                   string
	UserID               string
	WorkspaceID          string
	WorkspaceName        string
	WorkspaceIcon        string
	Status               string
	SetupError           string
	AccessTokenEnc       string
	RefreshTokenEnc      string
	TokenExpiresAt       string
	TokenVersion         int
	DuplicatedTemplateID string
	LibraryDBID          string
	LibraryDSID          string
	LibraryURL           string
	DailyDBID            string
	DailyDSID            string
	Props                map[string]string
	CreatedAt            string
	UpdatedAt            string
}

// HasDatabases reports whether both data sources and their property ids are known.
func (i Installation) HasDatabases() bool {
	return i.LibraryDSID != "" && i.DailyDSID != "" && len(i.Props) > 0
}

// ApplyDatabases points the installation at db.
func (i *Installation) ApplyDatabases(db DBConfig) {
	i.LibraryDBID, i.LibraryDSID, i.LibraryURL = db.LibraryDBID, db.LibraryDSID, db.LibraryURL
	i.DailyDBID, i.DailyDSID, i.Props = db.DailyDBID, db.DailyDSID, db.Props
}
```

`backend/internal/domain/reading.go`:

```go
package domain

import "time"

// Article is the per-installation aggregate of one canonical URL (spec §3.1 ART).
type Article struct {
	Hash                string
	CanonicalURL        string
	Source              string
	Title               string
	Author              string
	SiteName            string
	WordCount           int
	PublishedAt         string
	FirstReadAt         string
	LastReadAt          string
	TotalSeconds        int64
	Sessions            int64
	MaxProgress         float64
	Completed           bool
	Version             int64
	NotionPageID        string
	NotionURL           string
	NotionDataSourceID  string // data source the page was written into (R1); "" for pages written before it was recorded
	NotionSyncedVersion int64
	NotionSyncedAt      string
}

// Day is the per-installation aggregate of one local date (spec §3.1 DAY).
type Day struct {
	Date                string
	Seconds             int64
	ArticleHashes       []string
	Version             int64
	NotionPageID        string
	NotionDataSourceID  string
	NotionSyncedVersion int64
}

// ArticleMeta is the descriptive part of an article, written once.
type ArticleMeta struct {
	CanonicalURL string
	Source       string
	Title        string
	Author       string
	SiteName     string
	WordCount    int
	PublishedAt  string
}

// SegmentWrite is one validated segment ready to be applied exactly once (C2).
type SegmentWrite struct {
	InstallationID string
	DeviceID       string
	SegmentID      string
	SessionID      string
	Seq            int
	URLHash        string
	Date           string // YYYY-MM-DD of StartedAt in the request tz
	StartedAt      time.Time
	EndedAt        time.Time
	ActiveSeconds  int
	MaxProgress    float64
	Article        ArticleMeta
}

// PushOutcome is how one segment was applied.
type PushOutcome int

const (
	PushAccepted PushOutcome = iota + 1
	PushDuplicate
	PushRetry
)
```

`backend/internal/domain/rules.go`:

```go
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"
)

// URLHash is hex(SHA-256(canonical_url))[:32] (spec §2.8 step 1).
func URLHash(canonicalURL string) string {
	sum := sha256.Sum256([]byte(canonicalURL))
	return hex.EncodeToString(sum[:])[:32]
}

// Timestamp formats t as RFC 3339 UTC with second precision.
func Timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// SameID reports whether two external ids are equal, ignoring dashes and case
// (Notion accepts and returns both forms).
func SameID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return norm(a) == norm(b)
}

// RoundMinutes converts active seconds to whole minutes, half away from zero (§3.3).
func RoundMinutes(secs int64) int64 { return int64(math.Round(float64(secs) / 60)) }

// SyncPolicy holds the 🔶 thresholds the Notion sync applies (spec §2.13, §7).
type SyncPolicy struct {
	MinCreateSeconds  int
	CompleteProgress  float64
	CompleteTimeRatio float64
	WordsPerMinute    int
}

// IsCompleted implements spec §2.13 step 2; once true it stays true.
func (p SyncPolicy) IsCompleted(a Article) bool {
	if a.Completed {
		return true
	}
	if a.MaxProgress < p.CompleteProgress {
		return false
	}
	if a.WordCount == 0 {
		return true
	}
	est := float64(a.WordCount) / float64(p.WordsPerMinute) * 60
	return float64(a.TotalSeconds) >= p.CompleteTimeRatio*est
}
```

`backend/internal/domain/errors.go`:

```go
package domain

import "errors"

var (
	// ErrNotFound: the requested item does not exist (or a TTL item has expired, C12).
	ErrNotFound = errors.New("not found")
	// ErrConditionFailed: a guarded write lost a race (e.g. token_version, C7).
	ErrConditionFailed = errors.New("condition failed")
	// ErrThrottled: storage capacity was exceeded; the caller should retry later (spec §2.0 503).
	ErrThrottled = errors.New("storage throttled")
)

// SetupError is why an installation's Notion databases could not be resolved.
// Reason is stored in the installation's setup_error (spec §2.11 step 4).
type SetupError struct{ Reason string }

func (e *SetupError) Error() string { return "setup: " + e.Reason }

// SetupReason returns the setup_error for err; unexpected errors map to "notion_error".
func SetupReason(err error) string {
	var e *SetupError
	if errors.As(err, &e) {
		return e.Reason
	}
	return "notion_error"
}
```

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`, including `internal/domain` (7 tests) and `internal/archtest` (2 tests).

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): add domain package, move shared libs to platform, enforce dependency rule"
```

---
### Task 2: `store` → `adapter/dynamo` speaking domain types

**Files:**
- Move: `internal/store` → `internal/adapter/dynamo` (package `dynamo`), `internal/store/storetest` → `internal/adapter/dynamo/dynamotest` (package `dynamotest`)
- Delete: `internal/adapter/dynamo/models.go`
- Create: `internal/adapter/dynamo/records.go`, `internal/adapter/dynamo/throttle.go`
- Rewrite: `internal/adapter/dynamo/identity.go`, `installations.go`, `articles.go`, `days.go`, `oauth.go`
- Modify: `internal/adapter/dynamo/store.go`, `keys.go`, `push.go`; every caller (`httpapi`, `worker`, `setup`, `syncsvc`, `notion/notiontest`, `app`, `cmd/local`, `e2e`) and every test
- Test: `internal/adapter/dynamo/records_internal_test.go`, `internal/adapter/dynamo/throttle_test.go`

**Interfaces:**
- Consumes: `domain` (Task 1).
- Produces:
  - `dynamo.New(db dynamo.API, table string, opts ...dynamo.Option) *dynamo.Store`, plus `dynamo.WithSleep`, `dynamo.CreateTable`, `dynamo.API`, `dynamo.Key` and the key builders (`UserPK`, `InstPK`, `NUserPK`, `DevTokenPK`, …). These are unchanged apart from the package name.
  - Every `*dynamo.Store` method keeps today's name and parameters. The only change is that it now takes and returns `domain` types instead of `store` types. For example, `GetProfile(ctx, uid) (*domain.User, error)`, `CreateUser(ctx, uid string, u domain.User) error`, `GetInstallation(ctx, botID) (*domain.Installation, error)` and `PushSegment(ctx, domain.SegmentWrite) (domain.PushOutcome, error)`. Returned entities have their `ID`/`UserID`/`Hash` fields filled from the item keys.
  - Errors are `domain.ErrNotFound` and `domain.ErrConditionFailed`. Any DynamoDB throttling error satisfies `errors.Is(err, domain.ErrThrottled)` and still `errors.As` the SDK type.
  - `dynamotest.New(t) *dynamotest.Env{Store *dynamo.Store; DB *dynamodb.Client; Table string}`, plus `Scan`, `Endpoint` and `Client`. These are unchanged apart from the package name.

- [ ] **Step 1: Move the packages and rewrite references mechanically**

```bash
cd backend
mkdir -p internal/adapter
git mv internal/store internal/adapter/dynamo
git mv internal/adapter/dynamo/storetest internal/adapter/dynamo/dynamotest
perl -pi -e 's/^package store_test$/package dynamo_test/; s/^package store$/package dynamo/' internal/adapter/dynamo/*.go
perl -pi -e 's/^package storetest$/package dynamotest/' internal/adapter/dynamo/dynamotest/*.go
FILES=$(grep -rlE 'internal/store(/storetest)?"|\bstore\.|\bstoretest\.' --include='*.go' . )
perl -pi -e '
  s#reading-tracker/backend/internal/store/storetest"#reading-tracker/backend/internal/adapter/dynamo/dynamotest"#g;
  s#reading-tracker/backend/internal/store"#reading-tracker/backend/internal/adapter/dynamo"#g;
  s/\bstoretest\./dynamotest./g;
  s/\bstore\.Profile\b/domain.User/g;
  s/\bstore\.(Device|DeviceToken|OAuthState|AuthCode|Installation|DBConfig|Article|Day|ArticleMeta|SegmentWrite|PushOutcome|PushAccepted|PushDuplicate|PushRetry|StatusActive|StatusNeedsSetup|StatusRevoked|Timestamp|URLHash|ErrNotFound|ErrConditionFailed)\b/domain.$1/g;
  s/\bstore\./dynamo./g;
  s/\.UserID\(\)/.ID/g;
  s/\.ID\(\)/.ID/g;
  s/\.Hash\(\)/.Hash/g;
' $FILES
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w $FILES
```

`.UserID()` was only ever the `Profile` accessor and `.ID()` only the `Device`/`Installation` accessors, so the global rewrite is safe. `go build` in Step 6 confirms it.

- [ ] **Step 2: Write the failing adapter tests**

`backend/internal/adapter/dynamo/records_internal_test.go`:

```go
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
```

`backend/internal/adapter/dynamo/throttle_test.go`:

```go
package dynamo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

type throttledGetDB struct{ dynamo.API }

func (throttledGetDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Review focus 2 (adapter half): throttling is visible as domain.ErrThrottled.
func TestThrottleErrorsAreTagged(t *testing.T) {
	env := dynamotest.New(t)
	s := dynamo.New(throttledGetDB{env.DB}, env.Table)
	_, err := s.GetProfile(context.Background(), "u1")
	if !errors.Is(err, domain.ErrThrottled) {
		t.Fatalf("err %v does not match domain.ErrThrottled", err)
	}
	var pte *types.ProvisionedThroughputExceededException
	if !errors.As(err, &pte) {
		t.Fatal("SDK error no longer reachable with errors.As")
	}
	if _, err := env.Store.GetProfile(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrThrottled) {
		t.Fatalf("not-found error %v", err)
	}
}
```

- [ ] **Step 3: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/adapter/dynamo/...`
Expected: FAIL to compile with `undefined: newUserRecord` (and the other record types).

- [ ] **Step 4: Replace the models with private records**

```bash
cd backend && git rm -q internal/adapter/dynamo/models.go
```

`backend/internal/adapter/dynamo/records.go`:

```go
package dynamo

import (
	"strings"

	"reading-tracker/backend/internal/domain"
)

// Records are the stored item shapes (spec §3.1). They carry the dynamodbav tags so
// the domain does not have to; attribute names must never change (stored data).

type userRecord struct {
	PK                   string `dynamodbav:"pk"`
	SK                   string `dynamodbav:"sk"`
	NotionUserID         string `dynamodbav:"notion_user_id"`
	Name                 string `dynamodbav:"name"`
	Email                string `dynamodbav:"email,omitempty"`
	ActiveInstallationID string `dynamodbav:"active_installation_id,omitempty"`
	CreatedAt            string `dynamodbav:"created_at"`
	DeletedAt            string `dynamodbav:"deleted_at,omitempty"`
}

func newUserRecord(uid string, u domain.User) userRecord {
	return userRecord{PK: UserPK(uid), SK: SKProfile, NotionUserID: u.NotionUserID, Name: u.Name, Email: u.Email,
		ActiveInstallationID: u.ActiveInstallationID, CreatedAt: u.CreatedAt, DeletedAt: u.DeletedAt}
}

func (r userRecord) toDomain() *domain.User {
	return &domain.User{ID: strings.TrimPrefix(r.PK, "USER#"), NotionUserID: r.NotionUserID, Name: r.Name, Email: r.Email,
		ActiveInstallationID: r.ActiveInstallationID, CreatedAt: r.CreatedAt, DeletedAt: r.DeletedAt}
}

type deviceRecord struct {
	PK         string `dynamodbav:"pk"`
	SK         string `dynamodbav:"sk"`
	Name       string `dynamodbav:"name"`
	TokenHMAC  string `dynamodbav:"token_hmac"`
	CreatedAt  string `dynamodbav:"created_at"`
	LastSeenAt string `dynamodbav:"last_seen_at"`
	RevokedAt  string `dynamodbav:"revoked_at,omitempty"`
}

func newDeviceRecord(uid, deviceID string, d domain.Device) deviceRecord {
	return deviceRecord{PK: UserPK(uid), SK: DeviceSK(deviceID), Name: d.Name, TokenHMAC: d.TokenHMAC,
		CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, RevokedAt: d.RevokedAt}
}

func (r deviceRecord) toDomain() domain.Device {
	return domain.Device{ID: strings.TrimPrefix(r.SK, "DEVICE#"), UserID: strings.TrimPrefix(r.PK, "USER#"), Name: r.Name,
		TokenHMAC: r.TokenHMAC, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, RevokedAt: r.RevokedAt}
}

type deviceTokenRecord struct {
	PK       string `dynamodbav:"pk"`
	SK       string `dynamodbav:"sk"`
	UserID   string `dynamodbav:"user_id"`
	DeviceID string `dynamodbav:"device_id"`
}

type oauthStateRecord struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	ExtRedirect   string `dynamodbav:"ext_redirect"`
	CodeChallenge string `dynamodbav:"code_challenge"`
	TTL           int64  `dynamodbav:"ttl"`
}

type authCodeRecord struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	UserID         string `dynamodbav:"user_id"`
	InstallationID string `dynamodbav:"installation_id"`
	CodeChallenge  string `dynamodbav:"code_challenge"`
	ExtRedirect    string `dynamodbav:"ext_redirect"`
	TTL            int64  `dynamodbav:"ttl"`
}

type installationRecord struct {
	PK                   string            `dynamodbav:"pk"`
	SK                   string            `dynamodbav:"sk"`
	UserID               string            `dynamodbav:"user_id"`
	WorkspaceID          string            `dynamodbav:"workspace_id"`
	WorkspaceName        string            `dynamodbav:"workspace_name"`
	WorkspaceIcon        string            `dynamodbav:"workspace_icon,omitempty"`
	Status               string            `dynamodbav:"status"`
	SetupError           string            `dynamodbav:"setup_error,omitempty"`
	AccessTokenEnc       string            `dynamodbav:"access_token_enc"`
	RefreshTokenEnc      string            `dynamodbav:"refresh_token_enc"`
	TokenExpiresAt       string            `dynamodbav:"token_expires_at,omitempty"`
	TokenVersion         int               `dynamodbav:"token_version"`
	DuplicatedTemplateID string            `dynamodbav:"duplicated_template_id,omitempty"`
	LibraryDBID          string            `dynamodbav:"library_db_id,omitempty"`
	LibraryDSID          string            `dynamodbav:"library_ds_id,omitempty"`
	LibraryURL           string            `dynamodbav:"library_url,omitempty"`
	DailyDBID            string            `dynamodbav:"daily_db_id,omitempty"`
	DailyDSID            string            `dynamodbav:"daily_ds_id,omitempty"`
	Props                map[string]string `dynamodbav:"props,omitempty"`
	CreatedAt            string            `dynamodbav:"created_at"`
	UpdatedAt            string            `dynamodbav:"updated_at"`
}

func newInstallationRecord(botID string, i *domain.Installation) installationRecord {
	return installationRecord{PK: InstPK(botID), SK: SKMeta, UserID: i.UserID, WorkspaceID: i.WorkspaceID,
		WorkspaceName: i.WorkspaceName, WorkspaceIcon: i.WorkspaceIcon, Status: i.Status, SetupError: i.SetupError,
		AccessTokenEnc: i.AccessTokenEnc, RefreshTokenEnc: i.RefreshTokenEnc, TokenExpiresAt: i.TokenExpiresAt,
		TokenVersion: i.TokenVersion, DuplicatedTemplateID: i.DuplicatedTemplateID, LibraryDBID: i.LibraryDBID,
		LibraryDSID: i.LibraryDSID, LibraryURL: i.LibraryURL, DailyDBID: i.DailyDBID, DailyDSID: i.DailyDSID,
		Props: i.Props, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt}
}

func (r installationRecord) toDomain() *domain.Installation {
	return &domain.Installation{ID: strings.TrimPrefix(r.PK, "INST#"), UserID: r.UserID, WorkspaceID: r.WorkspaceID,
		WorkspaceName: r.WorkspaceName, WorkspaceIcon: r.WorkspaceIcon, Status: r.Status, SetupError: r.SetupError,
		AccessTokenEnc: r.AccessTokenEnc, RefreshTokenEnc: r.RefreshTokenEnc, TokenExpiresAt: r.TokenExpiresAt,
		TokenVersion: r.TokenVersion, DuplicatedTemplateID: r.DuplicatedTemplateID, LibraryDBID: r.LibraryDBID,
		LibraryDSID: r.LibraryDSID, LibraryURL: r.LibraryURL, DailyDBID: r.DailyDBID, DailyDSID: r.DailyDSID,
		Props: r.Props, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

type articleRecord struct {
	PK                  string  `dynamodbav:"pk"`
	SK                  string  `dynamodbav:"sk"`
	CanonicalURL        string  `dynamodbav:"canonical_url"`
	Source              string  `dynamodbav:"source"`
	Title               string  `dynamodbav:"title"`
	Author              string  `dynamodbav:"author"`
	SiteName            string  `dynamodbav:"site_name"`
	WordCount           int     `dynamodbav:"word_count"`
	PublishedAt         string  `dynamodbav:"published_at"`
	FirstReadAt         string  `dynamodbav:"first_read_at"`
	LastReadAt          string  `dynamodbav:"last_read_at"`
	TotalSeconds        int64   `dynamodbav:"total_seconds"`
	Sessions            int64   `dynamodbav:"sessions"`
	MaxProgress         float64 `dynamodbav:"max_progress"`
	Completed           bool    `dynamodbav:"completed"`
	Version             int64   `dynamodbav:"version"`
	NotionPageID        string  `dynamodbav:"notion_page_id,omitempty"`
	NotionURL           string  `dynamodbav:"notion_url,omitempty"`
	NotionDataSourceID  string  `dynamodbav:"notion_ds_id,omitempty"`
	NotionSyncedVersion int64   `dynamodbav:"notion_synced_version,omitempty"`
	NotionSyncedAt      string  `dynamodbav:"notion_synced_at,omitempty"`
}

func (r articleRecord) toDomain() *domain.Article {
	return &domain.Article{Hash: strings.TrimPrefix(r.SK, "ART#"), CanonicalURL: r.CanonicalURL, Source: r.Source,
		Title: r.Title, Author: r.Author, SiteName: r.SiteName, WordCount: r.WordCount, PublishedAt: r.PublishedAt,
		FirstReadAt: r.FirstReadAt, LastReadAt: r.LastReadAt, TotalSeconds: r.TotalSeconds, Sessions: r.Sessions,
		MaxProgress: r.MaxProgress, Completed: r.Completed, Version: r.Version, NotionPageID: r.NotionPageID,
		NotionURL: r.NotionURL, NotionDataSourceID: r.NotionDataSourceID, NotionSyncedVersion: r.NotionSyncedVersion,
		NotionSyncedAt: r.NotionSyncedAt}
}

type dayRecord struct {
	PK                  string   `dynamodbav:"pk"`
	SK                  string   `dynamodbav:"sk"`
	Date                string   `dynamodbav:"date"`
	Seconds             int64    `dynamodbav:"seconds"`
	ArticleHashes       []string `dynamodbav:"article_hashes,stringset"`
	Version             int64    `dynamodbav:"version"`
	NotionPageID        string   `dynamodbav:"notion_page_id,omitempty"`
	NotionDataSourceID  string   `dynamodbav:"notion_ds_id,omitempty"`
	NotionSyncedVersion int64    `dynamodbav:"notion_synced_version,omitempty"`
}

func (r dayRecord) toDomain() *domain.Day {
	return &domain.Day{Date: r.Date, Seconds: r.Seconds, ArticleHashes: r.ArticleHashes, Version: r.Version,
		NotionPageID: r.NotionPageID, NotionDataSourceID: r.NotionDataSourceID, NotionSyncedVersion: r.NotionSyncedVersion}
}
```

- [ ] **Step 5: Rewrite the repositories on top of the records**

Each file's logic, expressions and DynamoDB calls stay identical. Only the types change.

`backend/internal/adapter/dynamo/identity.go`:

```go
package dynamo

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetProfile(ctx context.Context, uid string) (*domain.User, error) {
	var r userRecord
	if err := s.get(ctx, UserPK(uid), SKProfile, &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}

// UserIDForNotionUser resolves NUSER#<notion_user_id> → user_id (spec §2.2 step 4).
func (s *Store) UserIDForNotionUser(ctx context.Context, notionUserID string) (string, error) {
	var link struct {
		UserID string `dynamodbav:"user_id"`
	}
	if err := s.get(ctx, NUserPK(notionUserID), SKNUser, &link); err != nil {
		return "", err
	}
	return link.UserID, nil
}

// CreateUser writes PROFILE and the NUSER pointer in one transaction.
func (s *Store) CreateUser(ctx context.Context, uid string, u domain.User) error {
	prof, err := attributevalue.MarshalMap(newUserRecord(uid, u))
	if err != nil {
		return err
	}
	link := map[string]types.AttributeValue{"pk": str(NUserPK(u.NotionUserID)), "sk": str(SKNUser), "user_id": str(uid)}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: prof}},
		{Put: &types.Put{TableName: &s.table, Item: link}},
	}})
	return err
}

func (s *Store) SetActiveInstallation(ctx context.Context, uid, botID string) error {
	return s.update(ctx, UserPK(uid), SKProfile, "SET #a = :a", "attribute_exists(pk)",
		map[string]string{"#a": "active_installation_id"}, map[string]types.AttributeValue{":a": str(botID)})
}

func (s *Store) MarkProfileDeleted(ctx context.Context, uid string, now time.Time) error {
	return s.update(ctx, UserPK(uid), SKProfile, "SET #d = :d", "attribute_exists(pk)",
		map[string]string{"#d": "deleted_at"}, map[string]types.AttributeValue{":d": str(domain.Timestamp(now))})
}

// CreateDevice writes DEVICE and its DEVTOKEN lookup together (spec §2.3 step 4).
func (s *Store) CreateDevice(ctx context.Context, uid, deviceID string, d domain.Device) error {
	dev, err := attributevalue.MarshalMap(newDeviceRecord(uid, deviceID, d))
	if err != nil {
		return err
	}
	tok := map[string]types.AttributeValue{
		"pk": str(DevTokenPK(d.TokenHMAC)), "sk": str(SKDevToken), "user_id": str(uid), "device_id": str(deviceID),
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: dev}},
		{Put: &types.Put{TableName: &s.table, Item: tok, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	return err
}

func (s *Store) GetDeviceToken(ctx context.Context, tokenHMAC string) (*domain.DeviceToken, error) {
	var r deviceTokenRecord
	if err := s.get(ctx, DevTokenPK(tokenHMAC), SKDevToken, &r); err != nil {
		return nil, err
	}
	return &domain.DeviceToken{UserID: r.UserID, DeviceID: r.DeviceID}, nil
}

func (s *Store) GetDevice(ctx context.Context, uid, deviceID string) (*domain.Device, error) {
	var r deviceRecord
	if err := s.get(ctx, UserPK(uid), DeviceSK(deviceID), &r); err != nil {
		return nil, err
	}
	d := r.toDomain()
	return &d, nil
}

// TouchDevice bumps last_seen_at only when it is over an hour old (spec §2.4 step 3).
func (s *Store) TouchDevice(ctx context.Context, uid, deviceID string, now time.Time) error {
	err := s.update(ctx, UserPK(uid), DeviceSK(deviceID), "SET #ls = :now",
		"attribute_exists(pk) AND (attribute_not_exists(#ls) OR #ls < :cutoff)",
		map[string]string{"#ls": "last_seen_at"},
		map[string]types.AttributeValue{":now": str(domain.Timestamp(now)), ":cutoff": str(domain.Timestamp(now.Add(-time.Hour)))})
	if errors.Is(err, domain.ErrConditionFailed) {
		return nil
	}
	return err
}

// RevokeDevice sets revoked_at and deletes the token lookup atomically (spec §2.17).
func (s *Store) RevokeDevice(ctx context.Context, uid, deviceID, tokenHMAC string, now time.Time) error {
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{
			TableName:                 &s.table,
			Key:                       key(UserPK(uid), DeviceSK(deviceID)),
			UpdateExpression:          aws.String("SET #r = :r"),
			ExpressionAttributeNames:  map[string]string{"#r": "revoked_at"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":r": str(domain.Timestamp(now))},
		}},
		{Delete: &types.Delete{TableName: &s.table, Key: key(DevTokenPK(tokenHMAC), SKDevToken)}},
	}})
	return err
}

func (s *Store) ListDevices(ctx context.Context, uid string) ([]domain.Device, error) {
	items, err := s.queryPrefix(ctx, UserPK(uid), "DEVICE#")
	if err != nil {
		return nil, err
	}
	var recs []deviceRecord
	if err := attributevalue.UnmarshalListOfMaps(items, &recs); err != nil {
		return nil, err
	}
	out := make([]domain.Device, len(recs))
	for i, r := range recs {
		out[i] = r.toDomain()
	}
	return out, nil
}

func (s *Store) DeleteDeviceToken(ctx context.Context, tokenHMAC string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &s.table, Key: key(DevTokenPK(tokenHMAC), SKDevToken)})
	return err
}
```

`backend/internal/adapter/dynamo/oauth.go`:

```go
package dynamo

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) PutOAuthState(ctx context.Context, state string, st domain.OAuthState, expires time.Time) error {
	return s.putItem(ctx, oauthStateRecord{PK: OAuthStatePK(state), SK: SKState, ExtRedirect: st.ExtRedirect,
		CodeChallenge: st.CodeChallenge, TTL: expires.Unix()})
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*domain.OAuthState, error) {
	var r oauthStateRecord
	if err := s.consume(ctx, OAuthStatePK(state), SKState, now, &r); err != nil {
		return nil, err
	}
	return &domain.OAuthState{ExtRedirect: r.ExtRedirect, CodeChallenge: r.CodeChallenge}, nil
}

func (s *Store) PutAuthCode(ctx context.Context, code string, ac domain.AuthCode, expires time.Time) error {
	return s.putItem(ctx, authCodeRecord{PK: AuthCodePK(code), SK: SKCode, UserID: ac.UserID, InstallationID: ac.InstallationID,
		CodeChallenge: ac.CodeChallenge, ExtRedirect: ac.ExtRedirect, TTL: expires.Unix()})
}

func (s *Store) ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*domain.AuthCode, error) {
	var r authCodeRecord
	if err := s.consume(ctx, AuthCodePK(code), SKCode, now, &r); err != nil {
		return nil, err
	}
	return &domain.AuthCode{UserID: r.UserID, InstallationID: r.InstallationID, CodeChallenge: r.CodeChallenge,
		ExtRedirect: r.ExtRedirect}, nil
}

// consume deletes a single-use TTL item and returns it. Expired items that DynamoDB
// hasn't reaped yet fail the condition, because TTL deletion is lazy (C12).
func (s *Store) consume(ctx context.Context, pk, sk string, now time.Time, out any) error {
	res, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 &s.table,
		Key:                       key(pk, sk),
		ConditionExpression:       aws.String("attribute_exists(pk) AND #ttl > :now"),
		ExpressionAttributeNames:  map[string]string{"#ttl": "ttl"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": numI(now.Unix())},
		ReturnValues:              types.ReturnValueAllOld,
	})
	if isConditionFailed(err) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return attributevalue.UnmarshalMap(res.Attributes, out)
}
```

`backend/internal/adapter/dynamo/installations.go`: copy today's file, then apply exactly these changes:
- Add the import `"reading-tracker/backend/internal/domain"`.
- `GetInstallation` decodes into `installationRecord` and returns `r.toDomain()`:

```go
func (s *Store) GetInstallation(ctx context.Context, botID string) (*domain.Installation, error) {
	var r installationRecord
	if err := s.get(ctx, InstPK(botID), SKMeta, &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}

// PutInstallation writes the whole META item (used by the OAuth callback upsert).
func (s *Store) PutInstallation(ctx context.Context, botID string, inst *domain.Installation) error {
	inst.ID = botID
	return s.putItem(ctx, newInstallationRecord(botID, inst))
}
```

- In `PutUserInstallation`, `SetInstallationStatus`, `SaveInstallationDatabases` and `UpdateInstallationTokens`:
  - replace `Timestamp(` with `domain.Timestamp(`;
  - replace `StatusActive` with `domain.StatusActive`;
  - replace the parameter type `DBConfig` with `domain.DBConfig`.

`backend/internal/adapter/dynamo/articles.go`: copy today's file, then apply exactly these changes:
- Add the import `"reading-tracker/backend/internal/domain"`.
- Rewrite `GetArticle`:

```go
func (s *Store) GetArticle(ctx context.Context, inst, hash string) (*domain.Article, error) {
	var r articleRecord
	if err := s.get(ctx, InstPK(inst), ArticleSK(hash), &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}
```

- In `BatchGetArticles`:
  - the return type is `map[string]*domain.Article` and `out := map[string]*domain.Article{}`;
  - the loop body unmarshals into `var r articleRecord`, then sets `a := r.toDomain(); out[a.Hash] = a`.
- In `MarkArticleSynced`:
  - replace `Timestamp(` with `domain.Timestamp(`;
  - replace `ErrConditionFailed` with `domain.ErrConditionFailed`.

`backend/internal/adapter/dynamo/days.go`: copy today's file, then:
- Add the import `"reading-tracker/backend/internal/domain"`.
- Rewrite `GetDay`:

```go
func (s *Store) GetDay(ctx context.Context, inst, date string) (*domain.Day, error) {
	var r dayRecord
	if err := s.get(ctx, InstPK(inst), DaySK(date), &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}
```

- In `MarkDaySynced`, replace `ErrConditionFailed` with `domain.ErrConditionFailed`.

- [ ] **Step 6: Move the shared rules into the domain and add throttle tagging**

`backend/internal/adapter/dynamo/keys.go`: delete `URLHash` and its `crypto/sha256` and `encoding/hex` imports. Its replacement is `domain.URLHash`.

`backend/internal/adapter/dynamo/store.go`:
- Delete the `ErrNotFound`/`ErrConditionFailed` `var` block and the `Timestamp` function.
- Rename `IsThrottle` to `isThrottle`, unexported, with the same body.
- In `get`, return `domain.ErrNotFound`.
- In `update`, return `domain.ErrConditionFailed`.
- Make `New` wrap the client:

```go
func New(db API, table string, opts ...Option) *Store {
	s := &Store{db: taggingAPI{db}, table: table, sleep: time.Sleep}
	for _, o := range opts {
		o(s)
	}
	return s
}
```

`backend/internal/adapter/dynamo/push.go`:
- Delete the `ArticleMeta`, `SegmentWrite` and `PushOutcome` declarations, including the `PushAccepted…PushRetry` const block.
- Qualify the remaining references:

```bash
cd backend/internal/adapter/dynamo
perl -pi -e 's/(?<![.\w])(SegmentWrite|ArticleMeta|PushOutcome|PushAccepted|PushDuplicate|PushRetry|Timestamp|ErrConditionFailed|ErrNotFound)\b/domain.$1/g; s/(?<![.\w])IsThrottle\(/isThrottle(/g' push.go maintenance.go store.go
cd ../../.. && go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/adapter/dynamo
```

`backend/internal/adapter/dynamo/throttle.go`:

```go
package dynamo

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"reading-tracker/backend/internal/domain"
)

// taggingAPI marks DynamoDB capacity errors so callers outside this adapter can
// recognise them with errors.Is(err, domain.ErrThrottled) (spec §2.0 → 503), while
// errors.As still reaches the SDK error for this package's own classification.
type taggingAPI struct{ API }

type throttledError struct{ err error }

func (e throttledError) Error() string   { return e.err.Error() }
func (e throttledError) Unwrap() []error { return []error{domain.ErrThrottled, e.err} }

func tag(err error) error {
	if err != nil && isThrottle(err) {
		return throttledError{err}
	}
	return err
}

func (t taggingAPI) GetItem(ctx context.Context, in *dynamodb.GetItemInput, o ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	out, err := t.API.GetItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) PutItem(ctx context.Context, in *dynamodb.PutItemInput, o ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	out, err := t.API.PutItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, o ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	out, err := t.API.UpdateItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, o ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	out, err := t.API.DeleteItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) Query(ctx context.Context, in *dynamodb.QueryInput, o ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	out, err := t.API.Query(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, o ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	out, err := t.API.BatchGetItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, o ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	out, err := t.API.BatchWriteItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, o ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	out, err := t.API.TransactWriteItems(ctx, in, o...)
	return out, tag(err)
}
```

`push.go`'s `classifyTx` keeps working: `isThrottle` and `errors.As` see through `throttledError` because `Unwrap() []error` exposes the SDK error.

- [ ] **Step 7: Fix the HTTP error mapping and remaining compile errors**

In `backend/internal/httpapi/errors.go`, replace `if store.IsThrottle(err)` with `if errors.Is(err, domain.ErrThrottled)` (after Step 1 it reads `dynamo.IsThrottle`). Fix the imports.

In `backend/internal/httpapi/errors_internal_test.go`, rewrite the throttle cases to construct throttles the way the adapter now surfaces them. The precedence cases stay as they are.

```go
		{"throttled", fmt.Errorf("get profile: %w", fmt.Errorf("%w: slow down", domain.ErrThrottled))},
```

If that test file has separate cases for `ProvisionedThroughputExceededException`, `ThrottlingException` and `RequestLimitExceeded`, replace them with this one wrapped-sentinel case plus a direct `domain.ErrThrottled` case. Recognising the SDK error codes is now `TestThrottleErrorsAreTagged`'s job. List the assertion change in the report.

Then build and fix whatever the mechanical rewrite missed:

```bash
cd backend && go build ./... && go vet ./...
```

Expected leftovers, all mechanical:
- Composite literals `domain.Device{…}` / `domain.User{…}` in tests that set `PK`/`SK`: no test does this today; if the compiler reports one, drop those fields.
- `worker.isCompleted` now takes `domain.Article` by pointer. Leave it as it is: it is deleted in Task 5.
- `notiontest`'s `Template.DBConfig()` returns `domain.DBConfig`.

- [ ] **Step 8: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`. The store tests, now `internal/adapter/dynamo`, number 25 plus 3 new ones.

- [ ] **Step 9: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): move store to adapter/dynamo with private records mapped to domain types"
```

---
### Task 3: Ports + `notion` → `adapter/notion` with the Notion gateway (auth and workspace)

**Files:**
- Move: `internal/notion` → `internal/adapter/notion` (package `notion` unchanged) and `internal/notion/notiontest` → `internal/adapter/notion/notiontest`
- Move: `internal/setup/schema.go` → `internal/adapter/notion/schema.go`
- Create: `internal/usecase/port/repositories.go`, `internal/usecase/port/notion.go`
- Create: `internal/adapter/notion/errors.go`, `gateway.go`, `workspace.go`
- Modify: `internal/setup/setup.go` (keep only `Service.Resync`), `internal/httpapi` (use the gateway), `internal/adapter/notion/notiontest/fake.go`, `internal/adapter/dynamo/store.go` (port assertions)
- Move tests: the template and provisioning tests in `internal/setup/setup_test.go` → `internal/adapter/notion/workspace_test.go`
- Test: `internal/adapter/notion/errors_internal_test.go`

**Interfaces:**
- Consumes: `domain` and `adapter/dynamo` (Tasks 1–2).
- Produces:
  - Repository ports, implemented by `*dynamo.Store`:
    - `port.Users`: `GetProfile`, `UserIDForNotionUser`, `CreateUser`, `SetActiveInstallation`, `MarkProfileDeleted`, `DeleteNotionUserLink`
    - `port.Devices`: `CreateDevice`, `GetDeviceToken`, `GetDevice`, `TouchDevice`, `RevokeDevice`, `ListDevices`, `DeleteDeviceToken`
    - `port.OAuthGrants`: `PutOAuthState`, `ConsumeOAuthState`, `PutAuthCode`, `ConsumeAuthCode`
    - `port.Installations`: `GetInstallation`, `PutInstallation`, `PutUserInstallation`, `ListUserInstallationIDs`, `SetInstallationStatus`, `SaveInstallationDatabases`, `UpdateInstallationTokens`
    - `port.Reading`: `PushSegment`, `GetArticle`, `BatchGetArticles`, `SetArticlePage`, `ClearArticlePage`, `MarkArticleSynced`, `GetDay`, `SetDayPage`, `ClearDayPage`, `MarkDaySynced`, `PendingSync`
    - Each method has the exact signature `*dynamo.Store` has after Task 2.
  - Notion ports:
    - `port.NotionGrant{AccessToken, RefreshToken, BotID, WorkspaceID, WorkspaceName, WorkspaceIcon, DuplicatedTemplateID string; ExpiresIn int; OwnerUserID, OwnerName, OwnerEmail string}`
    - `port.PageRef{ID, Title, URL string}`
    - `port.NotionAuth`: `AuthorizeURL(redirectURI, state string) string`, `ExchangeCode(ctx, code, redirectURI string) (*NotionGrant, error)`, `RefreshToken(ctx, refreshToken string) (*NotionGrant, error)`
    - `port.NotionWorkspace`: `SearchPages(ctx, token, query string) ([]PageRef, error)`, `ResolveTemplate(ctx, token, templateID string) (domain.DBConfig, error)`, `ProvisionDatabases(ctx, token, parentPageID string) (domain.DBConfig, error)`
  - Notion errors:
    - `port.NotionErrorKind` has the values `NotionTransient`, `NotionRateLimited`, `NotionUnauthorized`, `NotionInvalidGrant`, `NotionConflict`, `NotionNotFound`, `NotionPageGone`, `NotionDataSourceMissing` and `NotionRejected`.
    - `port.NotionError{Kind NotionErrorKind; Status int; Code, Message string; RetryAfter time.Duration; Err error}`
    - `port.NotionErrorOf(err) (*NotionError, bool)`
  - `notion.Gateway{Client *notion.Client}` implements `port.NotionAuth` and `port.NotionWorkspace`. Its errors are `*port.NotionError` or `*domain.SetupError`.
  - Schema: `notion.LibraryTitle`, `notion.DailyTitle`, `notion.PropSpec`, `notion.LibraryProps`, `notion.DailyProps`.
  - `setup.Service{Store *dynamo.Store; Queue queue.Sender; Bucket time.Duration; Now func() time.Time}` keeps only `Resync(ctx, inst string, full bool) error`.

- [ ] **Step 1: Move the client and fake, and the schema into the adapter**

```bash
cd backend
git mv internal/notion internal/adapter/notion
git mv internal/setup/schema.go internal/adapter/notion/schema.go
perl -pi -e 's/^package setup$/package notion/' internal/adapter/notion/schema.go
FILES=$(grep -rl 'reading-tracker/backend/internal/notion' --include='*.go' .)
perl -pi -e 's#reading-tracker/backend/internal/notion#reading-tracker/backend/internal/adapter/notion#g' $FILES
perl -pi -e 's/\bsetup\.(LibraryProps|DailyProps|LibraryTitle|DailyTitle)\b/notion.$1/g' internal/adapter/notion/notiontest/fake.go
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/adapter/notion/notiontest/fake.go
```

- [ ] **Step 2: Write the ports**

`backend/internal/usecase/port/repositories.go`:

```go
// Package port declares what the use cases need from the outside world. Adapters
// implement these interfaces; use cases never import an adapter.
package port

import (
	"context"
	"time"

	"reading-tracker/backend/internal/domain"
)

// Users is the PROFILE and NUSER store.
type Users interface {
	GetProfile(ctx context.Context, uid string) (*domain.User, error)
	UserIDForNotionUser(ctx context.Context, notionUserID string) (string, error)
	CreateUser(ctx context.Context, uid string, u domain.User) error
	SetActiveInstallation(ctx context.Context, uid, botID string) error
	MarkProfileDeleted(ctx context.Context, uid string, now time.Time) error
	DeleteNotionUserLink(ctx context.Context, notionUserID, uid string) error
}

// Devices is the DEVICE and DEVTOKEN store.
type Devices interface {
	CreateDevice(ctx context.Context, uid, deviceID string, d domain.Device) error
	GetDeviceToken(ctx context.Context, tokenHMAC string) (*domain.DeviceToken, error)
	GetDevice(ctx context.Context, uid, deviceID string) (*domain.Device, error)
	TouchDevice(ctx context.Context, uid, deviceID string, now time.Time) error
	RevokeDevice(ctx context.Context, uid, deviceID, tokenHMAC string, now time.Time) error
	ListDevices(ctx context.Context, uid string) ([]domain.Device, error)
	DeleteDeviceToken(ctx context.Context, tokenHMAC string) error
}

// OAuthGrants is the single-use OAUTHSTATE and AUTHCODE store (C12).
type OAuthGrants interface {
	PutOAuthState(ctx context.Context, state string, st domain.OAuthState, expires time.Time) error
	ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*domain.OAuthState, error)
	PutAuthCode(ctx context.Context, code string, ac domain.AuthCode, expires time.Time) error
	ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*domain.AuthCode, error)
}

// Installations is the INST META store.
type Installations interface {
	GetInstallation(ctx context.Context, botID string) (*domain.Installation, error)
	PutInstallation(ctx context.Context, botID string, inst *domain.Installation) error
	PutUserInstallation(ctx context.Context, uid, botID string, now time.Time) error
	ListUserInstallationIDs(ctx context.Context, uid string) ([]string, error)
	SetInstallationStatus(ctx context.Context, botID, status, setupError string, now time.Time) error
	SaveInstallationDatabases(ctx context.Context, botID string, db domain.DBConfig, now time.Time) error
	UpdateInstallationTokens(ctx context.Context, botID, accessEnc, refreshEnc string, oldVersion int, now time.Time) error
}

// Reading is the SEG, ART and DAY store.
type Reading interface {
	PushSegment(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error)
	GetArticle(ctx context.Context, inst, hash string) (*domain.Article, error)
	BatchGetArticles(ctx context.Context, inst string, hashes []string) (map[string]*domain.Article, error)
	SetArticlePage(ctx context.Context, inst, hash, pageID, url string) error
	ClearArticlePage(ctx context.Context, inst, hash string) error
	MarkArticleSynced(ctx context.Context, inst, hash string, version int64, completed bool, now time.Time) error
	GetDay(ctx context.Context, inst, date string) (*domain.Day, error)
	SetDayPage(ctx context.Context, inst, date, pageID string) error
	ClearDayPage(ctx context.Context, inst, date string) error
	MarkDaySynced(ctx context.Context, inst, date string, version int64) error
	PendingSync(ctx context.Context, inst string, all bool) (hashes, dates []string, err error)
}
```

`backend/internal/usecase/port/notion.go`:

```go
package port

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reading-tracker/backend/internal/domain"
)

// NotionGrant is the result of an OAuth code exchange or token refresh.
type NotionGrant struct {
	AccessToken          string
	RefreshToken         string
	BotID                string
	WorkspaceID          string
	WorkspaceName        string
	WorkspaceIcon        string
	DuplicatedTemplateID string
	ExpiresIn            int
	OwnerUserID          string
	OwnerName            string
	OwnerEmail           string
}

// PageRef is a page the integration can see (setup page picker).
type PageRef struct {
	ID    string
	Title string
	URL   string
}

// NotionAuth is Notion's OAuth endpoints (spec §2.1–§2.2, §2.16).
type NotionAuth interface {
	AuthorizeURL(redirectURI, state string) string
	ExchangeCode(ctx context.Context, code, redirectURI string) (*NotionGrant, error)
	RefreshToken(ctx context.Context, refreshToken string) (*NotionGrant, error)
}

// NotionWorkspace finds and builds the user's databases (spec §2.6, §2.7, §2.11).
// Resolution failures are *domain.SetupError; everything else is *NotionError.
type NotionWorkspace interface {
	SearchPages(ctx context.Context, token, query string) ([]PageRef, error)
	ResolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error)
	ProvisionDatabases(ctx context.Context, token, parentPageID string) (domain.DBConfig, error)
}

// NotionErrorKind is what a failed Notion call means to the caller (spec §2.15).
type NotionErrorKind int

const (
	NotionTransient         NotionErrorKind = iota // 5xx or network: retry
	NotionRateLimited                              // 429/529: back off RetryAfter
	NotionUnauthorized                             // 401: refresh the token (worker) or reconnect (API)
	NotionInvalidGrant                             // refresh token rejected: revoked
	NotionConflict                                 // 409 conflict_error: retry
	NotionNotFound                                 // 404 on an object that isn't a page or data source
	NotionPageGone                                 // page 404, archived, trashed or moved to another data source
	NotionDataSourceMissing                        // data source 404: the database was deleted
	NotionRejected                                 // any other 4xx: poison
)

func (k NotionErrorKind) String() string {
	return [...]string{"transient", "rate_limited", "unauthorized", "invalid_grant", "conflict", "not_found",
		"page_gone", "data_source_missing", "rejected"}[k]
}

// NotionError is a classified Notion failure.
type NotionError struct {
	Kind       NotionErrorKind
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Err        error
}

func (e *NotionError) Error() string {
	return fmt.Sprintf("notion %s: %d %s: %s", e.Kind, e.Status, e.Code, e.Message)
}

func (e *NotionError) Unwrap() error { return e.Err }

// NotionErrorOf unwraps err into *NotionError.
func NotionErrorOf(err error) (*NotionError, bool) {
	var e *NotionError
	ok := errors.As(err, &e)
	return e, ok
}

// IsNotionKind reports whether err is a NotionError of kind k.
func IsNotionKind(err error, k NotionErrorKind) bool {
	e, ok := NotionErrorOf(err)
	return ok && e.Kind == k
}
```

At the end of `backend/internal/adapter/dynamo/store.go`, add compile-time port assertions and import `reading-tracker/backend/internal/usecase/port`:

```go
var (
	_ port.Users         = (*Store)(nil)
	_ port.Devices       = (*Store)(nil)
	_ port.OAuthGrants   = (*Store)(nil)
	_ port.Installations = (*Store)(nil)
	_ port.Reading       = (*Store)(nil)
)
```

- [ ] **Step 3: Write the failing gateway tests**

`backend/internal/adapter/notion/errors_internal_test.go`:

```go
package notion

import (
	"errors"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// Review focus 3: every Notion response class maps to the same outcome as before.
func TestTranslateKinds(t *testing.T) {
	cases := []struct {
		err      error
		notFound port.NotionErrorKind
		want     port.NotionErrorKind
	}{
		{&APIError{Status: 429, RetryAfter: 2 * time.Second}, port.NotionPageGone, port.NotionRateLimited},
		{&APIError{Status: 529}, port.NotionPageGone, port.NotionRateLimited},
		{&APIError{Status: 502}, port.NotionPageGone, port.NotionTransient},
		{&APIError{Status: 409, Code: "conflict_error"}, port.NotionPageGone, port.NotionConflict},
		{&APIError{Status: 401, Code: "unauthorized"}, port.NotionPageGone, port.NotionUnauthorized},
		{&APIError{Status: 400, Code: "invalid_grant"}, port.NotionNotFound, port.NotionInvalidGrant},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionPageGone, port.NotionPageGone},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionDataSourceMissing, port.NotionDataSourceMissing},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionNotFound, port.NotionNotFound},
		{&APIError{Status: 400, Code: "validation_error", Message: "Can't edit block that is archived."}, port.NotionPageGone, port.NotionPageGone},
		{&APIError{Status: 400, Code: "validation_error", Message: "Can't edit block that is archived."}, port.NotionDataSourceMissing, port.NotionRejected},
		{&APIError{Status: 400, Code: "validation_error", Message: "body failed validation"}, port.NotionPageGone, port.NotionRejected},
		{&APIError{Status: 403, Code: "restricted_resource"}, port.NotionPageGone, port.NotionRejected},
		{errors.New("dial tcp: connection refused"), port.NotionPageGone, port.NotionTransient},
	}
	for i, c := range cases {
		ne, ok := port.NotionErrorOf(translate(c.err, c.notFound))
		if !ok || ne.Kind != c.want {
			t.Errorf("case %d: got %+v, want kind %s", i, ne, c.want)
		}
	}
	if ne, _ := port.NotionErrorOf(translate(&APIError{Status: 429, RetryAfter: 2 * time.Second}, port.NotionNotFound)); ne.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter lost: %+v", ne)
	}
	if translate(nil, port.NotionNotFound) != nil {
		t.Fatal("nil must stay nil")
	}
	se := &domain.SetupError{Reason: "template_not_found"}
	if got := translate(se, port.NotionNotFound); got != se {
		t.Fatalf("setup errors must pass through, got %v", got)
	}
}
```

Move the template and provisioning tests. Create `backend/internal/adapter/notion/workspace_test.go` from `internal/setup/setup_test.go`:

```bash
cd backend
cp internal/setup/setup_test.go internal/adapter/notion/workspace_test.go
```

Then edit `internal/adapter/notion/workspace_test.go`:
1. Change the package clause to `package notion_test`.
2. Delete `TestResyncEnqueuesPending`; it stays in `internal/setup`.
3. Replace the fixture with:

```go
type fixture struct {
	gw *notion.Gateway
	ns *notiontest.Server
}

func newFixture(t *testing.T) *fixture {
	ns := notiontest.New(t)
	return &fixture{gw: &notion.Gateway{Client: &notion.Client{BaseURL: ns.URL, Version: "2025-09-03"}}, ns: ns}
}
```

4. Run the rewrites below, then delete the now-unused `queue`, `setup`, `dynamo`, `dynamotest` imports and the `testNow` variable:

```bash
perl -pi -e '
  s/f\.svc\.ResolveTemplate\(/f.gw.ResolveTemplate(/g;
  s/f\.svc\.CreateDatabases\(/f.gw.ProvisionDatabases(/g;
  s/\bsetup\.Reason\(/domain.SetupReason(/g;
  s/\bsetup\.(LibraryTitle|DailyTitle|LibraryProps|DailyProps)\b/notion.$1/g;
' internal/adapter/notion/workspace_test.go
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/adapter/notion/workspace_test.go
```

Every assertion stays the same. The provisioning tests' `TestCreateDatabases…` names may stay.

In `internal/setup/setup_test.go`, delete every test except `TestResyncEnqueuesPending`. Shrink the fixture to the store, recorder and `setup.Service{Store, Queue, Bucket, Now}`, and remove the unused imports.

- [ ] **Step 4: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/adapter/notion/...`
Expected: FAIL to compile with `undefined: translate` and `undefined: notion.Gateway`.

- [ ] **Step 5: Implement the gateway**

`backend/internal/adapter/notion/errors.go`:

```go
package notion

import (
	"errors"
	"net/http"
	"strings"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// translate classifies a client error for the use cases (spec §2.15). notFound is
// what a 404 means for this call: NotionPageGone (PATCH a page),
// NotionDataSourceMissing (query or create in a data source) or NotionNotFound.
// Setup errors pass through unchanged.
func translate(err error, notFound port.NotionErrorKind) error {
	if err == nil {
		return nil
	}
	var se *domain.SetupError
	if errors.As(err, &se) {
		return err
	}
	e, ok := AsAPIError(err)
	if !ok {
		return &port.NotionError{Kind: port.NotionTransient, Message: err.Error(), Err: err}
	}
	ne := &port.NotionError{Status: e.Status, Code: e.Code, Message: e.Message, RetryAfter: e.RetryAfter, Err: err}
	switch {
	case e.Status == http.StatusTooManyRequests || e.Status == 529:
		ne.Kind = port.NotionRateLimited
	case e.Status >= 500:
		ne.Kind = port.NotionTransient
	case e.Code == "invalid_grant":
		ne.Kind = port.NotionInvalidGrant
	case e.Status == http.StatusUnauthorized:
		ne.Kind = port.NotionUnauthorized
	case e.Status == http.StatusConflict:
		ne.Kind = port.NotionConflict
	case e.Status == http.StatusNotFound:
		ne.Kind = notFound
	case notFound == port.NotionPageGone && e.Status == http.StatusBadRequest && e.Code == "validation_error" &&
		strings.Contains(strings.ToLower(e.Message), "archived"):
		ne.Kind = port.NotionPageGone // "Can't edit block that is archived"
	default:
		ne.Kind = port.NotionRejected
	}
	return ne
}

// isNotFound reports a raw client 404 (used before translation, inside this adapter).
func isNotFound(err error) bool {
	e, ok := AsAPIError(err)
	return ok && e.Status == http.StatusNotFound
}
```

`backend/internal/adapter/notion/gateway.go`:

```go
package notion

import (
	"context"

	"reading-tracker/backend/internal/usecase/port"
)

// Gateway implements the use cases' Notion ports on top of Client. Every Notion
// request and response shape lives in this adapter.
type Gateway struct {
	Client *Client
}

var (
	_ port.NotionAuth      = (*Gateway)(nil)
	_ port.NotionWorkspace = (*Gateway)(nil)
)

func (g *Gateway) AuthorizeURL(redirectURI, state string) string {
	return g.Client.AuthorizeURL(redirectURI, state)
}

func (g *Gateway) ExchangeCode(ctx context.Context, code, redirectURI string) (*port.NotionGrant, error) {
	tok, err := g.Client.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	return grant(tok), nil
}

func (g *Gateway) RefreshToken(ctx context.Context, refreshToken string) (*port.NotionGrant, error) {
	tok, err := g.Client.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	return grant(tok), nil
}

func grant(t *TokenResponse) *port.NotionGrant {
	return &port.NotionGrant{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, BotID: t.BotID,
		WorkspaceID: t.WorkspaceID, WorkspaceName: t.WorkspaceName, WorkspaceIcon: t.WorkspaceIcon,
		DuplicatedTemplateID: t.DuplicatedTemplateID, ExpiresIn: t.ExpiresIn, OwnerUserID: t.Owner.User.ID,
		OwnerName: t.Owner.User.Name, OwnerEmail: t.Owner.User.Person.Email}
}

func (g *Gateway) SearchPages(ctx context.Context, token, query string) ([]port.PageRef, error) {
	pages, err := g.Client.SearchPages(ctx, token, query)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	out := make([]port.PageRef, len(pages))
	for i, p := range pages {
		out[i] = port.PageRef{ID: p.ID, Title: p.Title, URL: p.URL}
	}
	return out, nil
}
```

`backend/internal/adapter/notion/workspace.go` is today's `internal/setup/setup.go` resolution code, moved onto `*Gateway`. Bodies are unchanged except that `s.Notion.` becomes `g.Client.`, `Error{` becomes `domain.SetupError{`, `store.DBConfig` becomes `domain.DBConfig`, and the two public methods translate errors on the way out:

```go
package notion

import (
	"context"
	"fmt"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// ResolveTemplate finds "Reading Library" and "Daily Log" inside a duplicated
// template page and resolves their data sources and property ids (spec §2.11).
func (g *Gateway) ResolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error) {
	db, err := g.resolveTemplate(ctx, token, templateID)
	return db, translate(err, port.NotionNotFound)
}

// ProvisionDatabases creates both databases under parentPageID (spec §2.7). It is
// idempotent: a Library already under the parent is reused, and so is the first Daily
// Log whose Articles relation targets that Library; only the missing ones are created.
func (g *Gateway) ProvisionDatabases(ctx context.Context, token, parentPageID string) (domain.DBConfig, error) {
	db, err := g.provision(ctx, token, parentPageID)
	return db, translate(err, port.NotionNotFound)
}

func (g *Gateway) resolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error) {
	blocks, err := g.Client.ListBlockChildren(ctx, token, templateID)
	if isNotFound(err) {
		return domain.DBConfig{}, &domain.SetupError{Reason: "template_not_found"}
	}
	if err != nil {
		return domain.DBConfig{}, err
	}
	return g.resolve(ctx, token, first(childDatabaseIDs(blocks, LibraryTitle)), first(childDatabaseIDs(blocks, DailyTitle)))
}

func (g *Gateway) provision(ctx context.Context, token, parentPageID string) (domain.DBConfig, error) {
	blocks, err := g.Client.ListBlockChildren(ctx, token, parentPageID)
	if err != nil && !isNotFound(err) {
		return domain.DBConfig{}, err
	}

	libID, libDSID := first(childDatabaseIDs(blocks, LibraryTitle)), ""
	if libID == "" {
		lib, err := g.Client.CreateDatabase(ctx, token, parentPageID, LibraryTitle, libraryCreateSchema())
		if err != nil {
			return domain.DBConfig{}, err
		}
		if len(lib.DataSources) == 0 {
			return domain.DBConfig{}, fmt.Errorf("notion: created %s has no data source", LibraryTitle)
		}
		libID, libDSID = lib.ID, lib.DataSources[0].ID
	} else {
		lib, err := g.Client.GetDatabase(ctx, token, libID)
		if isNotFound(err) || (err == nil && len(lib.DataSources) == 0) {
			return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + LibraryTitle}
		}
		if err != nil {
			return domain.DBConfig{}, err
		}
		libDSID = lib.DataSources[0].ID
	}

	dailyID := ""
	for _, id := range childDatabaseIDs(blocks, DailyTitle) {
		ok, err := g.dailyLogRelatesTo(ctx, token, id, libDSID)
		if err != nil {
			return domain.DBConfig{}, err
		}
		if ok {
			dailyID = id
			break
		}
	}
	if dailyID == "" {
		daily, err := g.Client.CreateDatabase(ctx, token, parentPageID, DailyTitle, dailyCreateSchema(libDSID))
		if err != nil {
			return domain.DBConfig{}, err
		}
		dailyID = daily.ID
	}
	return g.resolve(ctx, token, libID, dailyID)
}

// childDatabaseIDs lists the ids of child_database blocks titled title, in order.
func childDatabaseIDs(blocks []Block, title string) []string {
	var ids []string
	for _, b := range blocks {
		if b.Type == "child_database" && b.ChildDatabase != nil && b.ChildDatabase.Title == title {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// dailyLogRelatesTo reports whether Daily Log dbID's Articles relation targets
// libDSID. A database or data source that no longer exists does not qualify.
func (g *Gateway) dailyLogRelatesTo(ctx context.Context, token, dbID, libDSID string) (bool, error) {
	db, err := g.Client.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ds, err := g.Client.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return relatesTo(ds, libDSID), nil
}

// relatesTo reports whether daily's Articles relation targets data source libDSID.
func relatesTo(daily *DataSource, libDSID string) bool {
	rel := daily.Properties[articlesProp].Relation
	return rel != nil && domain.SameID(rel.DataSourceID, libDSID)
}

func (g *Gateway) resolve(ctx context.Context, token, libDBID, dailyDBID string) (domain.DBConfig, error) {
	if libDBID == "" {
		return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + LibraryTitle}
	}
	if dailyDBID == "" {
		return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + DailyTitle}
	}
	libDS, libURL, libProps, err := g.resolveDatabase(ctx, token, libDBID, LibraryTitle, LibraryProps)
	if err != nil {
		return domain.DBConfig{}, err
	}
	dailyDS, _, dailyProps, err := g.resolveDatabase(ctx, token, dailyDBID, DailyTitle, DailyProps)
	if err != nil {
		return domain.DBConfig{}, err
	}
	// The Articles relation must point at this Library, not at one the user deleted.
	if !relatesTo(dailyDS, libDS.ID) {
		return domain.DBConfig{}, &domain.SetupError{Reason: "property_type:" + articlesProp}
	}
	for k, v := range dailyProps {
		libProps[k] = v
	}
	return domain.DBConfig{LibraryDBID: libDBID, LibraryDSID: libDS.ID, LibraryURL: libURL,
		DailyDBID: dailyDBID, DailyDSID: dailyDS.ID, Props: libProps}, nil
}

func (g *Gateway) resolveDatabase(ctx context.Context, token, dbID, title string, specs []PropSpec) (ds *DataSource, url string, props map[string]string, err error) {
	db, err := g.Client.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return nil, "", nil, &domain.SetupError{Reason: "database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	ds, err = g.Client.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return nil, "", nil, &domain.SetupError{Reason: "database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	props = map[string]string{}
	for _, spec := range specs {
		p, ok := ds.Properties[spec.Name]
		if !ok {
			return nil, "", nil, &domain.SetupError{Reason: "property_missing:" + spec.Name}
		}
		if p.Type != spec.Type {
			return nil, "", nil, &domain.SetupError{Reason: "property_type:" + spec.Name}
		}
		props[spec.Key] = p.ID
	}
	return ds, db.URL, props, nil
}
```

`backend/internal/setup/setup.go` is reduced to the resync. Delete everything except this:

```go
// Package setup re-enqueues an installation's backlog when it becomes active (T22).
// It is replaced by usecase/setup in Task 6.
package setup

import (
	"context"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/queue"
)

type Service struct {
	Store  *dynamo.Store
	Queue  queue.Sender
	Bucket time.Duration
	Now    func() time.Time
}

// Resync enqueues every ART/DAY whose version is ahead of Notion; full re-sends every
// ART/DAY (used when the installation's data sources changed, scenario 10).
func (s *Service) Resync(ctx context.Context, installationID string, full bool) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID, full)
	if err != nil {
		return err
	}
	msgs := make([]queue.Message, 0, len(hashes)+len(dates))
	for _, h := range hashes {
		msgs = append(msgs, queue.Article(installationID, h))
	}
	for _, d := range dates {
		msgs = append(msgs, queue.Day(installationID, d))
	}
	if len(msgs) == 0 {
		return nil
	}
	return s.Queue.Send(ctx, queue.Bucketed(s.Now(), s.Bucket, msgs...)...)
}
```

- [ ] **Step 6: Point the HTTP handlers at the gateway**

In `backend/internal/httpapi/app.go`:
- Add `gw *notion.Gateway` to `handlers`.
- In `New`, set `gw: &notion.Gateway{Client: d.Notion}`.
- The `setup.Service` literal becomes `&setup.Service{Store: d.Store, Queue: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now}`.

In the handlers:
- `handlers_oauth.go`:
  - `h.Notion.AuthorizeURL(` becomes `h.gw.AuthorizeURL(`.
  - `h.Notion.ExchangeCode(` becomes `h.gw.ExchangeCode(`.
  - The `*notion.TokenResponse` parameter of `resolveUser` and `upsertInstallation` becomes `*port.NotionGrant`. Rename the field accesses: `tok.Owner.User.ID` → `tok.OwnerUserID`, `tok.Owner.User.Name` → `tok.OwnerName`, `tok.Owner.User.Person.Email` → `tok.OwnerEmail`.
  - `h.setup.ResolveTemplate(` becomes `h.gw.ResolveTemplate(`, and `setup.Reason(` becomes `domain.SetupReason(`.
- `handlers_setup.go`:
  - `h.Notion.SearchPages(` becomes `h.gw.SearchPages(`. Its result is `[]port.PageRef`, which has no JSON tags, so map it to the same JSON before replying:

```go
type pageView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}
// in setupPages, after the call:
views := make([]pageView, len(pages))
for i, p := range pages {
	views[i] = pageView{ID: p.ID, Title: p.Title, URL: p.URL}
}
return c.JSON(http.StatusOK, map[string]any{"pages": views})
```

  - `h.setup.CreateDatabases(` becomes `h.gw.ProvisionDatabases(`, and `var se *setup.Error` becomes `var se *domain.SetupError`.
  - Rewrite `notionErr` on top of port kinds, keeping the same mapping:

```go
// notionErr maps Notion failures in API calls to §2.0 errors.
func notionErr(err error) error {
	e, ok := port.NotionErrorOf(err)
	if !ok {
		return err
	}
	switch {
	case e.Kind == port.NotionUnauthorized:
		return errConflict("Notion authorization expired; reconnect")
	case e.Kind == port.NotionNotFound:
		return errNotFound("Notion object not found or not shared with the integration")
	case e.Kind == port.NotionRejected && e.Status == http.StatusBadRequest:
		return errInvalid("Notion rejected the request: %s", e.Message)
	}
	return err
}
```

Run `go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/httpapi`.

- [ ] **Step 7: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`. `adapter/notion` now carries the workspace tests that moved from `setup`, plus `TestTranslateKinds`.

- [ ] **Step 8: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): add use-case ports and a Notion gateway owning template and database provisioning"
```

---
### Task 4: `queue` → `adapter/queue` with the `SyncQueue` port, a job consumer, and R2

**Files:**
- Move: `internal/queue` → `internal/adapter/queue` (package `queue` unchanged)
- Create: `internal/usecase/port/queue.go`, `internal/adapter/queue/syncqueue.go`, `internal/adapter/queue/consumer.go`
- Modify: `internal/syncsvc/service.go`, `internal/setup/setup.go`, `internal/httpapi/app.go`, `internal/httpapi/handlers_account.go`, and the tests constructing those services
- Test: `internal/adapter/queue/syncqueue_test.go`, `internal/adapter/queue/consumer_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  - `port.JobType` with the values `JobArticle = "article"`, `JobDay = "day"` and `JobPurge = "purge"`.
  - `port.Job{Type JobType; InstallationID, URLHash, Date, UserID string}`.
  - `port.SyncQueue`:
    - `EnqueueSync(ctx, inst string, hashes, dates []string) error`: debounced bucket dedup, articles before days.
    - `EnqueueResync(ctx, inst string, hashes, dates []string) error`: unique dedup ids, so it is never swallowed by a push's bucket id (R2).
    - `Requeue(ctx, job Job) error`: unique dedup id.
    - `EnqueuePurge(ctx, uid, inst string) error`.
  - `port.JobHandler` interface `HandleJob(ctx, Job) error`; `port.RetryAfterError{Delay time.Duration; Err error}`.
  - `queue.SyncQueue{Sender queue.Sender; Bucket time.Duration; Now func() time.Time}` implements `port.SyncQueue`.
  - `queue.VisibilityChanger` interface; `*queue.SQS` and `*queue.Local` implement it.
  - `queue.Consumer{Handler port.JobHandler; Visibility VisibilityChanger; Log *slog.Logger}` with `HandleSQS(ctx, events.SQSEvent) (events.SQSEventResponse, error)`. It decodes each body into a `port.Job`, fails the first failing job and every later one (C21), and on `*port.RetryAfterError` changes visibility.

- [ ] **Step 1: Move the package**

```bash
cd backend
git mv internal/queue internal/adapter/queue
FILES=$(grep -rl 'reading-tracker/backend/internal/queue"' --include='*.go' .)
perl -pi -e 's#reading-tracker/backend/internal/queue"#reading-tracker/backend/internal/adapter/queue"#g' $FILES
go build ./... && go test ./internal/adapter/queue/...
```

Expected: the build passes and the queue tests pass.

- [ ] **Step 2: Write the port**

`backend/internal/usecase/port/queue.go`:

```go
package port

import (
	"context"
	"fmt"
	"time"
)

type JobType string

const (
	JobArticle JobType = "article"
	JobDay     JobType = "day"
	JobPurge   JobType = "purge"
)

// Job is one unit of Notion-sync work (spec §2.12).
type Job struct {
	Type           JobType
	InstallationID string
	URLHash        string
	Date           string
	UserID         string
}

// SyncQueue enqueues Notion-sync work. Every job of an installation is processed in
// order, one at a time (C4).
type SyncQueue interface {
	// EnqueueSync debounces: jobs for the same key within one bucket collapse (C5).
	EnqueueSync(ctx context.Context, inst string, hashes, dates []string) error
	// EnqueueResync is never collapsed with pushes, so a backlog sent right after a push
	// in the same bucket is still delivered (R2). Writes are absolute, so extra runs are harmless.
	EnqueueResync(ctx context.Context, inst string, hashes, dates []string) error
	// Requeue re-delivers one job (e.g. after its page was cleared).
	Requeue(ctx context.Context, job Job) error
	EnqueuePurge(ctx context.Context, uid, inst string) error
}

// JobHandler processes one job. nil acks it; *RetryAfterError backs it off; any other
// error fails it for redelivery.
type JobHandler interface {
	HandleJob(ctx context.Context, job Job) error
}

// RetryAfterError asks the queue to hide the job for Delay before redelivery (spec §2.15).
type RetryAfterError struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string { return fmt.Sprintf("retry after %s: %v", e.Delay, e.Err) }
func (e *RetryAfterError) Unwrap() error { return e.Err }
```

- [ ] **Step 3: Write the failing tests**

`backend/internal/adapter/queue/syncqueue_test.go`:

```go
package queue

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

func newSyncQueue(rec *Recorder, now time.Time) *SyncQueue {
	return &SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return now }}
}

func TestEnqueueSyncDebouncesArticlesThenDays(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	if err := q.EnqueueSync(context.Background(), bot, []string{hash}, []string{"2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Article(bot, hash) || sent[1].Message != Day(bot, "2026-10-06") ||
		sent[0].DedupID != "article#"+bot+"#"+hash+"#1000" {
		t.Fatalf("sent %+v", sent)
	}
	if err := q.EnqueueSync(context.Background(), bot, nil, nil); err != nil || len(rec.Drain()) != 0 {
		t.Fatal("nothing to send must send nothing")
	}
}

// Review focus 5 (adapter half) / R2: resync ids never equal a push's bucket ids.
func TestResyncIDsNeverCollideWithPushIDs(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.EnqueueSync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	must(q.EnqueueResync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	sent := rec.Drain()
	if len(sent) != 4 {
		t.Fatalf("sent %d", len(sent))
	}
	seen := map[string]bool{}
	for _, o := range sent {
		if seen[o.DedupID] {
			t.Fatalf("duplicate dedup id %q", o.DedupID)
		}
		seen[o.DedupID] = true
		if len(o.DedupID) > 128 {
			t.Fatalf("dedup id too long: %q", o.DedupID)
		}
	}
}

func TestResyncAfterPushIsDeliveredByLocalQueue(t *testing.T) {
	var delivered atomic.Int32
	done := make(chan struct{}, 4)
	l := &Local{Delay: time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handle: func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
			delivered.Add(1)
			done <- struct{}{}
			return events.SQSEventResponse{}, nil
		}}
	q := &SyncQueue{Sender: l, Bucket: 120 * time.Second, Now: time.Now}
	ctx := context.Background()
	q.EnqueueSync(ctx, bot, []string{hash}, nil)
	q.EnqueueResync(ctx, bot, []string{hash}, nil)
	<-done
	<-done
	if delivered.Load() != 2 {
		t.Fatalf("delivered %d, want 2", delivered.Load())
	}
}

func TestRequeueAndPurge(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(42, 0))
	q.Requeue(context.Background(), port.Job{Type: port.JobDay, InstallationID: bot, Date: "2026-10-06"})
	q.EnqueuePurge(context.Background(), "u1", bot)
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Day(bot, "2026-10-06") || !strings.Contains(sent[0].DedupID, "#r") ||
		sent[1].Message != Purge("u1", bot) || sent[1].DedupID != "purge#u1#42" {
		t.Fatalf("sent %+v", sent)
	}
}
```

`backend/internal/adapter/queue/consumer_test.go`:

```go
package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

type handlerFunc func(context.Context, port.Job) error

func (f handlerFunc) HandleJob(ctx context.Context, j port.Job) error { return f(ctx, j) }

type fakeVisibility struct{ calls []time.Duration }

func (f *fakeVisibility) ChangeVisibility(_ context.Context, _ string, d time.Duration) error {
	f.calls = append(f.calls, d)
	return nil
}

func event(t *testing.T, msgs ...Message) events.SQSEvent {
	var ev events.SQSEvent
	for i, m := range msgs {
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		ev.Records = append(ev.Records, events.SQSMessage{MessageId: fmt.Sprintf("m%d", i), ReceiptHandle: fmt.Sprintf("rh%d", i), Body: string(body)})
	}
	return ev
}

// C21: after the first failure, it and every later message are reported failed.
func TestConsumerFailsRestOfBatch(t *testing.T) {
	var ran []string
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(_ context.Context, j port.Job) error {
			ran = append(ran, j.URLHash)
			if j.URLHash == "bad" {
				return errors.New("boom")
			}
			return nil
		})}
	resp, err := c.HandleSQS(context.Background(), event(t, Article(bot, "ok"), Article(bot, "bad"), Article(bot, "later")))
	if err != nil || len(resp.BatchItemFailures) != 2 || resp.BatchItemFailures[0].ItemIdentifier != "m1" ||
		resp.BatchItemFailures[1].ItemIdentifier != "m2" || len(ran) != 2 {
		t.Fatalf("resp %+v ran %v", resp, ran)
	}
}

func TestConsumerBacksOffRetryAfter(t *testing.T) {
	vis := &fakeVisibility{}
	c := &Consumer{Visibility: vis, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(context.Context, port.Job) error {
			return &port.RetryAfterError{Delay: 7 * time.Second, Err: errors.New("rate limited")}
		})}
	resp, _ := c.HandleSQS(context.Background(), event(t, Day(bot, "2026-10-06")))
	if len(resp.BatchItemFailures) != 1 || len(vis.calls) != 1 || vis.calls[0] != 7*time.Second {
		t.Fatalf("resp %+v vis %v", resp, vis.calls)
	}
}

func TestConsumerPassesJobFields(t *testing.T) {
	var got port.Job
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(_ context.Context, j port.Job) error { got = j; return nil })}
	c.HandleSQS(context.Background(), event(t, Purge("u1", bot)))
	if got != (port.Job{Type: port.JobPurge, InstallationID: bot, UserID: "u1"}) {
		t.Fatalf("job %+v", got)
	}
}

func TestConsumerAcksUndecodable(t *testing.T) {
	logs := &bytes.Buffer{}
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(logs, nil)),
		Handler: handlerFunc(func(context.Context, port.Job) error { t.Fatal("must not run"); return nil })}
	resp, err := c.HandleSQS(context.Background(), events.SQSEvent{Records: []events.SQSMessage{{MessageId: "m0", Body: "not json"}}})
	if err != nil || len(resp.BatchItemFailures) != 0 || !bytes.Contains(logs.Bytes(), []byte("undecodable")) {
		t.Fatalf("resp %+v logs %s", resp, logs)
	}
}
```

- [ ] **Step 4: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/adapter/queue/...`
Expected: FAIL to compile with `undefined: SyncQueue` and `undefined: Consumer`.

- [ ] **Step 5: Implement the adapter**

`backend/internal/adapter/queue/syncqueue.go`:

```go
package queue

import (
	"context"
	"time"

	"reading-tracker/backend/internal/usecase/port"
)

// SyncQueue implements port.SyncQueue on any Sender (SQS, Local or Recorder) and owns
// the dedup-id policy: bucketed for pushes (C5), unique for resync and requeue (R2).
type SyncQueue struct {
	Sender Sender
	Bucket time.Duration
	Now    func() time.Time
}

var _ port.SyncQueue = (*SyncQueue)(nil)

func (q *SyncQueue) EnqueueSync(ctx context.Context, inst string, hashes, dates []string) error {
	msgs := syncMessages(inst, hashes, dates)
	if len(msgs) == 0 {
		return nil
	}
	return q.Sender.Send(ctx, Bucketed(q.Now(), q.Bucket, msgs...)...)
}

func (q *SyncQueue) EnqueueResync(ctx context.Context, inst string, hashes, dates []string) error {
	msgs := syncMessages(inst, hashes, dates)
	if len(msgs) == 0 {
		return nil
	}
	now := q.Now()
	out := make([]Outgoing, len(msgs))
	for i, m := range msgs {
		out[i] = Outgoing{Message: m, DedupID: m.RequeueDedupID(now)}
	}
	return q.Sender.Send(ctx, out...)
}

func (q *SyncQueue) Requeue(ctx context.Context, job port.Job) error {
	m := messageFor(job)
	return q.Sender.Send(ctx, Outgoing{Message: m, DedupID: m.RequeueDedupID(q.Now())})
}

func (q *SyncQueue) EnqueuePurge(ctx context.Context, uid, inst string) error {
	m := Purge(uid, inst)
	return q.Sender.Send(ctx, Outgoing{Message: m, DedupID: m.DedupID(q.Now(), q.Bucket)})
}

// syncMessages lists article messages before day messages, so a day page can relate
// to article pages created from the same batch.
func syncMessages(inst string, hashes, dates []string) []Message {
	msgs := make([]Message, 0, len(hashes)+len(dates))
	for _, h := range hashes {
		msgs = append(msgs, Article(inst, h))
	}
	for _, d := range dates {
		msgs = append(msgs, Day(inst, d))
	}
	return msgs
}

func messageFor(j port.Job) Message {
	return Message{Type: Type(j.Type), InstallationID: j.InstallationID, URLHash: j.URLHash, Date: j.Date, UserID: j.UserID}
}

func (m Message) job() port.Job {
	return port.Job{Type: port.JobType(m.Type), InstallationID: m.InstallationID, URLHash: m.URLHash, Date: m.Date, UserID: m.UserID}
}
```

`backend/internal/adapter/queue/consumer.go`:

```go
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

// VisibilityChanger hides a message for a back-off period (Notion 429, spec §2.15).
type VisibilityChanger interface {
	ChangeVisibility(ctx context.Context, receiptHandle string, d time.Duration) error
}

var (
	_ VisibilityChanger = (*SQS)(nil)
	_ VisibilityChanger = (*Local)(nil)
)

// Consumer turns an SQS FIFO batch into jobs, in order. On the first failure it
// reports that message and every later one as failed (C21), so group order holds.
type Consumer struct {
	Handler    port.JobHandler
	Visibility VisibilityChanger
	Log        *slog.Logger
}

func (c *Consumer) HandleSQS(ctx context.Context, ev events.SQSEvent) (events.SQSEventResponse, error) {
	var resp events.SQSEventResponse
	for i, rec := range ev.Records {
		var m Message
		if err := json.Unmarshal([]byte(rec.Body), &m); err != nil {
			c.Log.ErrorContext(ctx, "queue: undecodable message dropped", "message_id", rec.MessageId)
			continue
		}
		err := c.Handler.HandleJob(ctx, m.job())
		if err == nil {
			continue
		}
		c.Log.WarnContext(ctx, "queue: message failed", "message_id", rec.MessageId, "err", err)
		var ra *port.RetryAfterError
		if errors.As(err, &ra) {
			if verr := c.Visibility.ChangeVisibility(ctx, rec.ReceiptHandle, ra.Delay); verr != nil {
				c.Log.WarnContext(ctx, "queue: change visibility failed", "err", verr)
			}
		}
		for _, r := range ev.Records[i:] {
			resp.BatchItemFailures = append(resp.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: r.MessageId})
		}
		break
	}
	return resp, nil
}
```

- [ ] **Step 6: Route existing enqueues through `SyncQueue`**

`backend/internal/syncsvc/service.go`:
- `Service` becomes `{Store Pusher; Queue port.SyncQueue; Now func() time.Time}`. Delete the `Bucket` field.
- At the end of `Push`, replace the `touched.messages` block with:

```go
	if len(touched.hashes)+len(touched.dates) > 0 {
		if err := s.Queue.EnqueueSync(ctx, installationID, touched.hashes, touched.dates); err != nil {
			return nil, err
		}
	}
```

- Delete `touchedKeys.messages` and the `queue` import.
- In `internal/syncsvc/service_test.go`, `newService` builds `Queue: &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return now }}`. `TestPushDateUsesRequestTZ` reassigns `svc.Now`, so also set the SyncQueue's `Now` to the same closure; hold the queue in a variable and assign both. The assertions are unchanged.

`backend/internal/setup/setup.go`:
- `Service` becomes `{Store *dynamo.Store; Queue port.SyncQueue}`.
- `Resync` becomes:

```go
func (s *Service) Resync(ctx context.Context, installationID string, full bool) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID, full)
	if err != nil {
		return err
	}
	return s.Queue.EnqueueResync(ctx, installationID, hashes, dates)
}
```

In `internal/setup/setup_test.go`, the fixture builds `Queue: &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return testNow }}`.

`backend/internal/httpapi/app.go`:
- Add `sq *queue.SyncQueue` to `handlers` and build it in `New`: `sq := &queue.SyncQueue{Sender: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now}`.
- Pass `Queue: sq` to `setup.Service{Store: d.Store, Queue: sq}` and `syncsvc.Service{Store: d.Store, Queue: sq, Now: d.Now}`.

`backend/internal/httpapi/handlers_account.go`: in `deleteMe`, replace the two-line purge `Send` with:

```go
	if err := h.sq.EnqueuePurge(ctx, p.UserID, p.InstallationID()); err != nil {
		return err
	}
```

Remove the now-unused `now` variable if the compiler reports it.

The worker keeps its own `HandleSQS` and requeue until Task 5 deletes it.

- [ ] **Step 7: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`. `adapter/queue` adds 8 tests, and the syncsvc, setup and httpapi tests pass unchanged.

- [ ] **Step 8: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): move queue to adapter/queue with a SyncQueue port and job consumer; resync no longer deduped against pushes (R2)"
```

---
### Task 5: `usecase/notionsync` + Notion pages gateway, replacing `worker`; fixes R1

**Files:**
- Create: `internal/usecase/port/crypto.go`, `internal/usecase/notionsync/{service.go,session.go,article.go,day.go,purge.go}`, `internal/adapter/notion/pages.go`, `internal/adapter/dynamo/purge.go`
- Move: `internal/worker/properties.go` → `internal/adapter/notion/properties.go`
- Modify:
  - `internal/usecase/port/notion.go` (add `Page`, `NotionPages`) and `internal/usecase/port/repositories.go` (R1 signatures, `DataPurger`)
  - `internal/domain/installation.go` (add `Databases()`, `LibraryName`, `DailyLogName`)
  - `internal/adapter/dynamo/articles.go`, `days.go` (R1)
  - `internal/adapter/notion/schema.go` (titles from domain)
  - `internal/app/app.go`, `cmd/worker/main.go`, `cmd/local/main.go`, `internal/e2e/e2e_test.go`
- Move tests: `internal/worker/{env,article,day,purge,worker}_test.go` → `internal/usecase/notionsync/{env,article,day,purge,service}_test.go`
- Delete: `internal/worker`

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces:
  - `port.Sealer` interface: `Seal(plain, aad []byte) (string, error)` and `Open(sealed string, aad []byte) ([]byte, error)`. `*crypto.Keyring` satisfies it.
  - `port.Page{ID, URL string}`.
  - `port.NotionPages`, with these methods:
    - `FindArticlePage(ctx, token string, db domain.DBConfig, canonicalURL string) (*Page, error)`, which returns nil when there is no page.
    - `CreateArticlePage(ctx, token string, db domain.DBConfig, a domain.Article, completed bool) (*Page, error)`
    - `UpdateArticlePage(ctx, token string, db domain.DBConfig, pageID string, a domain.Article, completed bool) error`
    - `FindDayPage(ctx, token string, db domain.DBConfig, date string) (*Page, error)`
    - `CreateDayPage(ctx, token string, db domain.DBConfig, d domain.Day, articlePageIDs []string) (*Page, error)`
    - `UpdateDayPage(ctx, token string, db domain.DBConfig, pageID string, d domain.Day, articlePageIDs []string) error`
  - `port.DataPurger`: `DeleteInstallationData(ctx, botID string) error` and `DeleteUserData(ctx, uid string) error`. `*dynamo.Store` implements it.
  - `port.Reading` changes for R1: `SetArticlePage(ctx, inst, hash, pageID, url, dataSourceID string) error` and `SetDayPage(ctx, inst, date, pageID, dataSourceID string) error`.
  - Domain additions: `domain.Installation.Databases() domain.DBConfig`, `domain.LibraryName = "Reading Library"` and `domain.DailyLogName = "Daily Log"`.
  - `notionsync.Service{Installations port.Installations; Reading port.Reading; Users port.Users; Devices port.Devices; Purger port.DataPurger; Pages port.NotionPages; Auth port.NotionAuth; Sealer port.Sealer; Queue port.SyncQueue; Policy domain.SyncPolicy; Now func() time.Time; Jitter func() time.Duration; Sleep func(time.Duration); Log *slog.Logger}`, which implements `port.JobHandler`.
  - `app.WorkerDeps{Store *dynamo.Store; Notion *notion.Client; Keys *crypto.Keyring; Queue queue.Sender; Visibility queue.VisibilityChanger; Tracking config.Tracking; Now func() time.Time; Jitter func() time.Duration; Sleep func(time.Duration); Log *slog.Logger}`.
  - `app.NewWorker(WorkerDeps) *queue.Consumer`.
  - `app.Worker(cfg, st, q, vis, log) *queue.Consumer`, a paced client with 350 ms spacing (C22).

- [ ] **Step 1: Extend the domain, ports and DynamoDB adapter**

Append to `backend/internal/domain/installation.go`:

```go
// Database names the user sees and setup_error reasons use (spec §2.11, §3.3).
const (
	LibraryName  = "Reading Library"
	DailyLogName = "Daily Log"
)

// Databases is the installation's current Notion target.
func (i Installation) Databases() DBConfig {
	return DBConfig{LibraryDBID: i.LibraryDBID, LibraryDSID: i.LibraryDSID, LibraryURL: i.LibraryURL,
		DailyDBID: i.DailyDBID, DailyDSID: i.DailyDSID, Props: i.Props}
}
```

In `backend/internal/adapter/notion/schema.go`, define the titles from the domain:

```go
const (
	LibraryTitle = domain.LibraryName
	DailyTitle   = domain.DailyLogName
)
```

`backend/internal/usecase/port/crypto.go`:

```go
package port

// Sealer encrypts Notion tokens at rest with AAD = bot_id (C18). *crypto.Keyring
// implements it.
type Sealer interface {
	Seal(plain, aad []byte) (string, error)
	Open(sealed string, aad []byte) ([]byte, error)
}
```

Append to `backend/internal/usecase/port/notion.go`:

```go
// Page is a Notion page the sync wrote or adopted.
type Page struct {
	ID  string
	URL string
}

// NotionPages writes the Reading Library and Daily Log rows (spec §2.13, §2.14).
// It writes only server-owned properties, by stored property id (C1, C9). Find and
// Create return NotionDataSourceMissing on a 404. Update returns NotionPageGone when
// the page is missing, archived, trashed, or now lives in another data source.
type NotionPages interface {
	FindArticlePage(ctx context.Context, token string, db domain.DBConfig, canonicalURL string) (*Page, error)
	CreateArticlePage(ctx context.Context, token string, db domain.DBConfig, a domain.Article, completed bool) (*Page, error)
	UpdateArticlePage(ctx context.Context, token string, db domain.DBConfig, pageID string, a domain.Article, completed bool) error
	FindDayPage(ctx context.Context, token string, db domain.DBConfig, date string) (*Page, error)
	CreateDayPage(ctx context.Context, token string, db domain.DBConfig, d domain.Day, articlePageIDs []string) (*Page, error)
	UpdateDayPage(ctx context.Context, token string, db domain.DBConfig, pageID string, d domain.Day, articlePageIDs []string) error
}
```

In `backend/internal/usecase/port/repositories.go`, change the two `Reading` methods and add `DataPurger`:

```go
	SetArticlePage(ctx context.Context, inst, hash, pageID, url, dataSourceID string) error
	SetDayPage(ctx context.Context, inst, date, pageID, dataSourceID string) error
```

```go
// DataPurger deletes every stored item of an installation or user (spec §2.17).
type DataPurger interface {
	DeleteInstallationData(ctx context.Context, botID string) error
	DeleteUserData(ctx context.Context, uid string) error
}
```

In `backend/internal/adapter/dynamo/articles.go`, `SetArticlePage` also stores the data source (R1). `ClearArticlePage` removes it.

```go
func (s *Store) SetArticlePage(ctx context.Context, inst, hash, pageID, url, dataSourceID string) error {
	return s.update(ctx, InstPK(inst), ArticleSK(hash), "SET #p = :p, #u = :u, #d = :d", "attribute_exists(pk)",
		map[string]string{"#p": "notion_page_id", "#u": "notion_url", "#d": "notion_ds_id"},
		map[string]types.AttributeValue{":p": str(pageID), ":u": str(url), ":d": str(dataSourceID)})
}

func (s *Store) ClearArticlePage(ctx context.Context, inst, hash string) error {
	return s.update(ctx, InstPK(inst), ArticleSK(hash), "REMOVE #p, #u, #d", "",
		map[string]string{"#p": "notion_page_id", "#u": "notion_url", "#d": "notion_ds_id"}, nil)
}
```

In `backend/internal/adapter/dynamo/days.go`:

```go
func (s *Store) SetDayPage(ctx context.Context, inst, date, pageID, dataSourceID string) error {
	return s.update(ctx, InstPK(inst), DaySK(date), "SET #p = :p, #d = :d", "attribute_exists(pk)",
		map[string]string{"#p": "notion_page_id", "#d": "notion_ds_id"},
		map[string]types.AttributeValue{":p": str(pageID), ":d": str(dataSourceID)})
}

func (s *Store) ClearDayPage(ctx context.Context, inst, date string) error {
	return s.update(ctx, InstPK(inst), DaySK(date), "REMOVE #p, #d", "",
		map[string]string{"#p": "notion_page_id", "#d": "notion_ds_id"}, nil)
}
```

`backend/internal/adapter/dynamo/purge.go`:

```go
package dynamo

import "context"

// DeleteInstallationData deletes every INST#<botID> item (spec §2.17 purge).
func (s *Store) DeleteInstallationData(ctx context.Context, botID string) error {
	return s.deletePartition(ctx, InstPK(botID))
}

// DeleteUserData deletes every USER#<uid> item (spec §2.17 purge).
func (s *Store) DeleteUserData(ctx context.Context, uid string) error {
	return s.deletePartition(ctx, UserPK(uid))
}

func (s *Store) deletePartition(ctx context.Context, pk string) error {
	keys, err := s.PartitionKeys(ctx, pk)
	if err != nil {
		return err
	}
	return s.BatchDelete(ctx, keys)
}
```

Add `_ port.DataPurger = (*Store)(nil)` to the assertion block in `store.go`. Update the existing store test `TestArticleAndDayPageLinks`, now in `internal/adapter/dynamo/push_test.go`:
- Call `SetArticlePage(ctx, "bot-1", h, "page-1", "https://notion.so/page-1", "ds-1")` and `SetDayPage(ctx, "bot-1", "2026-10-06", "day-page-1", "ds-2")`.
- Additionally assert `a.NotionDataSourceID == "ds-1"` and `d.NotionDataSourceID == "ds-2"` after setting.
- Assert both are `""` after clearing.

Then update every other caller of the two methods to pass a data source id:

```bash
cd backend && grep -rn 'SetArticlePage(\|SetDayPage(' --include='*.go' internal | grep -v 'internal/adapter/dynamo/\(articles\|days\).go'
```

Test seeds such as `internal/httpapi/articles_test.go`'s `SetArticlePage(ctx, testBotID, …, "page-1", "https://www.notion.so/page-1")` gain a trailing `"ds-1"`. Production callers are the `notionsync` code in Step 6.

- [ ] **Step 2: Write the Notion pages gateway**

```bash
cd backend
git mv internal/worker/properties.go internal/adapter/notion/properties.go
perl -pi -e 's/^package worker$/package notion/' internal/adapter/notion/properties.go
```

Edit `internal/adapter/notion/properties.go`:
- Delete `isCompleted` (now `domain.SyncPolicy.IsCompleted`) and `roundMinutes`; use `domain.RoundMinutes` instead.
- Change `*store.Article` / `*domain.Article` parameters to `domain.Article`.
- Fix the imports to `domain` and `platform/textutil`.

The resulting functions are `articleMetrics(p map[string]string, a domain.Article, completed bool) map[string]any`, `articleCreateBody(dsID string, p map[string]string, a domain.Article, completed bool) map[string]any`, `richText`, `dateValue`, `selectName` and the limit constants `notionMaxURL`, `notionMaxText`, `notionMaxSelect` and `maxRelations`. Their bodies are unchanged except `roundMinutes(` → `domain.RoundMinutes(`.

`backend/internal/adapter/notion/pages.go`:

```go
package notion

import (
	"context"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

var _ port.NotionPages = (*Gateway)(nil)

// FindArticlePage looks a Library row up by URL. URLs Notion cannot store (> 2000
// chars) are never queried.
func (g *Gateway) FindArticlePage(ctx context.Context, token string, db domain.DBConfig, canonicalURL string) (*port.Page, error) {
	if len(canonicalURL) > notionMaxURL {
		return nil, nil
	}
	return g.findOne(ctx, token, db.LibraryDSID, map[string]any{
		"filter":    map[string]any{"property": db.Props["url"], "url": map[string]any{"equals": canonicalURL}},
		"page_size": 1,
	})
}

func (g *Gateway) CreateArticlePage(ctx context.Context, token string, db domain.DBConfig, a domain.Article, completed bool) (*port.Page, error) {
	p, err := g.Client.CreatePage(ctx, token, articleCreateBody(db.LibraryDSID, db.Props, a, completed))
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	return &port.Page{ID: p.ID, URL: p.URL}, nil
}

// UpdateArticlePage PATCHes the server-owned metric properties only (§2.13 step 4).
func (g *Gateway) UpdateArticlePage(ctx context.Context, token string, db domain.DBConfig, pageID string, a domain.Article, completed bool) error {
	return g.updatePage(ctx, token, pageID, db.LibraryDSID, articleMetrics(db.Props, a, completed))
}

func (g *Gateway) FindDayPage(ctx context.Context, token string, db domain.DBConfig, date string) (*port.Page, error) {
	return g.findOne(ctx, token, db.DailyDSID, map[string]any{
		"filter":    map[string]any{"property": db.Props["day_date"], "date": map[string]any{"equals": date}},
		"page_size": 1,
	})
}

func (g *Gateway) CreateDayPage(ctx context.Context, token string, db domain.DBConfig, d domain.Day, articlePageIDs []string) (*port.Page, error) {
	props := map[string]any{
		db.Props["day_title"]: map[string]any{"title": richText(d.Date)},
		db.Props["day_date"]:  dateValue(d.Date),
	}
	for k, v := range dayMetrics(db.Props, d, articlePageIDs) {
		props[k] = v
	}
	p, err := g.Client.CreatePage(ctx, token, map[string]any{
		"parent":     map[string]any{"type": "data_source_id", "data_source_id": db.DailyDSID},
		"properties": props,
	})
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	return &port.Page{ID: p.ID, URL: p.URL}, nil
}

func (g *Gateway) UpdateDayPage(ctx context.Context, token string, db domain.DBConfig, pageID string, d domain.Day, articlePageIDs []string) error {
	return g.updatePage(ctx, token, pageID, db.DailyDSID, dayMetrics(db.Props, d, articlePageIDs))
}

func (g *Gateway) findOne(ctx context.Context, token, dsID string, body map[string]any) (*port.Page, error) {
	res, err := g.Client.QueryDataSource(ctx, token, dsID, body)
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return &port.Page{ID: res.Results[0].ID, URL: res.Results[0].URL}, nil
}

// updatePage PATCHes a row of data source dsID. A page that comes back archived or
// trashed, or whose parent is another data source (the installation moved to new
// databases), is gone: the caller re-creates it in dsID (scenario 10).
func (g *Gateway) updatePage(ctx context.Context, token, pageID, dsID string, props map[string]any) error {
	p, err := g.Client.UpdatePage(ctx, token, pageID, props)
	if err != nil {
		return translate(err, port.NotionPageGone)
	}
	if p.Archived || p.InTrash || (p.Parent.DataSourceID != "" && !domain.SameID(p.Parent.DataSourceID, dsID)) {
		return &port.NotionError{Kind: port.NotionPageGone, Message: "page archived or moved"}
	}
	return nil
}

// dayMetrics are the Daily Log's server-owned properties (§2.14).
func dayMetrics(p map[string]string, d domain.Day, pageIDs []string) map[string]any {
	rel := make([]any, len(pageIDs))
	for i, id := range pageIDs {
		rel[i] = map[string]any{"id": id}
	}
	return map[string]any{
		p["day_minutes"]:  map[string]any{"number": domain.RoundMinutes(d.Seconds)},
		p["day_articles"]: map[string]any{"relation": rel},
	}
}
```

- [ ] **Step 3: Port the worker tests to the use case (they will fail to compile)**

```bash
cd backend
mkdir -p internal/usecase/notionsync
for f in env article day purge; do git mv internal/worker/${f}_test.go internal/usecase/notionsync/${f}_test.go; done
git mv internal/worker/worker_test.go internal/usecase/notionsync/service_test.go
cd internal/usecase/notionsync
perl -pi -e 's/^package worker$/package notionsync/; s/\be\.w\.openSession\(/e.s.openSession(/g; s/\be\.w\.Store = dynamo\.New\(/e.s.Installations = dynamo.New(/g' *_test.go
```

Replace the constructor half of `env_test.go`. Keep the helpers `seedInstallation`, `seedInstallationWith`, `read`, `readMeta`, `handle`, `pid`, `num`, `must`, `b64` and the constants. Change `wenv` and `newEnvWithKeys` to:

```go
type wenv struct {
	t      *testing.T
	s      *Service
	c      *queue.Consumer
	gw     *notion.Gateway
	db     *dynamotest.Env
	ns     *notiontest.Server
	q      *queue.Recorder
	vis    *fakeVisibility
	tpl    notiontest.Template
	keys   *crypto.Keyring
	now    time.Time
	logs   *bytes.Buffer
	sleeps []time.Duration
	segs   int
}

func newEnvWithKeys(t *testing.T, keys string) *wenv {
	t.Helper()
	kr, err := crypto.ParseKeyring(keys)
	must(t, err)
	e := &wenv{t: t, db: dynamotest.New(t), ns: notiontest.New(t), q: &queue.Recorder{}, vis: &fakeVisibility{},
		keys: kr, now: testNow, logs: &bytes.Buffer{}}
	e.tpl = e.ns.AddTemplate("tpl-1")
	e.gw = &notion.Gateway{Client: &notion.Client{BaseURL: e.ns.URL, Version: "2025-09-03", ClientID: "cid", ClientSecret: "csecret"}}
	clock := func() time.Time { return e.now }
	log := slog.New(slog.NewTextHandler(e.logs, nil))
	e.s = &Service{
		Installations: e.db.Store, Reading: e.db.Store, Users: e.db.Store, Devices: e.db.Store, Purger: e.db.Store,
		Pages: e.gw, Auth: e.gw, Sealer: kr,
		Queue:  &queue.SyncQueue{Sender: e.q, Bucket: 120 * time.Second, Now: clock},
		Policy: domain.SyncPolicy{MinCreateSeconds: 30, CompleteProgress: 0.9, CompleteTimeRatio: 0.3, WordsPerMinute: 230},
		Now:    clock,
		Jitter: func() time.Duration { return 2 * time.Second },
		Sleep:  func(d time.Duration) { e.sleeps = append(e.sleeps, d) },
		Log:    log,
	}
	e.c = &queue.Consumer{Handler: e.s, Visibility: e.vis, Log: log}
	return e
}
```

The `SyncPolicy` values are the embedded `/v1/config` defaults (spec §2.10). Use them literally here; production wiring reads them from config.

In `handle`, replace `e.w.HandleSQS(ctx, ev)` with `e.c.HandleSQS(ctx, ev)`. `fakeVisibility` stays.

Fix the imports with goimports. The new import paths are `adapter/dynamo/dynamotest`, `adapter/notion`, `adapter/notion/notiontest`, `adapter/queue`, `domain` and `platform/crypto`.

Then adjust `service_test.go`, the old `worker_test.go`:
- Delete `TestFirstFailureFailsRestOfBatch` and `TestUndecodableAndUnknownMessagesAreAcked`. `adapter/queue`'s `TestConsumerFailsRestOfBatch` and `TestConsumerAcksUndecodable` now cover them; list both in the report. Then add the unknown-type case below.
- The error-classification tests stop overriding `e.w.handlers`. Each calls `classify` directly, keeping its assertions:

```go
func TestRateLimitBacksOffRetryAfterPlusJitter(t *testing.T) {
	e := newEnv(t)
	err := e.s.classify(ctx, &port.NotionError{Kind: port.NotionRateLimited, Status: 429, RetryAfter: 5 * time.Second})
	var ra *port.RetryAfterError
	if !errors.As(err, &ra) || ra.Delay != 7*time.Second {
		t.Fatalf("err %v", err)
	}
}

func TestOverloadWithoutRetryAfterUsesDefault(t *testing.T) {
	e := newEnv(t)
	var ra *port.RetryAfterError
	if err := e.s.classify(ctx, &port.NotionError{Kind: port.NotionRateLimited, Status: 529}); !errors.As(err, &ra) || ra.Delay != defaultBackoff+2*time.Second {
		t.Fatalf("err %v", err)
	}
}

func TestRetryableKindsFailWithoutBackoff(t *testing.T) {
	e := newEnv(t)
	for _, k := range []port.NotionErrorKind{port.NotionTransient, port.NotionConflict, port.NotionUnauthorized} {
		err := e.s.classify(ctx, &port.NotionError{Kind: k})
		var ra *port.RetryAfterError
		if err == nil || errors.As(err, &ra) {
			t.Errorf("%s: err %v", k, err)
		}
	}
	if err := e.s.classify(ctx, errors.New("dynamodb: connection reset")); err == nil {
		t.Error("storage errors must be retried")
	}
}

func TestPoisonKindsAreAckedAndLogged(t *testing.T) {
	e := newEnv(t)
	for _, k := range []port.NotionErrorKind{port.NotionRejected, port.NotionNotFound} {
		if err := e.s.classify(ctx, &port.NotionError{Kind: k, Status: 400, Code: "validation_error", Message: "body failed validation"}); err != nil {
			t.Errorf("%s: err %v", k, err)
		}
	}
	if !strings.Contains(e.logs.String(), "validation_error") {
		t.Fatal("poison message not logged")
	}
}

func TestUnknownJobTypeIsAcked(t *testing.T) {
	e := newEnv(t)
	if err := e.s.HandleJob(ctx, port.Job{Type: "mystery"}); err != nil {
		t.Fatal(err)
	}
}
```

These replace `TestRateLimitChangesVisibility`, `TestOverloadWithoutRetryAfterUsesDefault`, `TestServerErrorIsRetriedWithoutVisibilityChange`, `TestConflictIsRetriedWithoutVisibilityChange`, `TestUnauthorizedIsRetried`, `TestForbiddenIsAcked` and `TestValidationErrorIsAcked`. The visibility half is now `TestConsumerBacksOffRetryAfter` in `adapter/queue`.

- In the session tests, `searchCall` uses the gateway so that 401s are classified:

```go
func searchCall(e *wenv) func(string) error {
	return func(tok string) error {
		_, err := e.gw.SearchPages(ctx, tok, "")
		return err
	}
}
```

- In `TestSessionRefreshTokenWriteGivesUpAfterThreeAttempts`, `errors.Is(err, errAck)` stays, because `errAck` lives in `notionsync`.

In `article_test.go`:
- Delete `TestCompletedRule`; it is now `domain.TestIsCompleted`.
- `articleMsg()` stays `queue.Article(testBot, domain.URLHash(artURL))` and still returns a `queue.Message`.

- [ ] **Step 4: Write the R1 tests**

Append to `backend/internal/usecase/notionsync/article_test.go`:

```go
// switchLibrary points the installation at a second, surviving Library data source,
// as setup on a different parent page would (R1).
func (e *wenv) switchLibrary() string {
	e.t.Helper()
	e.ns.AddDatabase("lib2-db", "lib2-ds", notiontest.LibrarySchema)
	db := e.tpl.DBConfig()
	db.LibraryDBID, db.LibraryDSID = "lib2-db", "lib2-ds"
	must(e.t, e.db.Store.SaveInstallationDatabases(ctx, testBot, db, e.now))
	return "lib2-ds"
}

// Review focus 4 / R1: a page in a database the installation no longer targets is
// re-created in the current one without PATCHing the old page (whose property ids
// the new database does not have), even though the old database still exists.
func TestArticleInOldDatabaseIsRecreatedBeforePatch(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 120, 0.5)
	e.handle(articleMsg())
	old := e.onlyLibraryPage()
	if a := e.article(artURL); a.NotionDataSourceID != e.tpl.LibraryDS {
		t.Fatalf("data source not recorded: %+v", a)
	}

	newDS := e.switchLibrary()
	e.read(artURL, 1, 60, 0.6)
	e.q.Drain()
	if f := e.handle(articleMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Calls("PATCH", "/v1/pages/"+old.ID)); n != 0 {
		t.Fatalf("PATCHed the old page %d times", n)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != articleMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	pages := e.ns.Pages(newDS)
	a := e.article(artURL)
	if len(pages) != 1 || a.NotionPageID != pages[0].ID || a.NotionDataSourceID != newDS {
		t.Fatalf("not re-created in the new Library: pages %d, article %+v", len(pages), a)
	}
}
```

Append to `backend/internal/usecase/notionsync/day_test.go`:

```go
func TestDayInOldDatabaseIsRecreatedBeforePatch(t *testing.T) {
	e := newEnv(t)
	e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 60, 0.5)
	e.handle(dayMsg())
	old := e.onlyDayPage()

	e.ns.AddDatabase("daily2-db", "daily2-ds", notiontest.DailySchema)
	db := e.tpl.DBConfig()
	db.DailyDBID, db.DailyDSID = "daily2-db", "daily2-ds"
	must(t, e.db.Store.SaveInstallationDatabases(ctx, testBot, db, e.now))
	e.read(artURL, 1, 60, 0.6)
	e.q.Drain()

	if f := e.handle(dayMsg()); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	if n := len(e.ns.Calls("PATCH", "/v1/pages/"+old.ID)); n != 0 {
		t.Fatalf("PATCHed the old day page %d times", n)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != dayMsg() {
		t.Fatalf("requeue %+v", sent)
	}
	e.handle(sent[0].Message)
	d, err := e.db.Store.GetDay(ctx, testBot, today)
	must(t, err)
	if pages := e.ns.Pages("daily2-ds"); len(pages) != 1 || d.NotionPageID != pages[0].ID || d.NotionDataSourceID != "daily2-ds" {
		t.Fatalf("day not re-created in the new Daily Log: %+v", d)
	}
}
```

- [ ] **Step 5: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/usecase/notionsync/...`
Expected: FAIL to compile with `undefined: Service` and `undefined: errAck`.

- [ ] **Step 6: Implement the use case**

`backend/internal/usecase/notionsync/service.go`:

```go
// Package notionsync writes reading data to the user's Notion (spec §2.13–§2.17). It is
// the only code that writes pages and the only code that refreshes tokens (C3, C8). The
// queue runs one job per installation at a time (C4), so a token refresh cannot race.
package notionsync

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Installations port.Installations
	Reading       port.Reading
	Users         port.Users
	Devices       port.Devices
	Purger        port.DataPurger
	Pages         port.NotionPages
	Auth          port.NotionAuth
	Sealer        port.Sealer
	Queue         port.SyncQueue
	Policy        domain.SyncPolicy
	Now           func() time.Time
	Jitter        func() time.Duration // added to Retry-After; default rand 0..3 s
	Sleep         func(time.Duration)  // in-process retry back-off; default time.Sleep
	Log           *slog.Logger
}

var _ port.JobHandler = (*Service)(nil)

// errAck stops a job without failing it (revoked, needs_setup, …).
var errAck = errors.New("notionsync: ack")

const defaultBackoff = 30 * time.Second

// HandleJob runs one job and classifies its error (spec §2.15).
func (s *Service) HandleJob(ctx context.Context, job port.Job) error {
	var err error
	switch job.Type {
	case port.JobArticle:
		err = s.syncArticle(ctx, job)
	case port.JobDay:
		err = s.syncDay(ctx, job)
	case port.JobPurge:
		err = s.purge(ctx, job)
	default:
		s.Log.ErrorContext(ctx, "notionsync: unknown job type dropped", "type", job.Type)
		return nil
	}
	return s.classify(ctx, err)
}

// classify maps a job error to ack (nil), back-off (*port.RetryAfterError) or retry.
// Retried (redelivered; DLQ after maxReceive, so it stays visible): storage and network
// errors, Notion 5xx, 409 conflict_error, and a 401 that escaped session.call (the
// refresh endpoint rejected us, e.g. invalid_client). Acked and logged: every other
// Notion 4xx (400 validation_error poison, 403, untranslated 404s).
func (s *Service) classify(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, errAck) {
		return nil
	}
	e, ok := port.NotionErrorOf(err)
	if !ok {
		return err
	}
	switch e.Kind {
	case port.NotionRateLimited:
		d := e.RetryAfter
		if d <= 0 {
			d = defaultBackoff
		}
		return &port.RetryAfterError{Delay: d + s.jitter(), Err: err}
	case port.NotionTransient, port.NotionConflict, port.NotionUnauthorized:
		return err
	default:
		s.Log.ErrorContext(ctx, "notionsync: notion rejected request; dropping job",
			"kind", e.Kind.String(), "status", e.Status, "code", e.Code, "message", e.Message)
		return nil
	}
}

func (s *Service) jitter() time.Duration {
	if s.Jitter != nil {
		return s.Jitter()
	}
	return time.Duration(rand.IntN(4)) * time.Second
}

func (s *Service) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// activeInstallation returns nil (→ ack) when the installation is gone or not active;
// its data stays stored and is resynced on reactivation (spec §2.13 step 1).
func (s *Service) activeInstallation(ctx context.Context, id string) (*domain.Installation, error) {
	inst, err := s.Installations.GetInstallation(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inst.Status != domain.StatusActive {
		return nil, nil
	}
	return inst, nil
}

// dataSourceError: the database was deleted, so the installation needs setup again
// and the job is acked (spec §2.15). Other errors pass through.
func (s *Service) dataSourceError(ctx context.Context, inst *domain.Installation, name string, err error) error {
	if !port.IsNotionKind(err, port.NotionDataSourceMissing) {
		return err
	}
	if serr := s.Installations.SetInstallationStatus(ctx, inst.ID, domain.StatusNeedsSetup, "database_missing:"+name, s.Now()); serr != nil {
		return serr
	}
	s.Log.WarnContext(ctx, "notionsync: database missing; installation needs setup", "installation_id", inst.ID, "database", name)
	return errAck
}
```

`backend/internal/usecase/notionsync/session.go` is today's `worker/session.go` with these replacements:
- `w *Worker` / `s.w.` become `svc *Service` / `s.svc.`.
- `s.w.Keys` becomes `s.svc.Sealer`.
- `s.w.Store` becomes `s.svc.Installations`.
- `s.w.Notion.RefreshToken` becomes `s.svc.Auth.RefreshToken`, returning `*port.NotionGrant`.
- `isStatus(err, http.StatusUnauthorized)` becomes `port.IsNotionKind(err, port.NotionUnauthorized)`.
- The invalid-grant check becomes `port.IsNotionKind(err, port.NotionInvalidGrant)`.
- `store.*` becomes `domain.*`.
- `s.w.Sleep(` becomes `s.svc.sleep(`.

The full file:

```go
package notionsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// session runs Notion calls for one installation, refreshing the token once on 401.
type session struct {
	svc       *Service
	inst      *domain.Installation
	token     string
	refreshed bool
}

func (s *Service) openSession(inst *domain.Installation) (*session, error) {
	tok, err := s.Sealer.Open(inst.AccessTokenEnc, []byte(inst.ID))
	if err != nil {
		return nil, fmt.Errorf("notionsync: decrypt access token: %w", err)
	}
	return &session{svc: s, inst: inst, token: string(tok)}, nil
}

// db is the installation's current Notion target.
func (s *session) db() domain.DBConfig { return s.inst.Databases() }

// call runs fn with the current token. On 401 it refreshes once and retries. A 401
// with a freshly refreshed token means the integration was removed → revoked.
func (s *session) call(ctx context.Context, fn func(token string) error) error {
	err := fn(s.token)
	if !port.IsNotionKind(err, port.NotionUnauthorized) {
		return err
	}
	if s.refreshed {
		return s.revoke(ctx)
	}
	if err := s.refresh(ctx); err != nil {
		return err
	}
	if err = fn(s.token); port.IsNotionKind(err, port.NotionUnauthorized) {
		return s.revoke(ctx)
	}
	return err
}

// refresh rotates tokens and persists both under the token_version guard (spec §2.16, C7).
func (s *session) refresh(ctx context.Context) error {
	s.refreshed = true
	if s.inst.RefreshTokenEnc == "" {
		return s.revoke(ctx)
	}
	aad := []byte(s.inst.ID)
	rt, err := s.svc.Sealer.Open(s.inst.RefreshTokenEnc, aad)
	if err != nil {
		return fmt.Errorf("notionsync: decrypt refresh token: %w", err)
	}
	res, err := s.svc.Auth.RefreshToken(ctx, string(rt))
	if port.IsNotionKind(err, port.NotionInvalidGrant) {
		return s.revoke(ctx)
	}
	if err != nil {
		return err
	}
	access, err := s.svc.Sealer.Seal([]byte(res.AccessToken), aad)
	if err != nil {
		return err
	}
	refresh := s.inst.RefreshTokenEnc
	if res.RefreshToken != "" {
		if refresh, err = s.svc.Sealer.Seal([]byte(res.RefreshToken), aad); err != nil {
			return err
		}
	}
	err = s.persistTokens(ctx, access, refresh)
	if errors.Is(err, domain.ErrConditionFailed) {
		// Someone rotated first; use what they stored.
		fresh, gerr := s.svc.Installations.GetInstallation(ctx, s.inst.ID)
		if gerr != nil {
			return gerr
		}
		tok, oerr := s.svc.Sealer.Open(fresh.AccessTokenEnc, aad)
		if oerr != nil {
			return oerr
		}
		s.inst, s.token = fresh, string(tok)
		return nil
	}
	if err != nil {
		return err
	}
	s.inst.AccessTokenEnc, s.inst.RefreshTokenEnc = access, refresh
	s.inst.TokenVersion++
	s.token = res.AccessToken
	return nil
}

const (
	tokenWriteAttempts = 3
	tokenWriteBackoff  = 100 * time.Millisecond
)

// persistTokens stores the rotated tokens, retrying transient failures in-process:
// Notion has already rotated the refresh token, so losing this write would force a
// reconnect (C7). A condition failure is returned at once.
func (s *session) persistTokens(ctx context.Context, access, refresh string) error {
	var err error
	for attempt := 1; attempt <= tokenWriteAttempts; attempt++ {
		err = s.svc.Installations.UpdateInstallationTokens(ctx, s.inst.ID, access, refresh, s.inst.TokenVersion, s.svc.Now())
		if err == nil || errors.Is(err, domain.ErrConditionFailed) {
			return err
		}
		if attempt < tokenWriteAttempts {
			s.svc.sleep(time.Duration(attempt) * tokenWriteBackoff)
		}
	}
	s.svc.Log.ErrorContext(ctx, "notionsync: persisting rotated tokens failed", "installation_id", s.inst.ID, "err", err)
	return err
}

func (s *session) revoke(ctx context.Context) error {
	if err := s.svc.Installations.SetInstallationStatus(ctx, s.inst.ID, domain.StatusRevoked, "", s.svc.Now()); err != nil {
		return err
	}
	s.svc.Log.WarnContext(ctx, "notionsync: installation revoked", "installation_id", s.inst.ID)
	return errAck
}
```

`backend/internal/usecase/notionsync/article.go`:

```go
package notionsync

import (
	"context"
	"errors"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// syncArticle implements spec §2.13. It writes absolute values read from storage (C6).
func (s *Service) syncArticle(ctx context.Context, job port.Job) error {
	inst, err := s.activeInstallation(ctx, job.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	art, err := s.Reading.GetArticle(ctx, inst.ID, job.URLHash)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	completed := s.Policy.IsCompleted(*art)
	sess, err := s.openSession(inst)
	if err != nil {
		return err
	}

	switch {
	case art.NotionPageID != "" && art.NotionDataSourceID != "" && !domain.SameID(art.NotionDataSourceID, inst.LibraryDSID):
		// R1: the page lives in a database this installation no longer writes to. Its
		// property ids differ from ours, so a PATCH would be rejected; re-create instead.
		return s.forgetArticlePage(ctx, inst.ID, job)
	case art.NotionPageID == "":
		if art.TotalSeconds < int64(s.Policy.MinCreateSeconds) {
			return nil
		}
		if err := s.createArticlePage(ctx, sess, art, completed); err != nil {
			return s.dataSourceError(ctx, inst, domain.LibraryName, err)
		}
	default:
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateArticlePage(ctx, tok, sess.db(), art.NotionPageID, *art, completed)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			return s.forgetArticlePage(ctx, inst.ID, job)
		}
		if err != nil {
			return err
		}
	}
	return s.Reading.MarkArticleSynced(ctx, inst.ID, job.URLHash, art.Version, completed, s.Now())
}

// forgetArticlePage clears the page link and re-delivers the job, which then creates
// (or adopts) a page in the current Library.
func (s *Service) forgetArticlePage(ctx context.Context, inst string, job port.Job) error {
	if err := s.Reading.ClearArticlePage(ctx, inst, job.URLHash); err != nil {
		return err
	}
	return s.Queue.Requeue(ctx, job)
}

// createArticlePage adopts a page with the same URL if one exists, else creates one.
func (s *Service) createArticlePage(ctx context.Context, sess *session, art *domain.Article, completed bool) error {
	var page *port.Page
	err := sess.call(ctx, func(tok string) error {
		p, err := s.Pages.FindArticlePage(ctx, tok, sess.db(), art.CanonicalURL)
		page = p
		return err
	})
	if err != nil {
		return err
	}
	if page != nil {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateArticlePage(ctx, tok, sess.db(), page.ID, *art, completed)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			page = nil // vanished between the query and the PATCH: create a fresh page below
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		err := sess.call(ctx, func(tok string) error {
			p, err := s.Pages.CreateArticlePage(ctx, tok, sess.db(), *art, completed)
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.Reading.SetArticlePage(ctx, sess.inst.ID, art.Hash, page.ID, page.URL, sess.inst.LibraryDSID)
}
```

`backend/internal/usecase/notionsync/day.go`:

```go
package notionsync

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// maxDayRelations caps the Articles relation (spec §2.14 step 2).
const maxDayRelations = 100

// syncDay implements spec §2.14.
func (s *Service) syncDay(ctx context.Context, job port.Job) error {
	inst, err := s.activeInstallation(ctx, job.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	day, err := s.Reading.GetDay(ctx, inst.ID, job.Date)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if day.NotionPageID != "" && day.NotionDataSourceID != "" && !domain.SameID(day.NotionDataSourceID, inst.DailyDSID) {
		return s.forgetDayPage(ctx, inst.ID, job) // R1, as for articles
	}
	pageIDs, err := s.dayArticlePages(ctx, inst.ID, day.ArticleHashes)
	if err != nil {
		return err
	}
	sess, err := s.openSession(inst)
	if err != nil {
		return err
	}
	if day.NotionPageID == "" {
		if err := s.createDayPage(ctx, sess, day, pageIDs); err != nil {
			return s.dataSourceError(ctx, inst, domain.DailyLogName, err)
		}
	} else {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateDayPage(ctx, tok, sess.db(), day.NotionPageID, *day, pageIDs)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			return s.forgetDayPage(ctx, inst.ID, job)
		}
		if err != nil {
			return err
		}
	}
	return s.Reading.MarkDaySynced(ctx, inst.ID, job.Date, day.Version)
}

func (s *Service) forgetDayPage(ctx context.Context, inst string, job port.Job) error {
	if err := s.Reading.ClearDayPage(ctx, inst, job.Date); err != nil {
		return err
	}
	return s.Queue.Requeue(ctx, job)
}

// dayArticlePages returns the page ids of the day's articles that have one (≤ 100).
func (s *Service) dayArticlePages(ctx context.Context, inst string, hashes []string) ([]string, error) {
	sorted := slices.Sorted(slices.Values(hashes))
	arts, err := s.Reading.BatchGetArticles(ctx, inst, sorted)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range sorted {
		if a := arts[h]; a != nil && a.NotionPageID != "" {
			ids = append(ids, a.NotionPageID)
			if len(ids) == maxDayRelations {
				break
			}
		}
	}
	return ids, nil
}

// createDayPage adopts the row for this date if one exists, else creates it.
func (s *Service) createDayPage(ctx context.Context, sess *session, day *domain.Day, pageIDs []string) error {
	var page *port.Page
	err := sess.call(ctx, func(tok string) error {
		p, err := s.Pages.FindDayPage(ctx, tok, sess.db(), day.Date)
		page = p
		return err
	})
	if err != nil {
		return err
	}
	if page != nil {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateDayPage(ctx, tok, sess.db(), page.ID, *day, pageIDs)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			page = nil
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		err := sess.call(ctx, func(tok string) error {
			p, err := s.Pages.CreateDayPage(ctx, tok, sess.db(), *day, pageIDs)
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.Reading.SetDayPage(ctx, sess.inst.ID, day.Date, page.ID, sess.inst.DailyDSID)
}
```

`backend/internal/usecase/notionsync/purge.go`:

```go
package notionsync

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// purge deletes every stored item of a deleted user (spec §2.17). It never touches
// Notion, skips users not marked deleted, and skips installations that now belong to
// someone else.
func (s *Service) purge(ctx context.Context, job port.Job) error {
	uid := job.UserID
	if uid == "" {
		return nil
	}
	prof, err := s.Users.GetProfile(ctx, uid)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		prof = nil
	case err != nil:
		return err
	case !prof.Deleted():
		s.Log.WarnContext(ctx, "notionsync: purge for a live user ignored", "user_id", uid)
		return nil
	}

	insts, err := s.Installations.ListUserInstallationIDs(ctx, uid)
	if err != nil {
		return err
	}
	if job.InstallationID != "" && !slices.Contains(insts, job.InstallationID) {
		insts = append(insts, job.InstallationID)
	}
	for _, id := range insts {
		inst, err := s.Installations.GetInstallation(ctx, id)
		if err == nil && inst.UserID != uid {
			continue
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := s.Purger.DeleteInstallationData(ctx, id); err != nil {
			return err
		}
	}

	devices, err := s.Devices.ListDevices(ctx, uid)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if err := s.Devices.DeleteDeviceToken(ctx, d.TokenHMAC); err != nil {
			return err
		}
	}
	if prof != nil {
		if err := s.Users.DeleteNotionUserLink(ctx, prof.NotionUserID, uid); err != nil {
			return err
		}
	}
	return s.Purger.DeleteUserData(ctx, uid)
}
```

- [ ] **Step 7: Rewire the worker binaries and the e2e test, then delete `worker`**

Replace the `Worker` function in `backend/internal/app/app.go`:

```go
// WorkerDeps are the notion-writer's collaborators.
type WorkerDeps struct {
	Store      *dynamo.Store
	Notion     *notion.Client
	Keys       *crypto.Keyring
	Queue      queue.Sender
	Visibility queue.VisibilityChanger
	Tracking   config.Tracking
	Now        func() time.Time
	Jitter     func() time.Duration
	Sleep      func(time.Duration)
	Log        *slog.Logger
}

// NewWorker assembles the Notion sync behind the SQS consumer.
func NewWorker(d WorkerDeps) *queue.Consumer {
	gw := &notion.Gateway{Client: d.Notion}
	svc := &notionsync.Service{
		Installations: d.Store, Reading: d.Store, Users: d.Store, Devices: d.Store, Purger: d.Store,
		Pages: gw, Auth: gw, Sealer: d.Keys,
		Queue: &queue.SyncQueue{Sender: d.Queue, Now: d.Now},
		Policy: domain.SyncPolicy{MinCreateSeconds: d.Tracking.MinCreateSeconds, CompleteProgress: d.Tracking.CompleteProgress,
			CompleteTimeRatio: d.Tracking.CompleteTimeRatio, WordsPerMinute: d.Tracking.WordsPerMinute},
		Now: d.Now, Jitter: d.Jitter, Sleep: d.Sleep, Log: d.Log,
	}
	return &queue.Consumer{Handler: svc, Visibility: d.Visibility, Log: d.Log}
}

// Worker is the production notion-writer: paced Notion client (C22).
func Worker(cfg *config.Config, st *dynamo.Store, q queue.Sender, vis queue.VisibilityChanger, log *slog.Logger) *queue.Consumer {
	return NewWorker(WorkerDeps{Store: st, Notion: NotionClient(cfg, notion.NewMinInterval(350*time.Millisecond)),
		Keys: cfg.Keys, Queue: q, Visibility: vis, Tracking: cfg.Public.Tracking, Now: time.Now, Log: log})
}
```

`cmd/worker/main.go` and `cmd/local/main.go` keep calling `app.Worker(...).HandleSQS` unchanged.

In `internal/e2e/e2e_test.go`, replace the `worker.New(worker.Deps{…})` construction with `app.NewWorker(app.WorkerDeps{…})` using the same fields: `Store`, `Notion`, `Keys`, `Queue`, `Visibility`, `Tracking`, `Now` and `Log`. The `wk` field type becomes `*queue.Consumer`, and `wk.HandleSQS` calls stay as they are.

```bash
cd backend
git rm -rq internal/worker
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/app internal/e2e internal/usecase internal/adapter
```

- [ ] **Step 8: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`. `usecase/notionsync` carries the ported article, day, purge and session tests plus the new classification and R1 tests. `TestDependencyRule` passes for `usecase/notionsync` (no adapter imports) and for all adapters.

- [ ] **Step 9: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): replace worker with usecase/notionsync over Notion and storage ports; check page data source before PATCH (R1)"
```

---
### Task 6: Use cases `auth`, `connect`, `setup` (+ `apperr`); thin OAuth, token, setup and auth handlers

**Files:**
- Create: `internal/usecase/apperr/apperr.go`, `internal/usecase/auth/auth.go`, `internal/usecase/connect/connect.go`, `internal/usecase/setup/{activator.go,setup.go}`
- Modify: `internal/domain/identity.go` (one-time code bound)
- Modify: `internal/httpapi/{app.go,errors.go,middleware.go,handlers_oauth.go,handlers_auth.go,handlers_setup.go,handlers_account.go,handlers_me.go,views.go}`
- Modify: `internal/app/app.go`, `cmd/api/main.go`, `cmd/local/main.go`, `internal/httpapi/testenv_test.go`, `internal/e2e/e2e_test.go`
- Delete: `internal/setup` (its resync test moves to `internal/usecase/setup/activator_test.go`)
- Test: `internal/usecase/setup/activator_test.go`, `internal/httpapi/errors_internal_test.go` (add cases), `internal/httpapi/setup_test.go` (add R2 test)

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces:
  - `apperr`:
    - `apperr.Kind` with the values `Invalid`, `InvalidGrant`, `Unauthorized`, `NotFound`, `Conflict`, `NoInstallation`, `TooLarge` and `Capacity`.
    - `apperr.Error{Kind; Msg}` and `apperr.As(err) (*Error, bool)`.
    - Constructors `apperr.NewInvalid(format, args...)`, `NewInvalidGrant()`, `NewUnauthorized()`, `NewNotFound(msg)`, `NewConflict(msg)`, `NewNoInstallation()`, `NewTooLarge(msg)` and `NewCapacity()`. Their messages are today's §2.0 messages.
  - `domain.PlausibleOneTimeCode(s string) bool`: non-empty and at most 128 characters.
  - `auth`:
    - `auth.Service{Users; Devices; Grants; Installations; Pepper []byte; Now; Log}`
    - `auth.Principal{UserID, TokenHMAC string; Device *domain.Device; User *domain.User}`, with `InstallationID()`
    - `(*Service).Authenticate(ctx, token string) (*Principal, error)`
    - `(*Service).RedeemCode(ctx, code, verifier, deviceName string) (*Grant, error)`
    - `auth.Grant{DeviceToken, DeviceID string; User *domain.User; Installation *domain.Installation}`
    - `(*Service).Logout(ctx, *Principal) error`
  - `setup`:
    - `setup.Activator{Reading port.Reading; Installations port.Installations; Queue port.SyncQueue; Now; Sleep func(time.Duration); Log}`, with `Activate(ctx, inst *domain.Installation, full bool) error`
    - `setup.Service{Installations; Workspace port.NotionWorkspace; Sealer port.Sealer; Activator *Activator; Now}`
    - `(*Service).SearchPages(ctx, instID, query string) ([]port.PageRef, error)`
    - `(*Service).Setup(ctx, instID, parentPageID string) (*domain.Installation, error)`
  - `connect`:
    - `connect.Service{Users; Grants; Installations; Auth port.NotionAuth; Workspace port.NotionWorkspace; Sealer port.Sealer; Activator *setup.Activator; AllowedExtensionIDs map[string]bool; Now; Log}`
    - `(*Service).Start(ctx, extRedirect, challenge, method, redirectURI string) (string, error)`
    - `(*Service).Complete(ctx, state, code, notionError, redirectURI string) (*connect.Redirect, error)`
    - `connect.Redirect{URL, Param, Value string}` and `connect.ErrLinkExpired`
  - `app`:
    - `app.APIDeps{Config *config.Config; Store *dynamo.Store; Notion *notion.Client; Queue queue.Sender; Now func() time.Time; Log *slog.Logger; Sleep func(time.Duration)}`
    - `app.NewAPI(APIDeps) *echo.Echo`
    - `app.API(cfg, st, q, log) *echo.Echo` for the binaries.

- [ ] **Step 1: Write the failing tests**

`backend/internal/usecase/setup/activator_test.go` (moved from `internal/setup/setup_test.go`'s `TestResyncEnqueuesPending`, plus a full-mode case):

```go
package setup_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/setup"
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

func newActivator(t *testing.T) (*setup.Activator, *dynamotest.Env, *queue.Recorder) {
	db, rec := dynamotest.New(t), &queue.Recorder{}
	a := &setup.Activator{Reading: db.Store, Installations: db.Store,
		Queue: &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return testNow }},
		Now:   func() time.Time { return testNow }, Sleep: func(time.Duration) {}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return a, db, rec
}

func pushTwo(t *testing.T, db *dynamotest.Env) {
	for i, u := range []string{"https://a.example/1", "https://b.example/2"} {
		_, err := db.Store.PushSegment(ctx, domain.SegmentWrite{
			InstallationID: "bot-1", DeviceID: "d", SegmentID: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}[i],
			SessionID: "s", URLHash: domain.URLHash(u), Date: "2026-10-06", StartedAt: testNow, EndedAt: testNow.Add(time.Minute),
			ActiveSeconds: 60, MaxProgress: 0.5, Article: domain.ArticleMeta{CanonicalURL: u, Source: "generic"},
		})
		must(t, err)
	}
	must(t, db.Store.MarkArticleSynced(ctx, "bot-1", domain.URLHash("https://a.example/1"), 1, false, testNow))
}

// T22: activation enqueues every article/day whose version is ahead of Notion.
func TestActivateEnqueuesPending(t *testing.T) {
	a, db, rec := newActivator(t)
	pushTwo(t, db)
	inst := &domain.Installation{ID: "bot-1", Status: domain.StatusActive}
	must(t, a.Activate(ctx, inst, false))
	got := rec.Messages()
	want := []queue.Message{queue.Article("bot-1", domain.URLHash("https://b.example/2")), queue.Day("bot-1", "2026-10-06")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestActivateFullResendsSyncedItems(t *testing.T) {
	a, db, rec := newActivator(t)
	pushTwo(t, db)
	must(t, a.Activate(ctx, &domain.Installation{ID: "bot-1", Status: domain.StatusActive}, true))
	if got := rec.Messages(); len(got) != 3 {
		t.Fatalf("full resync enqueued %+v", got)
	}
}
```

Append to `backend/internal/httpapi/errors_internal_test.go`:

```go
func TestToAPIErrorMapsAppErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{apperr.NewInvalid("bad %s", "x"), 400, "invalid_request"},
		{apperr.NewInvalidGrant(), 400, "invalid_grant"},
		{apperr.NewUnauthorized(), 401, "unauthorized"},
		{apperr.NewNotFound("nope"), 404, "not_found"},
		{apperr.NewConflict("busy"), 409, "conflict"},
		{apperr.NewNoInstallation(), 409, "no_installation"},
		{apperr.NewTooLarge("big"), 413, "payload_too_large"},
		{fmt.Errorf("wrapped: %w", apperr.NewCapacity()), 503, "capacity_exceeded"},
	}
	for _, c := range cases {
		if ae := toAPIError(c.err); ae.status != c.status || ae.code != c.code {
			t.Errorf("%v: got %d %s, want %d %s", c.err, ae.status, ae.code, c.status, c.code)
		}
	}
	if ae := toAPIError(apperr.NewInvalid("bad %s", "x")); ae.msg != "bad x" {
		t.Errorf("message lost: %q", ae.msg)
	}
}
```

Append to `backend/internal/httpapi/setup_test.go` the R2 check. It uses existing helpers: `pushBody` from `sync_test.go` (same package); `seedUser`, `seedInstallation` and `queue.Recorder.Drain`.

```go
// Review focus 5 / R2: a push followed by setup in the same debounce bucket must not
// lose the resync to SQS's 5-minute deduplication: every resync dedup id differs from
// the push's bucket ids.
func TestResyncAfterPushInSameBucketIsDelivered(t *testing.T) {
	env := newTestEnv(t)
	uid, tok := env.seedUser()
	env.seedInstallation(uid, domain.StatusNeedsSetup)
	if rec := env.do("POST", "/v1/sync/push", pushBody(env), tok); rec.Code != 200 {
		t.Fatalf("push %d %s", rec.Code, rec.Body)
	}
	pushIDs := map[string]bool{}
	for _, o := range env.queue.Drain() {
		pushIDs[o.DedupID] = true
	}
	if rec := env.do("POST", "/v1/setup", map[string]string{"parent_page_id": "parent-1"}, tok); rec.Code != 200 {
		t.Fatalf("setup %d %s", rec.Code, rec.Body)
	}
	resync := env.queue.Drain()
	if len(resync) == 0 {
		t.Fatal("setup enqueued no resync")
	}
	for _, o := range resync {
		if pushIDs[o.DedupID] {
			t.Fatalf("resync message %+v reuses push dedup id %q", o.Message, o.DedupID)
		}
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd backend && go test ./internal/usecase/setup/... ./internal/httpapi/...`
Expected: FAIL to compile with `undefined: setup.Activator` and `undefined: apperr`.

- [ ] **Step 3: Implement `apperr` and the domain bound**

`backend/internal/usecase/apperr/apperr.go`:

```go
// Package apperr is the use cases' error vocabulary. The HTTP adapter maps each Kind
// to a spec §2.0 status and code; use cases never know about HTTP.
package apperr

import (
	"errors"
	"fmt"
)

type Kind int

const (
	Invalid Kind = iota + 1
	InvalidGrant
	Unauthorized
	NotFound
	Conflict
	NoInstallation
	TooLarge
	Capacity
)

type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// As unwraps err into *Error.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

func NewInvalid(format string, a ...any) *Error { return &Error{Invalid, fmt.Sprintf(format, a...)} }
func NewInvalidGrant() *Error {
	return &Error{InvalidGrant, "authorization code is invalid, expired or already used"}
}
func NewUnauthorized() *Error   { return &Error{Unauthorized, "missing, unknown or revoked device token"} }
func NewNotFound(msg string) *Error { return &Error{NotFound, msg} }
func NewConflict(msg string) *Error { return &Error{Conflict, msg} }
func NewNoInstallation() *Error { return &Error{NoInstallation, "connect Notion first"} }
func NewTooLarge(msg string) *Error { return &Error{TooLarge, msg} }
func NewCapacity() *Error       { return &Error{Capacity, "storage is busy; retry with backoff"} }
```

Append to `backend/internal/domain/identity.go`:

```go
// maxOneTimeCodeLen bounds OAuth state and auth codes (ours are 43 chars) before they
// are used as storage keys: an oversized key is a storage error, not "expired".
const maxOneTimeCodeLen = 128

// PlausibleOneTimeCode reports whether s could be one of our state or auth code values.
func PlausibleOneTimeCode(s string) bool { return s != "" && len(s) <= maxOneTimeCodeLen }
```

- [ ] **Step 4: Implement the use cases**

`backend/internal/usecase/auth/auth.go`:

```go
// Package auth authenticates extension devices and issues their tokens (spec §2.3, §2.4, §2.17).
package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/platform/textutil"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Users         port.Users
	Devices       port.Devices
	Grants        port.OAuthGrants
	Installations port.Installations
	Pepper        []byte
	Now           func() time.Time
	Log           *slog.Logger
}

// Principal is the authenticated device and its user.
type Principal struct {
	UserID    string
	TokenHMAC string
	Device    *domain.Device
	User      *domain.User
}

// InstallationID is the user's active installation (one per user in v1), or "".
func (p *Principal) InstallationID() string { return p.User.ActiveInstallationID }

// Authenticate resolves a device bearer token (spec §2.4). Unknown, revoked or
// deleted-account tokens are apperr Unauthorized.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if !crypto.ValidDeviceTokenFormat(token) {
		return nil, apperr.NewUnauthorized()
	}
	mac := crypto.TokenHMAC(s.Pepper, token)
	ref, err := s.Devices.GetDeviceToken(ctx, mac)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	dev, err := s.Devices.GetDevice(ctx, ref.UserID, ref.DeviceID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && dev.Revoked()) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	user, err := s.Users.GetProfile(ctx, ref.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && user.Deleted()) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	s.touch(ctx, ref.UserID, dev)
	return &Principal{UserID: ref.UserID, TokenHMAC: mac, Device: dev, User: user}, nil
}

// touch updates last_seen_at at most hourly. The in-memory check skips the write
// (and its WCU) on most requests; the conditional update closes races.
func (s *Service) touch(ctx context.Context, uid string, dev *domain.Device) {
	now := s.Now()
	if seen, err := time.Parse(time.RFC3339, dev.LastSeenAt); err == nil && now.Sub(seen) < time.Hour {
		return
	}
	if err := s.Devices.TouchDevice(ctx, uid, dev.ID, now); err != nil {
		s.Log.WarnContext(ctx, "auth: touch device failed", "err", err)
	}
}

// Grant is a freshly issued device token.
type Grant struct {
	DeviceToken  string
	DeviceID     string
	User         *domain.User
	Installation *domain.Installation
}

// RedeemCode exchanges a one-time code for a device token (spec §2.3). The code is
// consumed before the PKCE check, so a wrong verifier burns it.
func (s *Service) RedeemCode(ctx context.Context, code, verifier, deviceName string) (*Grant, error) {
	if !domain.PlausibleOneTimeCode(code) {
		return nil, apperr.NewInvalidGrant()
	}
	now := s.Now()
	ac, err := s.Grants.ConsumeAuthCode(ctx, code, now)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NewInvalidGrant()
	}
	if err != nil {
		return nil, err
	}
	if !crypto.VerifyPKCE(verifier, ac.CodeChallenge) {
		return nil, apperr.NewInvalidGrant()
	}
	token, err := crypto.NewDeviceToken()
	if err != nil {
		return nil, err
	}
	deviceID := ulid.Make().String()
	dev := domain.Device{Name: cleanDeviceName(deviceName), TokenHMAC: crypto.TokenHMAC(s.Pepper, token),
		CreatedAt: domain.Timestamp(now), LastSeenAt: domain.Timestamp(now)}
	if err := s.Devices.CreateDevice(ctx, ac.UserID, deviceID, dev); err != nil {
		return nil, err
	}
	user, err := s.Users.GetProfile(ctx, ac.UserID)
	if err != nil {
		return nil, err
	}
	inst, err := s.Installations.GetInstallation(ctx, ac.InstallationID)
	if errors.Is(err, domain.ErrNotFound) {
		inst, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Grant{DeviceToken: token, DeviceID: deviceID, User: user, Installation: inst}, nil
}

// Logout revokes the calling device (spec §2.17).
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.Devices.RevokeDevice(ctx, p.UserID, p.Device.ID, p.TokenHMAC, s.Now())
}

func cleanDeviceName(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "Unknown device"
	}
	return textutil.Truncate(s, 100)
}
```

`backend/internal/usecase/setup/activator.go`:

```go
// Package setup connects an installation to its Notion databases and enqueues its
// backlog when it becomes active (spec §2.6, §2.7, T22).
package setup

import (
	"context"
	"log/slog"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

const (
	resyncAttempts = 3
	resyncBackoff  = 100 * time.Millisecond
)

// Activator enqueues an installation's backlog when it becomes active.
type Activator struct {
	Reading       port.Reading
	Installations port.Installations
	Queue         port.SyncQueue
	Now           func() time.Time
	Sleep         func(time.Duration) // retry back-off; nil means time.Sleep
	Log           *slog.Logger
}

// Activate enqueues the backlog (full when the databases changed, scenario 10),
// retrying briefly. If it still fails, the installation goes back to needs_setup
// with setup_error "resync_failed" instead of staying active with a stranded
// backlog: re-running setup or reconnecting runs it again.
func (a *Activator) Activate(ctx context.Context, inst *domain.Installation, full bool) error {
	var err error
	for attempt := 1; attempt <= resyncAttempts; attempt++ {
		if err = a.resync(ctx, inst.ID, full); err == nil {
			return nil
		}
		if attempt < resyncAttempts {
			a.sleep(time.Duration(attempt) * resyncBackoff)
		}
	}
	a.Log.ErrorContext(ctx, "resync enqueue failed; installation needs setup", "installation_id", inst.ID, "err", err)
	if err := a.Installations.SetInstallationStatus(ctx, inst.ID, domain.StatusNeedsSetup, domain.SetupErrResyncFailed, a.Now()); err != nil {
		return err
	}
	inst.Status, inst.SetupError = domain.StatusNeedsSetup, domain.SetupErrResyncFailed
	return nil
}

func (a *Activator) resync(ctx context.Context, id string, full bool) error {
	hashes, dates, err := a.Reading.PendingSync(ctx, id, full)
	if err != nil {
		return err
	}
	return a.Queue.EnqueueResync(ctx, id, hashes, dates)
}

func (a *Activator) sleep(d time.Duration) {
	if a.Sleep != nil {
		a.Sleep(d)
		return
	}
	time.Sleep(d)
}
```

`backend/internal/usecase/setup/setup.go`:

```go
package setup

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

// Service lets a connected user pick a parent page and create the databases.
type Service struct {
	Installations port.Installations
	Workspace     port.NotionWorkspace
	Sealer        port.Sealer
	Activator     *Activator
	Now           func() time.Time
}

// SearchPages lists pages the integration can see (spec §2.6).
func (s *Service) SearchPages(ctx context.Context, instID, query string) ([]port.PageRef, error) {
	_, token, err := s.installationToken(ctx, instID)
	if err != nil {
		return nil, err
	}
	pages, err := s.Workspace.SearchPages(ctx, token, query)
	if err != nil {
		return nil, notionErr(err)
	}
	return pages, nil
}

// Setup creates (or reuses) both databases under parentPageID and activates the
// installation with a full resync (spec §2.7, scenario 10).
func (s *Service) Setup(ctx context.Context, instID, parentPageID string) (*domain.Installation, error) {
	if strings.TrimSpace(parentPageID) == "" {
		return nil, apperr.NewInvalid("parent_page_id is required")
	}
	inst, token, err := s.installationToken(ctx, instID)
	if err != nil {
		return nil, err
	}
	if inst.Status == domain.StatusActive {
		return nil, apperr.NewConflict("installation is already set up")
	}
	db, err := s.Workspace.ProvisionDatabases(ctx, token, parentPageID)
	if err != nil {
		// A reused same-titled database may have a missing or wrong-typed property.
		var se *domain.SetupError
		if errors.As(err, &se) {
			return nil, apperr.NewConflict("setup failed: " + se.Reason)
		}
		return nil, notionErr(err)
	}
	if err := s.Installations.SaveInstallationDatabases(ctx, inst.ID, db, s.Now()); err != nil {
		return nil, err
	}
	inst.ApplyDatabases(db)
	inst.Status, inst.SetupError = domain.StatusActive, ""
	if err := s.Activator.Activate(ctx, inst, true); err != nil {
		return nil, err
	}
	return inst, nil
}

// installationToken loads the active installation and decrypts its access token.
// The API never refreshes (C8): a Notion 401 surfaces as a conflict ("reconnect").
func (s *Service) installationToken(ctx context.Context, instID string) (*domain.Installation, string, error) {
	if instID == "" {
		return nil, "", apperr.NewNoInstallation()
	}
	inst, err := s.Installations.GetInstallation(ctx, instID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, "", apperr.NewNoInstallation()
	}
	if err != nil {
		return nil, "", err
	}
	if inst.Status == domain.StatusRevoked {
		return nil, "", apperr.NewConflict("Notion connection revoked; reconnect")
	}
	tok, err := s.Sealer.Open(inst.AccessTokenEnc, []byte(inst.ID))
	if err != nil {
		return nil, "", err
	}
	return inst, string(tok), nil
}

// notionErr maps Notion failures in API calls to application errors (spec §2.0).
func notionErr(err error) error {
	e, ok := port.NotionErrorOf(err)
	if !ok {
		return err
	}
	switch {
	case e.Kind == port.NotionUnauthorized:
		return apperr.NewConflict("Notion authorization expired; reconnect")
	case e.Kind == port.NotionNotFound:
		return apperr.NewNotFound("Notion object not found or not shared with the integration")
	case e.Kind == port.NotionRejected && e.Status == http.StatusBadRequest:
		return apperr.NewInvalid("Notion rejected the request: %s", e.Message)
	}
	return err
}
```

`net/http` is standard library and is used only for the status constant. The rule allows it.

`backend/internal/usecase/connect/connect.go`:

```go
// Package connect runs the Notion OAuth flow: start, callback, user and installation
// upsert (spec §2.1, §2.2).
package connect

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
	"reading-tracker/backend/internal/usecase/setup"
)

const (
	oauthStateTTL = 10 * time.Minute
	authCodeTTL   = 2 * time.Minute
)

// ErrLinkExpired: the callback's state is missing, expired or already used.
var ErrLinkExpired = errors.New("connect: link expired")

type Service struct {
	Users               port.Users
	Grants              port.OAuthGrants
	Installations       port.Installations
	Auth                port.NotionAuth
	Workspace           port.NotionWorkspace
	Sealer              port.Sealer
	Activator           *setup.Activator
	AllowedExtensionIDs map[string]bool
	Now                 func() time.Time
	Log                 *slog.Logger
}

// Redirect sends the browser back to the extension with ?<Param>=<Value>.
type Redirect struct {
	URL   string
	Param string
	Value string
}

// Start validates the extension redirect and PKCE challenge, stores a single-use
// state, and returns Notion's authorize URL (spec §2.1).
func (s *Service) Start(ctx context.Context, extRedirect, challenge, method, redirectURI string) (string, error) {
	if !s.allowedExtRedirect(extRedirect) {
		return "", apperr.NewInvalid("ext_redirect is not an allowed extension redirect")
	}
	if method != "S256" || !crypto.ValidChallenge(challenge) {
		return "", apperr.NewInvalid("code_challenge must be an S256 challenge")
	}
	state, err := crypto.RandomB64URL(32)
	if err != nil {
		return "", err
	}
	st := domain.OAuthState{ExtRedirect: extRedirect, CodeChallenge: challenge}
	if err := s.Grants.PutOAuthState(ctx, state, st, s.Now().Add(oauthStateTTL)); err != nil {
		return "", err
	}
	return s.Auth.AuthorizeURL(redirectURI, state), nil
}

// allowedExtRedirect accepts only https://<id>.chromiumapp.org/… for configured ids.
func (s *Service) allowedExtRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	id, ok := strings.CutSuffix(strings.ToLower(u.Hostname()), ".chromiumapp.org")
	return ok && s.AllowedExtensionIDs[id]
}

// Complete handles Notion's callback (spec §2.2): consume the state, exchange the
// code, upsert the user and installation, and issue a one-time code for the extension.
func (s *Service) Complete(ctx context.Context, state, code, notionError, redirectURI string) (*Redirect, error) {
	if !domain.PlausibleOneTimeCode(state) {
		return nil, ErrLinkExpired
	}
	st, err := s.Grants.ConsumeOAuthState(ctx, state, s.Now())
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrLinkExpired
	}
	if err != nil {
		return nil, err
	}
	if notionError != "" {
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: notionError}, nil
	}
	grant, err := s.Auth.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		s.Log.WarnContext(ctx, "oauth: code exchange failed", "err", err)
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: "exchange_failed"}, nil
	}
	if grant.BotID == "" || grant.OwnerUserID == "" {
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: "unsupported_owner"}, nil
	}
	uid, err := s.resolveUser(ctx, grant)
	if err != nil {
		return nil, err
	}
	inst, err := s.upsertInstallation(ctx, uid, grant)
	if err != nil {
		return nil, err
	}
	authCode, err := crypto.RandomB64URL(32)
	if err != nil {
		return nil, err
	}
	ac := domain.AuthCode{UserID: uid, InstallationID: inst.ID, CodeChallenge: st.CodeChallenge, ExtRedirect: st.ExtRedirect}
	if err := s.Grants.PutAuthCode(ctx, authCode, ac, s.Now().Add(authCodeTTL)); err != nil {
		return nil, err
	}
	return &Redirect{URL: st.ExtRedirect, Param: "code", Value: authCode}, nil
}

// resolveUser maps the Notion owner to our user, creating one if needed (§2.2 step 4).
// A user whose deletion is pending is treated as absent.
func (s *Service) resolveUser(ctx context.Context, g *port.NotionGrant) (string, error) {
	uid, err := s.Users.UserIDForNotionUser(ctx, g.OwnerUserID)
	switch {
	case err == nil:
		user, uerr := s.Users.GetProfile(ctx, uid)
		if uerr == nil && !user.Deleted() {
			return uid, nil
		}
		if uerr != nil && !errors.Is(uerr, domain.ErrNotFound) {
			return "", uerr
		}
	case !errors.Is(err, domain.ErrNotFound):
		return "", err
	}
	uid = ulid.Make().String()
	return uid, s.Users.CreateUser(ctx, uid, domain.User{NotionUserID: g.OwnerUserID, Name: g.OwnerName,
		Email: g.OwnerEmail, CreatedAt: domain.Timestamp(s.Now())})
}

// upsertInstallation stores the installation with sealed tokens (C18) and decides its
// status (§2.2 steps 5–7).
func (s *Service) upsertInstallation(ctx context.Context, uid string, g *port.NotionGrant) (*domain.Installation, error) {
	now := s.Now()
	inst, err := s.Installations.GetInstallation(ctx, g.BotID)
	if errors.Is(err, domain.ErrNotFound) {
		inst, err = &domain.Installation{CreatedAt: domain.Timestamp(now)}, nil
	}
	if err != nil {
		return nil, err
	}
	prevLibrary, prevDaily, prevSetupError := inst.LibraryDSID, inst.DailyDSID, inst.SetupError
	aad := []byte(g.BotID)
	if inst.AccessTokenEnc, err = s.Sealer.Seal([]byte(g.AccessToken), aad); err != nil {
		return nil, err
	}
	inst.RefreshTokenEnc = ""
	if g.RefreshToken != "" {
		if inst.RefreshTokenEnc, err = s.Sealer.Seal([]byte(g.RefreshToken), aad); err != nil {
			return nil, err
		}
	}
	inst.TokenExpiresAt = ""
	if g.ExpiresIn > 0 {
		inst.TokenExpiresAt = domain.Timestamp(now.Add(time.Duration(g.ExpiresIn) * time.Second))
	}
	inst.TokenVersion++
	inst.UserID, inst.WorkspaceID, inst.WorkspaceName, inst.WorkspaceIcon = uid, g.WorkspaceID, g.WorkspaceName, g.WorkspaceIcon
	inst.UpdatedAt, inst.SetupError = domain.Timestamp(now), ""

	switch {
	case g.DuplicatedTemplateID != "":
		inst.DuplicatedTemplateID = g.DuplicatedTemplateID
		db, err := s.Workspace.ResolveTemplate(ctx, g.AccessToken, g.DuplicatedTemplateID)
		if err != nil {
			inst.Status, inst.SetupError = domain.StatusNeedsSetup, domain.SetupReason(err)
			s.Log.WarnContext(ctx, "oauth: template resolution failed", "reason", inst.SetupError, "err", err)
		} else {
			inst.ApplyDatabases(db)
			inst.Status = domain.StatusActive
		}
	case inst.HasDatabases():
		inst.Status = domain.StatusActive // reconnect to an already set-up workspace
	default:
		inst.Status = domain.StatusNeedsSetup
	}

	if err := s.Installations.PutInstallation(ctx, g.BotID, inst); err != nil {
		return nil, err
	}
	if err := s.Installations.PutUserInstallation(ctx, uid, g.BotID, now); err != nil {
		return nil, err
	}
	if err := s.Users.SetActiveInstallation(ctx, uid, g.BotID); err != nil {
		return nil, err
	}
	if inst.Status == domain.StatusActive {
		// New databases (e.g. a newly duplicated template) → re-send everything (scenario 10).
		// So does recovering from a failed resync, which may have been a full one.
		full := inst.LibraryDSID != prevLibrary || inst.DailyDSID != prevDaily || prevSetupError == domain.SetupErrResyncFailed
		if err := s.Activator.Activate(ctx, inst, full); err != nil {
			return nil, err
		}
	}
	return inst, nil
}
```

- [ ] **Step 5: Make the handlers thin**

`backend/internal/httpapi/app.go`:

```go
// Package httpapi is the Echo adapter behind the api Lambda's Function URL (spec §2).
// Handlers decode requests, call use cases and encode responses; nothing else.
package httpapi

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/syncsvc"
	"reading-tracker/backend/internal/usecase/auth"
	"reading-tracker/backend/internal/usecase/connect"
	"reading-tracker/backend/internal/usecase/setup"
)

// Deps are the use cases behind the routes. Store and Queue serve the handlers that
// Task 7 moves to use cases.
type Deps struct {
	Config  *config.Config
	Auth    *auth.Service
	Connect *connect.Service
	Setup   *setup.Service
	Store   *dynamo.Store
	Queue   *queue.SyncQueue
	Now     func() time.Time
	Log     *slog.Logger
}

type handlers struct {
	Deps
	sync *syncsvc.Service
}

// New builds the app: /v1 routes, §2.0 JSON errors, 256 KB body limit and
// path-only request logs. CORS is configured on the Function URL, not here.
func New(d Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner, e.HidePort = true, true
	e.HTTPErrorHandler = errorHandler(d.Log)
	e.Use(middleware.Recover())
	e.Use(requestLogger(d.Log))
	e.Use(middleware.BodyLimit("256K"))

	h := &handlers{Deps: d, sync: &syncsvc.Service{Store: d.Store, Queue: d.Queue, Now: d.Now}}
	v1 := e.Group("/v1")
	v1.GET("/config", h.getConfig)
	v1.GET("/me", h.getMe, h.requireDevice)
	v1.GET("/oauth/notion/start", h.oauthStart)
	v1.GET("/oauth/notion/callback", h.oauthCallback)
	v1.POST("/auth/token", h.authToken)
	v1.POST("/auth/logout", h.logout, h.requireDevice)
	v1.GET("/setup/pages", h.setupPages, h.requireDevice)
	v1.POST("/setup", h.postSetup, h.requireDevice)
	v1.POST("/articles/status", h.articlesStatus, h.requireDevice)
	v1.POST("/sync/push", h.syncPush, h.requireDevice)
	v1.DELETE("/me", h.deleteMe, h.requireDevice)
	return e
}
```

`backend/internal/httpapi/errors.go`:
- Keep `apiError` and its constructors for adapter-level failures: decode, route 404/405, and body limit.
- In `toAPIError`, after the `apiError` check, map application errors. The throttle check uses the domain sentinel, as Task 2 set up.

```go
	if ae, ok := apperr.As(err); ok {
		return fromApp(ae)
	}
```

```go
func fromApp(e *apperr.Error) *apiError {
	switch e.Kind {
	case apperr.Invalid:
		return &apiError{http.StatusBadRequest, "invalid_request", e.Msg}
	case apperr.InvalidGrant:
		return &apiError{http.StatusBadRequest, "invalid_grant", e.Msg}
	case apperr.Unauthorized:
		return &apiError{http.StatusUnauthorized, "unauthorized", e.Msg}
	case apperr.NotFound:
		return &apiError{http.StatusNotFound, "not_found", e.Msg}
	case apperr.Conflict:
		return &apiError{http.StatusConflict, "conflict", e.Msg}
	case apperr.NoInstallation:
		return &apiError{http.StatusConflict, "no_installation", e.Msg}
	case apperr.TooLarge:
		return &apiError{http.StatusRequestEntityTooLarge, "payload_too_large", e.Msg}
	case apperr.Capacity:
		return &apiError{http.StatusServiceUnavailable, "capacity_exceeded", e.Msg}
	}
	return &apiError{http.StatusInternalServerError, "internal", "internal error"}
}
```

`backend/internal/httpapi/middleware.go`: delete `Principal`, `touch` and the store lookups. The middleware becomes:

```go
const principalKey = "principal"

func principal(c echo.Context) *auth.Principal { return c.Get(principalKey).(*auth.Principal) }

// requireDevice authenticates "Authorization: Bearer rt_…" (spec §2.4).
func (h *handlers) requireDevice(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tok, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		if !ok {
			return errUnauthorized()
		}
		p, err := h.Auth.Authenticate(c.Request().Context(), tok)
		if err != nil {
			return err
		}
		c.Set(principalKey, p)
		return next(c)
	}
}
```

`requestLogger` is unchanged.

`backend/internal/httpapi/handlers_oauth.go`, entire file:

```go
package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/usecase/connect"
)

const linkExpired = "Link expired, try again"

// oauthStart serves GET /v1/oauth/notion/start (spec §2.1).
func (h *handlers) oauthStart(c echo.Context) error {
	q := c.QueryParams()
	to, err := h.Connect.Start(c.Request().Context(), q.Get("ext_redirect"), q.Get("code_challenge"),
		q.Get("code_challenge_method"), h.redirectURI(c))
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, to)
}

// oauthCallback serves GET /v1/oauth/notion/callback (spec §2.2).
func (h *handlers) oauthCallback(c echo.Context) error {
	q := c.QueryParams()
	r, err := h.Connect.Complete(c.Request().Context(), q.Get("state"), q.Get("code"), q.Get("error"), h.redirectURI(c))
	if errors.Is(err, connect.ErrLinkExpired) {
		return c.String(http.StatusBadRequest, linkExpired)
	}
	if err != nil {
		return err
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return err
	}
	v := u.Query()
	v.Set(r.Param, r.Value)
	u.RawQuery = v.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// redirectURI is PUBLIC_BASE_URL or the request's own origin, plus the callback path (C14).
func (h *handlers) redirectURI(c echo.Context) string {
	base := h.Config.PublicBaseURL
	if base == "" {
		base = c.Scheme() + "://" + c.Request().Host
	}
	return base + "/v1/oauth/notion/callback"
}
```

`backend/internal/httpapi/handlers_auth.go`, entire file:

```go
package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type tokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	DeviceName   string `json:"device_name"`
}

// authToken serves POST /v1/auth/token (spec §2.3).
func (h *handlers) authToken(c echo.Context) error {
	var req tokenRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	g, err := h.Auth.RedeemCode(c.Request().Context(), req.Code, req.CodeVerifier, req.DeviceName)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"device_token": g.DeviceToken,
		"device_id":    g.DeviceID,
		"user":         userView{ID: g.User.ID, Name: g.User.Name},
		"installation": viewInstallation(g.Installation),
	})
}
```

`backend/internal/httpapi/handlers_setup.go`, entire file:

```go
package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type pageView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// setupPages serves GET /v1/setup/pages (spec §2.6).
func (h *handlers) setupPages(c echo.Context) error {
	pages, err := h.Setup.SearchPages(c.Request().Context(), principal(c).InstallationID(), c.QueryParam("q"))
	if err != nil {
		return err
	}
	views := make([]pageView, len(pages))
	for i, p := range pages {
		views[i] = pageView{ID: p.ID, Title: p.Title, URL: p.URL}
	}
	return c.JSON(http.StatusOK, map[string]any{"pages": views})
}

type setupRequest struct {
	ParentPageID string `json:"parent_page_id"`
}

// postSetup serves POST /v1/setup (spec §2.7).
func (h *handlers) postSetup(c echo.Context) error {
	var req setupRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	inst, err := h.Setup.Setup(c.Request().Context(), principal(c).InstallationID(), req.ParentPageID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, viewInstallation(inst))
}
```

Other handler changes:
- `handlers_account.go`: `logout` becomes:

```go
func (h *handlers) logout(c echo.Context) error {
	if err := h.Auth.Logout(c.Request().Context(), principal(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
```

  In `deleteMe`, `h.sq.EnqueuePurge` becomes `h.Queue.EnqueuePurge`.
- `handlers_me.go`: `p.Profile.Name` becomes `p.User.Name`.
- `views.go`: `activeInstallation` takes `*auth.Principal`.
- `handlers_sync.go` and `handlers_articles.go` are unchanged apart from the principal type.

Then delete the old package and run goimports:

```bash
cd backend
git rm -rq internal/setup
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/httpapi
```

- [ ] **Step 6: Compose the API in `app` and switch callers**

Replace `APIDeps` in `backend/internal/app/app.go`:

```go
// APIDeps are the API's infrastructure; NewAPI builds the use cases on top of them.
type APIDeps struct {
	Config *config.Config
	Store  *dynamo.Store
	Notion *notion.Client
	Queue  queue.Sender
	Now    func() time.Time
	Log    *slog.Logger
	Sleep  func(time.Duration)
}

// NewAPI composes the HTTP adapter and its use cases. The API never paces or
// refreshes Notion tokens (C3, C8).
func NewAPI(d APIDeps) *echo.Echo {
	gw := &notion.Gateway{Client: d.Notion}
	sq := &queue.SyncQueue{Sender: d.Queue, Bucket: d.Config.DebounceBucket, Now: d.Now}
	activator := &setup.Activator{Reading: d.Store, Installations: d.Store, Queue: sq, Now: d.Now, Sleep: d.Sleep, Log: d.Log}
	return httpapi.New(httpapi.Deps{
		Config: d.Config,
		Auth: &auth.Service{Users: d.Store, Devices: d.Store, Grants: d.Store, Installations: d.Store,
			Pepper: d.Config.DeviceTokenPepper, Now: d.Now, Log: d.Log},
		Connect: &connect.Service{Users: d.Store, Grants: d.Store, Installations: d.Store, Auth: gw, Workspace: gw,
			Sealer: d.Config.Keys, Activator: activator, AllowedExtensionIDs: d.Config.AllowedExtensionIDs, Now: d.Now, Log: d.Log},
		Setup: &setup.Service{Installations: d.Store, Workspace: gw, Sealer: d.Config.Keys, Activator: activator, Now: d.Now},
		Store: d.Store, Queue: sq, Now: d.Now, Log: d.Log,
	})
}

// API is the production API: unpaced Notion client, real clock.
func API(cfg *config.Config, st *dynamo.Store, q queue.Sender, log *slog.Logger) *echo.Echo {
	return NewAPI(APIDeps{Config: cfg, Store: st, Notion: NotionClient(cfg, nil), Queue: q, Now: time.Now, Log: log})
}
```

In `cmd/api/main.go` and `cmd/local/main.go`, replace `httpapi.New(app.APIDeps(cfg, st, q, log))` with `app.API(cfg, st, q, log)` and drop the `httpapi` import.

In `internal/httpapi/testenv_test.go` and `internal/e2e/e2e_test.go`, replace `httpapi.New(httpapi.Deps{…})` with `app.NewAPI(app.APIDeps{…})`. The field list is identical: `Config`, `Store`, `Notion`, `Queue`, `Now`, `Log`, `Sleep`.

```bash
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/app internal/httpapi internal/e2e cmd
```

- [ ] **Step 7: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`. All existing `httpapi` OAuth, token, setup, auth and logout tests pass unchanged through the new use cases. The new `TestToAPIErrorMapsAppErrors`, `TestActivateEnqueuesPending`, `TestActivateFullResendsSyncedItems` and `TestResyncAfterPushInSameBucketIsDelivered` pass. `TestDependencyRule` passes for the three new use-case packages.

- [ ] **Step 8: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): move auth, OAuth connect and setup into use cases; handlers only translate HTTP"
```

---
### Task 7: Use cases `readsync`, `library`, `account`; HTTP adapter moves to `adapter/httpapi`

**Files:**
- Move: `internal/syncsvc` → `internal/usecase/readsync` (package `readsync`), `internal/httpapi` → `internal/adapter/httpapi`
- Create: `internal/usecase/library/library.go`, `internal/usecase/account/account.go`, `internal/adapter/httpapi/dto.go`
- Modify: `internal/usecase/readsync/types.go` (drop JSON tags), `internal/adapter/httpapi/{app.go,handlers_sync.go,handlers_articles.go,handlers_account.go,handlers_me.go,views.go}`, `internal/app/app.go`
- Test: `internal/adapter/httpapi/app_test.go` (add `TestThrottledReadIs503`)

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces:
  - `readsync`:
    - `readsync.Service{Store readsync.Pusher; Queue port.SyncQueue; Now}`, with `Push(ctx, inst, deviceID string, req PushRequest) (*PushResponse, error)` unchanged.
    - `readsync.PushRequest`, `Article`, `Segment`, `PushResponse` and `Rejection` keep their fields but lose their JSON tags.
    - `readsync.EnvelopeError` and `readsync.ErrAllThrottled` are unchanged.
  - `library`:
    - `library.Service{Reading port.Reading}`, with `Status(ctx, instID string, urls []string) (map[string]library.ArticleStatus, error)`.
    - `library.ArticleStatus{TotalSeconds int64; MaxProgress float64; Sessions int64; Completed bool; LastReadAt, NotionURL string}`.
  - `account`:
    - `account.Service{Users port.Users; Devices port.Devices; Installations port.Installations; Queue port.SyncQueue; Now}`.
    - `Me(ctx, *auth.Principal) (*account.Me, error)` and `Delete(ctx, *auth.Principal) error`.
    - `account.Me{User *domain.User; Device *domain.Device; Installation *domain.Installation}`.
  - `httpapi.Deps{Config *config.Config; Auth *auth.Service; Connect *connect.Service; Setup *setup.Service; ReadSync *readsync.Service; Library *library.Service; Account *account.Service; Log *slog.Logger}`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/httpapi/app_test.go`; it moves with the package in Step 4:

```go
type throttledReadsDB struct{ dynamo.API }

func (throttledReadsDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Review focus 2: a DynamoDB throttle on the API path (auth reads the device token
// first) is still 503 capacity_exceeded now that errors cross the use-case boundary.
func TestThrottledReadIs503(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.seedUser()
	e := app.NewAPI(app.APIDeps{Config: env.cfg, Store: dynamo.New(throttledReadsDB{env.db.DB}, env.db.Table),
		Notion: &notion.Client{BaseURL: env.notion.URL, Version: "2025-09-03"}, Queue: env.queue,
		Now: func() time.Time { return env.now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest("GET", "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 503 || errorCode(t, rec) != "capacity_exceeded" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
```

Fix the imports with goimports: `adapter/dynamo`, `adapter/notion`, `app`, the AWS `aws`, `dynamodb` and `types` packages, `io`, `log/slog`, `net/http/httptest`, `context` and `time`.

Run: `cd backend && go test ./internal/httpapi/ -run TestThrottledReadIs503`
Expected: PASS. This pins Task 2 and Task 6's mapping before the move, and it must keep passing after it.

- [ ] **Step 2: Move `syncsvc` to `usecase/readsync` and drop transport tags**

```bash
cd backend
git mv internal/syncsvc internal/usecase/readsync
perl -pi -e 's/^package syncsvc(_test)?$/package readsync$1/' internal/usecase/readsync/*.go
FILES=$(grep -rl 'internal/syncsvc"\|\bsyncsvc\.' --include='*.go' .)
perl -pi -e 's#reading-tracker/backend/internal/syncsvc"#reading-tracker/backend/internal/usecase/readsync"#g; s/\bsyncsvc\./readsync./g' $FILES
perl -pi -e 's/ `json:"[a-z_]+"`//g' internal/usecase/readsync/types.go
gofmt -w internal/usecase/readsync
```

Change `readsync.Service.Store`'s type from the `Pusher` interface to keep using `Pusher`, the consumer-side interface. Update its signature in `service.go` to `PushSegment(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error)`; Task 2 already did this.

Add the HTTP DTOs, which now carry the JSON contract. `backend/internal/httpapi/dto.go`:

```go
package httpapi

import "reading-tracker/backend/internal/usecase/readsync"

// The JSON shapes of spec §2.8; the use case works on tag-free types.

type articleDTO struct {
	CanonicalURL string `json:"canonical_url"`
	Source       string `json:"source"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	SiteName     string `json:"site_name"`
	WordCount    int    `json:"word_count"`
	PublishedAt  string `json:"published_at"`
	OriginalURL  string `json:"original_url"`
}

type segmentDTO struct {
	ID            string  `json:"id"`
	SessionID     string  `json:"session_id"`
	Seq           int     `json:"seq"`
	CanonicalURL  string  `json:"canonical_url"`
	StartedAt     string  `json:"started_at"`
	EndedAt       string  `json:"ended_at"`
	ActiveSeconds int     `json:"active_seconds"`
	MaxProgress   float64 `json:"max_progress"`
}

type pushRequestDTO struct {
	ClientTime string       `json:"client_time"`
	TZ         string       `json:"tz"`
	Articles   []articleDTO `json:"articles"`
	Segments   []segmentDTO `json:"segments"`
}

func (d pushRequestDTO) toInput() readsync.PushRequest {
	req := readsync.PushRequest{ClientTime: d.ClientTime, TZ: d.TZ,
		Articles: make([]readsync.Article, len(d.Articles)), Segments: make([]readsync.Segment, len(d.Segments))}
	for i, a := range d.Articles {
		req.Articles[i] = readsync.Article(a)
	}
	for i, s := range d.Segments {
		req.Segments[i] = readsync.Segment(s)
	}
	return req
}

type rejectionDTO struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type pushResponseDTO struct {
	Accepted  []string       `json:"accepted"`
	Duplicate []string       `json:"duplicate"`
	Retry     []string       `json:"retry"`
	Rejected  []rejectionDTO `json:"rejected"`
}

func pushResponse(r *readsync.PushResponse) pushResponseDTO {
	out := pushResponseDTO{Accepted: r.Accepted, Duplicate: r.Duplicate, Retry: r.Retry, Rejected: make([]rejectionDTO, len(r.Rejected))}
	for i, x := range r.Rejected {
		out.Rejected[i] = rejectionDTO(x)
	}
	return out
}
```

The conversions `readsync.Article(a)` and `readsync.Segment(s)` compile because the field names, types and order are identical. Go ignores struct tags in conversions.

`readsync.newResponse` initialises every slice to non-nil, so `[]` encodes as `[]` (§2.8).

- [ ] **Step 3: Implement `library` and `account`, and thin the remaining handlers**

`backend/internal/usecase/library/library.go`:

```go
// Package library answers "what do we know about these articles" (spec §2.9).
package library

import (
	"context"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

const maxStatusURLs = 100

type Service struct {
	Reading port.Reading
}

// ArticleStatus is one known article; NotionURL is "" until a page exists.
type ArticleStatus struct {
	TotalSeconds int64
	MaxProgress  float64
	Sessions     int64
	Completed    bool
	LastReadAt   string
	NotionURL    string
}

// Status returns the known articles keyed by the caller's URL strings. Unknown URLs
// are omitted.
func (s *Service) Status(ctx context.Context, instID string, urls []string) (map[string]ArticleStatus, error) {
	if len(urls) > maxStatusURLs {
		return nil, apperr.NewTooLarge("canonical_urls exceeds 100")
	}
	if instID == "" {
		return nil, apperr.NewNoInstallation()
	}
	urlByHash := map[string]string{}
	hashes := make([]string, 0, len(urls))
	for _, u := range urls {
		h := domain.URLHash(u)
		if _, dup := urlByHash[h]; !dup {
			urlByHash[h] = u
			hashes = append(hashes, h)
		}
	}
	arts, err := s.Reading.BatchGetArticles(ctx, instID, hashes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ArticleStatus, len(arts))
	for hash, a := range arts {
		out[urlByHash[hash]] = ArticleStatus{TotalSeconds: a.TotalSeconds, MaxProgress: a.MaxProgress, Sessions: a.Sessions,
			Completed: a.Completed, LastReadAt: a.LastReadAt, NotionURL: a.NotionURL}
	}
	return out, nil
}
```

`backend/internal/usecase/account/account.go`:

```go
// Package account serves the signed-in user's own account (spec §2.5, §2.17).
package account

import (
	"context"
	"errors"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/auth"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Users         port.Users
	Devices       port.Devices
	Installations port.Installations
	Queue         port.SyncQueue
	Now           func() time.Time
}

// Me is the caller's user, device and active installation (nil if none).
type Me struct {
	User         *domain.User
	Device       *domain.Device
	Installation *domain.Installation
}

func (s *Service) Me(ctx context.Context, p *auth.Principal) (*Me, error) {
	me := &Me{User: p.User, Device: p.Device}
	if id := p.InstallationID(); id != "" {
		inst, err := s.Installations.GetInstallation(ctx, id)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		me.Installation = inst
	}
	return me, nil
}

// Delete enqueues the purge, marks the profile deleted, and revokes every device.
// Notion content is never deleted (spec §2.17).
//
// The order keeps every failure retryable. Nothing irreversible happens before the
// purge is queued, so a failed enqueue leaves the account untouched. If marking fails
// after the enqueue, the worker ignores the purge (live user) and the retry re-sends
// and marks. Once marked, auth rejects the user, so a failed revoke still locks them
// out and the purge removes the devices anyway. This relies on the queue's delivery
// delay (≥ the 120 s debounce bucket) exceeding this call's runtime.
func (s *Service) Delete(ctx context.Context, p *auth.Principal) error {
	devices, err := s.Devices.ListDevices(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := s.Queue.EnqueuePurge(ctx, p.UserID, p.InstallationID()); err != nil {
		return err
	}
	now := s.Now()
	if err := s.Users.MarkProfileDeleted(ctx, p.UserID, now); err != nil {
		return err
	}
	for _, d := range devices {
		if d.Revoked() {
			continue
		}
		if err := s.Devices.RevokeDevice(ctx, p.UserID, d.ID, d.TokenHMAC, now); err != nil {
			return err
		}
	}
	return nil
}
```

`backend/internal/httpapi/app.go`:
- Replace `Deps` with the final version:

```go
type Deps struct {
	Config   *config.Config
	Auth     *auth.Service
	Connect  *connect.Service
	Setup    *setup.Service
	ReadSync *readsync.Service
	Library  *library.Service
	Account  *account.Service
	Log      *slog.Logger
}

type handlers struct{ Deps }
```

- In `New`, `h := &handlers{Deps: d}`.
- Remove the `dynamo`, `queue` and `readsync`-construction imports.

`backend/internal/httpapi/handlers_sync.go`:

```go
// syncPush serves POST /v1/sync/push (spec §2.8). It is accepted whatever the
// installation status; Notion catches up via resync.
func (h *handlers) syncPush(c echo.Context) error {
	p := principal(c)
	if p.InstallationID() == "" {
		return errNoInstallation()
	}
	var req pushRequestDTO
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	resp, err := h.ReadSync.Push(c.Request().Context(), p.InstallationID(), p.Device.ID, req.toInput())
	var env *readsync.EnvelopeError
	switch {
	case errors.As(err, &env):
		if env.TooLarge {
			return errTooLarge(env.Msg)
		}
		return errInvalid("%s", env.Msg)
	case errors.Is(err, readsync.ErrAllThrottled):
		return errCapacity()
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, pushResponse(resp))
}
```

`backend/internal/httpapi/handlers_articles.go`:

```go
type statusRequest struct {
	CanonicalURLs []string `json:"canonical_urls"`
}

type articleStatus struct {
	TotalSeconds int64   `json:"total_seconds"`
	MaxProgress  float64 `json:"max_progress"`
	Sessions     int64   `json:"sessions"`
	Completed    bool    `json:"completed"`
	LastReadAt   string  `json:"last_read_at"`
	NotionURL    *string `json:"notion_url"`
}

// articlesStatus serves POST /v1/articles/status (spec §2.9).
func (h *handlers) articlesStatus(c echo.Context) error {
	var req statusRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	got, err := h.Library.Status(c.Request().Context(), principal(c).InstallationID(), req.CanonicalURLs)
	if err != nil {
		return err
	}
	items := make(map[string]articleStatus, len(got))
	for u, a := range got {
		st := articleStatus{TotalSeconds: a.TotalSeconds, MaxProgress: a.MaxProgress, Sessions: a.Sessions,
			Completed: a.Completed, LastReadAt: a.LastReadAt}
		if a.NotionURL != "" {
			n := a.NotionURL
			st.NotionURL = &n
		}
		items[u] = st
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}
```

The old handler checked the 100-URL limit before the installation, and returned 413 for a request that had both problems. `library.Status` keeps that order.

`backend/internal/httpapi/handlers_me.go`:

```go
// getMe serves GET /v1/me (spec §2.5).
func (h *handlers) getMe(c echo.Context) error {
	me, err := h.Account.Me(c.Request().Context(), principal(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user":         userView{ID: me.User.ID, Name: me.User.Name},
		"device":       deviceView{ID: me.Device.ID, Name: me.Device.Name},
		"installation": viewInstallation(me.Installation),
	})
}
```

In `backend/internal/httpapi/handlers_account.go`, `deleteMe` becomes:

```go
// deleteMe serves DELETE /v1/me (spec §2.17).
func (h *handlers) deleteMe(c echo.Context) error {
	if err := h.Account.Delete(c.Request().Context(), principal(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}
```

In `backend/internal/httpapi/views.go`, delete `activeInstallation` and its imports.

- [ ] **Step 4: Move the adapter and compose it**

```bash
cd backend
git mv internal/httpapi internal/adapter/httpapi
FILES=$(grep -rl 'reading-tracker/backend/internal/httpapi"' --include='*.go' .)
perl -pi -e 's#reading-tracker/backend/internal/httpapi"#reading-tracker/backend/internal/adapter/httpapi"#g' $FILES
```

In `backend/internal/app/app.go`, `NewAPI` passes the final `Deps`:

```go
	return httpapi.New(httpapi.Deps{
		Config: d.Config,
		Auth: &auth.Service{Users: d.Store, Devices: d.Store, Grants: d.Store, Installations: d.Store,
			Pepper: d.Config.DeviceTokenPepper, Now: d.Now, Log: d.Log},
		Connect: &connect.Service{Users: d.Store, Grants: d.Store, Installations: d.Store, Auth: gw, Workspace: gw,
			Sealer: d.Config.Keys, Activator: activator, AllowedExtensionIDs: d.Config.AllowedExtensionIDs, Now: d.Now, Log: d.Log},
		Setup:    &setup.Service{Installations: d.Store, Workspace: gw, Sealer: d.Config.Keys, Activator: activator, Now: d.Now},
		ReadSync: &readsync.Service{Store: d.Store, Queue: sq, Now: d.Now},
		Library:  &library.Service{Reading: d.Store},
		Account:  &account.Service{Users: d.Store, Devices: d.Store, Installations: d.Store, Queue: sq, Now: d.Now},
		Log:      d.Log,
	})
```

```bash
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w internal/app internal/adapter/httpapi internal/usecase cmd internal/e2e
```

- [ ] **Step 5: Run the full suite**

Run: `cd backend && gofmt -l . ; go vet ./... && go test -race ./...`
Expected: gofmt prints nothing and every package is `ok`.
- `internal/adapter/httpapi` holds every former `httpapi` test, unchanged, plus `TestThrottledReadIs503`.
- `internal/usecase/readsync` holds the former `syncsvc` tests, unchanged.
- `TestDependencyRule` now covers `adapter/httpapi`, which must import no other adapter: `grep -n 'internal/adapter/' internal/adapter/httpapi/*.go | grep -v _test.go` prints nothing.

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): move push, article status and account into use cases; HTTP adapter now depends only on use cases"
```

---
### Task 8: Composition root `wire`, final dependency rules, docs, verification

**Files:**
- Move: `internal/app` → `internal/wire` (package `wire`)
- Modify: `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/local/main.go`, `internal/e2e/e2e_test.go`, `internal/adapter/httpapi/testenv_test.go`, `internal/adapter/httpapi/app_test.go`, `internal/usecase/notionsync/env_test.go` (if it imports `app`), `internal/archtest/arch_test.go`, `backend/README.md`

**Interfaces:**
- Consumes: Tasks 1–7.
- Produces:
  - `wire.APIDeps`, `wire.NewAPI` and `wire.API`.
  - `wire.WorkerDeps`, `wire.NewWorker` and `wire.Worker`.
  - `wire.AWSConfig`, `wire.DynamoDB`, `wire.SQS`, `wire.NotionClient` and `wire.Logger`.
  - These are the same signatures `app` had, under the new package name.
  - Final archtest rules:
    - only the layer directories exist under `internal/`;
    - nothing under `internal/` imports `wire` except `wire` itself; `cmd/*` and tests are exempt.

- [ ] **Step 1: Write the failing architecture tests**

In `backend/internal/archtest/arch_test.go`:
- Add the `wire` rule as the first `case` inside `violation`'s `switch`:

```go
	case internal && under(target, "internal/wire") && !under(pkg, "internal/wire"):
		return "only cmd/* may import wire"
```

- Change `TestViolationRules`' `{"internal/adapter/queue", module + "/internal/wire", true}` case to keep expecting true. Add:

```go
		{"internal/usecase/setup", module + "/internal/wire", true},
		{"internal/domain", module + "/internal/wire", true},
```

- Add:

```go
// Every production package lives in a layer directory.
func TestOnlyLayerDirectories(t *testing.T) {
	layers := []string{"internal/domain", "internal/usecase", "internal/adapter", "internal/platform", "internal/wire"}
	for pkg := range packageImports(t) {
		ok := false
		for _, l := range layers {
			if under(pkg, l) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s is outside the layer directories %v", pkg, layers)
		}
	}
}
```

Run: `cd backend && go test ./internal/archtest/...`
Expected: FAIL. `TestOnlyLayerDirectories` reports `internal/app is outside the layer directories`.

- [ ] **Step 2: Rename `app` to `wire`**

```bash
cd backend
git mv internal/app internal/wire
perl -pi -e 's/^package app$/package wire/; s/^\/\/ Package app /\/\/ Package wire /' internal/wire/*.go
FILES=$(grep -rl 'reading-tracker/backend/internal/app"' --include='*.go' .)
perl -pi -e 's#reading-tracker/backend/internal/app"#reading-tracker/backend/internal/wire"#g; s/\bapp\.(NewAPI|API|APIDeps|NewWorker|Worker|WorkerDeps|AWSConfig|DynamoDB|SQS|NotionClient|Logger)\b/wire.$1/g' $FILES
go run golang.org/x/tools/cmd/goimports@v0.34.0 -w $FILES
```

Update the package comment in `internal/wire/app.go` to:

```go
// Package wire is the composition root: it builds adapters, injects them into use
// cases, and hands cmd/* a ready API handler or SQS consumer. Only cmd/* (and
// integration tests) import it.
```

- [ ] **Step 3: Verify no old package remains**

```bash
cd backend
for d in store notion queue worker setup syncsvc httpapi crypto config textutil app; do
  test -e internal/$d && echo "leftover: internal/$d"
done
go list ./... | sort
```

Expected: no `leftover:` lines. The list shows only `cmd/{api,local,worker}` and these packages:
- `internal/domain`
- `internal/usecase/{port,apperr,auth,connect,setup,readsync,library,account,notionsync}`
- `internal/adapter/{dynamo,dynamo/dynamotest,notion,notion/notiontest,queue,httpapi}`
- `internal/platform/{crypto,config,textutil}`
- `internal/wire`, `internal/archtest`, `internal/e2e`

- [ ] **Step 4: Document the architecture**

In `backend/README.md`, replace the package-map section with:

```markdown
## Architecture

Clean architecture: dependencies point inward, enforced by `internal/archtest`.

| Layer | Packages | May import |
|---|---|---|
| Domain | `internal/domain` — entities and business rules | standard library only |
| Use cases | `internal/usecase/{auth,connect,setup,readsync,library,account,notionsync}`; interfaces in `usecase/port`; error kinds in `usecase/apperr` | domain, other use cases, `platform/{crypto,textutil}` |
| Adapters | `internal/adapter/dynamo` (DynamoDB), `adapter/notion` (Notion API), `adapter/queue` (SQS/local queue, consumer), `adapter/httpapi` (Echo) | domain, use cases, platform — never another adapter |
| Platform | `internal/platform/{crypto,config,textutil}` | platform only |
| Composition | `internal/wire`, used by `cmd/{api,worker,local}` | everything |

Flow of a read: extension → `adapter/httpapi` → `usecase/readsync` → `port.Reading` (`adapter/dynamo`) +
`port.SyncQueue` (`adapter/queue`) → SQS → `adapter/queue.Consumer` → `usecase/notionsync` →
`port.NotionPages` (`adapter/notion`).

Adding a feature: put rules in `domain`, orchestration in a `usecase` package that declares the
ports it needs, implement those ports in an adapter, and connect them in `wire`.
```

Keep the existing Prerequisites, Local development, Tests and Build sections. Where they mention old package paths, such as `internal/store/storetest`, change them to the new ones.

- [ ] **Step 5: Full verification**

```bash
cd backend
gofmt -l . ; go vet ./... && go test -race -count=1 ./...
go mod tidy -diff
make build && file build/api/bootstrap build/worker/bootstrap
make ddb-up
(make run-local > /tmp/reading-local.log 2>&1 &) ; sleep 4
curl -s localhost:8080/v1/config | head -c 80; echo
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/v1/me
lsof -ti tcp:8080 | xargs kill ; grep -c '"level":"ERROR"' /tmp/reading-local.log
```

Expected:
- gofmt prints nothing, vet is clean, and every package is `ok`.
- `go mod tidy -diff` prints nothing.
- Both binaries are `ELF 64-bit LSB executable, ARM aarch64 … statically linked`.
- The config JSON starts with `{"version":1,"tracking":{"idle_seconds":60`.
- `/v1/me` returns `401`.
- The ERROR count is `0`.

Write `/tmp/reading-local.log` to the session scratchpad if `/tmp` is not allowed.

- [ ] **Step 6: Commit**

```bash
cd /Users/tuandoquoc/personal/reading-chrome-extension
git add -A backend
git commit -m "refactor(backend): wire is the composition root; enforce layer directories; document the architecture"
```

---

## Design coverage

| Approved design item | Task |
|---|---|
| `domain` with entities + rules, stdlib only | 1 |
| Ports owned by use cases (`usecase/port`) | 3, 4, 5 |
| `adapter/dynamo` with private tagged records | 2 |
| `adapter/notion` owns all Notion JSON (properties, schemas, template walk) and error translation | 3, 5 |
| `adapter/queue` (senders, dedup policy, consumer) | 4 |
| `adapter/httpapi` thin (decode → use case → encode) | 6, 7 |
| Use cases auth, connect, setup, readsync, library, account, notionsync | 5, 6, 7 |
| `platform` libs | 1 |
| `wire` composition root | 5–6 (as `app`), 8 |
| Architecture test enforcing the rule | 1, 8 |
| R1 fixed (data source stored and checked before PATCH) | 5 |
| R2 fixed (resync dedup ids never collide with push ids) | 4, 6 |
| Behaviour preserved, existing tests moved not weakened | every task's Step "Run the full suite" |
