# WxPusher WeCom Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that replaces the installed WxPusher Chrome extension as receiver, persists messages, forwards originals to a WeCom robot, and asynchronously enriches linked messages with page text and screenshots.

**Architecture:** A single Go binary contains CLI commands, WxPusher protocol handling, identity import, SQLite-backed durable storage, WeCom delivery, and optional Headless Chrome enrichment. Packages stay small and communicate through explicit interfaces so protocol, storage, dispatch, and browser work can be tested independently.

**Tech Stack:** Go 1.22, `database/sql`, `modernc.org/sqlite`, `github.com/gorilla/websocket`, `github.com/pelletier/go-toml/v2`, `github.com/syndtr/goleveldb/leveldb`, `github.com/chromedp/chromedp`, systemd.

---

## File Structure

- `go.mod`, `go.sum`: module and dependencies.
- `.gitignore`: build output, local config, databases, screenshots.
- `cmd/wxpusher-bridge/main.go`: CLI entrypoint.
- `internal/config/config.go`: TOML config model, defaults, env secret resolution, validation.
- `internal/identity/identity.go`: identity data type and validation.
- `internal/store/store.go`: storage interfaces and shared types.
- `internal/store/sqlite.go`: SQLite implementation and migrations.
- `internal/wxpusher/protocol.go`: protocol constants, WebSocket URL, HTTP headers, message parsing.
- `internal/wxpusher/client.go`: WebSocket connection, heartbeat, reconnect, push-token update callback.
- `internal/wecom/wecom.go`: WeCom webhook payload construction and sending.
- `internal/dispatch/dispatch.go`: durable queue workers for delivery and enrichment.
- `internal/browser/browser.go`: Headless Chrome page title, text, and screenshot capture.
- `internal/importer/json.go`: JSON identity importer.
- `internal/importer/chrome.go`: Chrome profile identity importer from explicit profile and extension id.
- `internal/app/app.go`: runtime composition and graceful shutdown.
- `docs/systemd/wxpusher-bridge.service`: example systemd service.
- `configs/config.example.toml`: example config with empty secrets.

## Task 1: Bootstrap Module, Config, And Identity Model

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/identity/identity.go`
- Create: `internal/identity/identity_test.go`
- Create: `configs/config.example.toml`

- [ ] **Step 1: Initialize the Go module**

Run:

```bash
go mod init github.com/hhh/wxpusher-wecom-bridge
go get github.com/pelletier/go-toml/v2@latest
```

Expected: `go.mod` exists and contains module path `github.com/hhh/wxpusher-wecom-bridge`.

- [ ] **Step 2: Add ignore rules**

Create `.gitignore`:

```gitignore
bin/
dist/
*.db
*.db-shm
*.db-wal
screenshots/
config.local.toml
identity.local.json
coverage.out
```

- [ ] **Step 3: Write failing config tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndEnvWebhook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[storage]
sqlite_path = "`+filepath.Join(dir, "bridge.db")+`"

[wecom]
webhook_env = "TEST_WECOM_WEBHOOK"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_WECOM_WEBHOOK", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.WxPusher.Host != "wxpusher.zjiecode.com" {
		t.Fatalf("host = %q", cfg.WxPusher.Host)
	}
	if cfg.WxPusher.Version != "1.1.0" {
		t.Fatalf("version = %q", cfg.WxPusher.Version)
	}
	if cfg.WeCom.WebhookURL != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc" {
		t.Fatalf("webhook url not resolved from env")
	}
}

func TestValidateRejectsMissingWebhook(t *testing.T) {
	cfg := Default()
	cfg.Storage.SQLitePath = filepath.Join(t.TempDir(), "bridge.db")
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected missing webhook error")
	}
}
```

- [ ] **Step 4: Implement config loading**

Create `internal/config/config.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	WxPusher WxPusherConfig `toml:"wxpusher"`
	Storage  StorageConfig  `toml:"storage"`
	WeCom    WeComConfig    `toml:"wecom"`
	Browser  BrowserConfig  `toml:"browser"`
	Retry    RetryConfig    `toml:"retry"`
}

type WxPusherConfig struct {
	Host     string `toml:"host"`
	Version  string `toml:"version"`
	Platform string `toml:"platform"`
}

type StorageConfig struct {
	SQLitePath string `toml:"sqlite_path"`
}

type WeComConfig struct {
	WebhookURL string `toml:"webhook_url"`
	WebhookEnv string `toml:"webhook_env"`
}

type BrowserConfig struct {
	Enabled         bool   `toml:"enabled"`
	ChromePath      string `toml:"chrome_path"`
	ScreenshotDir   string `toml:"screenshot_dir"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
	SummaryMaxChars int    `toml:"summary_max_chars"`
}

type RetryConfig struct {
	WeComMaxAttempts      int `toml:"wecom_max_attempts"`
	EnrichmentMaxAttempts int `toml:"enrichment_max_attempts"`
	InitialBackoffSeconds int `toml:"initial_backoff_seconds"`
	MaxBackoffSeconds     int `toml:"max_backoff_seconds"`
}

func Default() Config {
	platform := "Chrome-Other"
	switch runtime.GOOS {
	case "linux":
		platform = "Chrome-Linux"
	case "darwin":
		platform = "Chrome-Mac"
	}
	return Config{
		WxPusher: WxPusherConfig{
			Host:     "wxpusher.zjiecode.com",
			Version:  "1.1.0",
			Platform: platform,
		},
		Storage: StorageConfig{SQLitePath: "/var/lib/wxpusher-bridge/bridge.db"},
		WeCom:   WeComConfig{WebhookEnv: "WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL"},
		Browser: BrowserConfig{
			Enabled:         true,
			ChromePath:      "/usr/bin/chromium",
			ScreenshotDir:   "/var/lib/wxpusher-bridge/screenshots",
			TimeoutSeconds:  20,
			SummaryMaxChars: 1000,
		},
		Retry: RetryConfig{
			WeComMaxAttempts:      5,
			EnrichmentMaxAttempts: 3,
			InitialBackoffSeconds: 2,
			MaxBackoffSeconds:     60,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.WeCom.WebhookURL == "" && cfg.WeCom.WebhookEnv != "" {
		cfg.WeCom.WebhookURL = os.Getenv(cfg.WeCom.WebhookEnv)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.WxPusher.Host == "" {
		problems = append(problems, "wxpusher.host is required")
	}
	if c.WxPusher.Version == "" {
		problems = append(problems, "wxpusher.version is required")
	}
	if c.WxPusher.Platform == "" {
		problems = append(problems, "wxpusher.platform is required")
	}
	if c.Storage.SQLitePath == "" {
		problems = append(problems, "storage.sqlite_path is required")
	}
	if c.WeCom.WebhookURL == "" {
		problems = append(problems, "wecom webhook URL is required through wecom.webhook_url or wecom.webhook_env")
	}
	if c.Retry.WeComMaxAttempts < 1 {
		problems = append(problems, "retry.wecom_max_attempts must be >= 1")
	}
	if c.Retry.EnrichmentMaxAttempts < 1 {
		problems = append(problems, "retry.enrichment_max_attempts must be >= 1")
	}
	if c.Browser.Enabled && c.Browser.TimeoutSeconds < 1 {
		problems = append(problems, "browser.timeout_seconds must be >= 1 when browser is enabled")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func RedactSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "***"
	}
	return fmt.Sprintf("%s***%s", s[:4], s[len(s)-4:])
}
```

- [ ] **Step 5: Write failing identity tests**

Create `internal/identity/identity_test.go`:

```go
package identity

import "testing"

func TestIdentityValidateRequiresDeviceFields(t *testing.T) {
	id := Identity{DeviceUUID: "du", DeviceToken: "dt", PushToken: "pt", Platform: "Chrome-Linux", Version: "1.1.0", Source: "json"}
	if err := id.Validate(); err != nil {
		t.Fatalf("expected valid identity: %v", err)
	}
	id.DeviceToken = ""
	if err := id.Validate(); err == nil {
		t.Fatal("expected missing device token error")
	}
}
```

- [ ] **Step 6: Implement identity model**

Create `internal/identity/identity.go`:

```go
package identity

import (
	"errors"
	"strings"
	"time"
)

type Identity struct {
	DeviceUUID string    `json:"deviceUuid"`
	DeviceToken string   `json:"deviceToken"`
	PushToken   string   `json:"pushToken"`
	Platform    string   `json:"platform"`
	Version     string   `json:"version"`
	Source      string   `json:"source"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (i Identity) Validate() error {
	var problems []string
	if strings.TrimSpace(i.DeviceUUID) == "" {
		problems = append(problems, "deviceUuid is required")
	}
	if strings.TrimSpace(i.DeviceToken) == "" {
		problems = append(problems, "deviceToken is required")
	}
	if strings.TrimSpace(i.Platform) == "" {
		problems = append(problems, "platform is required")
	}
	if strings.TrimSpace(i.Version) == "" {
		problems = append(problems, "version is required")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (i Identity) WithDefaults(platform, version, source string) Identity {
	if i.Platform == "" {
		i.Platform = platform
	}
	if i.Version == "" {
		i.Version = version
	}
	if i.Source == "" {
		i.Source = source
	}
	if i.UpdatedAt.IsZero() {
		i.UpdatedAt = time.Now().UTC()
	}
	return i
}
```

- [ ] **Step 7: Add example config**

Create `configs/config.example.toml`:

```toml
[wxpusher]
host = "wxpusher.zjiecode.com"
version = "1.1.0"
platform = "Chrome-Linux"

[storage]
sqlite_path = "/var/lib/wxpusher-bridge/bridge.db"

[wecom]
webhook_url = ""
webhook_env = "WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL"

[browser]
enabled = true
chrome_path = "/usr/bin/chromium"
screenshot_dir = "/var/lib/wxpusher-bridge/screenshots"
timeout_seconds = 20
summary_max_chars = 1000

[retry]
wecom_max_attempts = 5
enrichment_max_attempts = 3
initial_backoff_seconds = 2
max_backoff_seconds = 60
```

- [ ] **Step 8: Run tests**

Run:

```bash
go test ./internal/config ./internal/identity
```

Expected: all tests pass.

- [ ] **Step 9: Commit**

Run:

```bash
git add go.mod go.sum .gitignore configs/config.example.toml internal/config internal/identity
git commit -m "feat: add config and identity model"
```

## Task 2: Add SQLite Storage And Migrations

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_test.go`

- [ ] **Step 1: Add SQLite dependency**

Run:

```bash
go get modernc.org/sqlite@latest
```

Expected: `go.mod` includes `modernc.org/sqlite`.

- [ ] **Step 2: Write failing storage tests**

Create `internal/store/sqlite_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func TestSQLiteIdentityRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	id := identity.Identity{DeviceUUID: "du", DeviceToken: "dt", PushToken: "pt", Platform: "Chrome-Linux", Version: "1.1.0", Source: "test", UpdatedAt: time.Now().UTC()}
	if err := st.SaveIdentity(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceUUID != "du" || got.DeviceToken != "dt" || got.PushToken != "pt" {
		t.Fatalf("identity mismatch: %+v", got)
	}
}

func TestSQLiteMessageDedupesByQID(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	msg := Message{QID: "q1", MsgType: 20001, Content: "hello", RawPayload: []byte(`{"qid":"q1"}`), ReceivedAt: time.Now().UTC()}
	first, err := st.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Inserted || second.Inserted {
		t.Fatalf("dedupe flags first=%v second=%v", first.Inserted, second.Inserted)
	}
}

func TestSQLiteTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	saved, err := st.SaveMessage(ctx, Message{QID: "q2", MsgType: 20001, Content: "https://example.com", RawPayload: []byte(`{}`), ReceivedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.EnqueueDelivery(ctx, DeliveryTask{MessageID: saved.ID, Kind: DeliveryOriginal, Payload: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("claimed tasks = %+v", tasks)
	}
	if err := st.MarkDeliveryDone(ctx, taskID, "ok"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: Define storage interfaces and shared types**

Create `internal/store/store.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

type Message struct {
	ID         int64
	QID        string
	DedupeKey  string
	MsgType    int
	Content    string
	RawPayload []byte
	ReceivedAt time.Time
}

type SaveMessageResult struct {
	ID       int64
	Inserted bool
}

type DeliveryKind string

const (
	DeliveryOriginal DeliveryKind = "original"
	DeliveryEnriched DeliveryKind = "enriched"
)

type TaskStatus string

const (
	TaskPending TaskStatus = "pending"
	TaskRunning TaskStatus = "running"
	TaskDone    TaskStatus = "done"
	TaskFailed  TaskStatus = "failed"
)

type DeliveryTask struct {
	ID          int64
	MessageID   int64
	Kind        DeliveryKind
	Payload     string
	Status      TaskStatus
	Attempts    int
	NextAttempt time.Time
	LastError   string
}

type EnrichmentTask struct {
	ID          int64
	MessageID   int64
	URL         string
	Status      TaskStatus
	Attempts    int
	NextAttempt time.Time
	LastError   string
}

type Enrichment struct {
	MessageID  int64
	URL        string
	Title      string
	Summary    string
	Screenshot string
	CreatedAt  time.Time
}

type AppEvent struct {
	Kind      string
	Message   string
	CreatedAt time.Time
}

type Store interface {
	Close() error
	SaveIdentity(context.Context, identity.Identity) error
	LoadIdentity(context.Context) (identity.Identity, error)
	SaveMessage(context.Context, Message) (SaveMessageResult, error)
	EnqueueDelivery(context.Context, DeliveryTask) (int64, error)
	ClaimDeliveryTasks(context.Context, int, time.Time) ([]DeliveryTask, error)
	MarkDeliveryDone(context.Context, int64, string) error
	MarkDeliveryRetry(context.Context, int64, int, time.Time, string) error
	MarkDeliveryFailed(context.Context, int64, string) error
	EnqueueEnrichment(context.Context, EnrichmentTask) (int64, error)
	ClaimEnrichmentTasks(context.Context, int, time.Time) ([]EnrichmentTask, error)
	SaveEnrichment(context.Context, Enrichment) error
	MarkEnrichmentDone(context.Context, int64) error
	MarkEnrichmentRetry(context.Context, int64, int, time.Time, string) error
	MarkEnrichmentFailed(context.Context, int64, string) error
	AddAppEvent(context.Context, AppEvent) error
}
```

- [ ] **Step 4: Implement SQLite schema and methods**

Create `internal/store/sqlite.go` with migrations for the six tables from the design. Implement `OpenSQLite(path string) (*SQLiteStore, error)`, `Close`, and every method in `Store`. Use `INSERT OR IGNORE` for message dedupe and task dedupe. For `SaveMessage`, compute `dedupe_key` as `qid:<qid>` when `QID` is not empty, otherwise `sha256:<hex>` over message type, content, and raw payload.

Use this method signature block exactly:

```go
type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error)
func (s *SQLiteStore) Close() error
func (s *SQLiteStore) SaveIdentity(ctx context.Context, id identity.Identity) error
func (s *SQLiteStore) LoadIdentity(ctx context.Context) (identity.Identity, error)
func (s *SQLiteStore) SaveMessage(ctx context.Context, msg Message) (SaveMessageResult, error)
```

For task claiming, run updates inside a transaction:

```sql
SELECT id FROM delivery_attempts
WHERE status = 'pending' AND next_attempt_at <= ?
ORDER BY id
LIMIT ?;
```

Then set selected rows to `running` before returning them.

- [ ] **Step 5: Run storage tests**

Run:

```bash
go test ./internal/store
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

Run:

```bash
git add go.mod go.sum internal/store
git commit -m "feat: add sqlite store"
```

## Task 3: Implement Identity Importers

**Files:**
- Create: `internal/importer/json.go`
- Create: `internal/importer/json_test.go`
- Create: `internal/importer/chrome.go`
- Create: `internal/importer/chrome_test.go`

- [ ] **Step 1: Add LevelDB dependency**

Run:

```bash
go get github.com/syndtr/goleveldb/leveldb@latest
```

Expected: `go.mod` includes `github.com/syndtr/goleveldb/leveldb`.

- [ ] **Step 2: Write JSON importer test**

Create `internal/importer/json_test.go`:

```go
package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportJSONReadsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	body := `{"deviceUuid":"du","deviceToken":"dt","pushToken":"pt","platform":"Chrome-Linux","version":"1.1.0"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := ImportJSON(path, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if id.DeviceUUID != "du" || id.DeviceToken != "dt" || id.PushToken != "pt" {
		t.Fatalf("identity mismatch: %+v", id)
	}
	if id.Source != "json" {
		t.Fatalf("source = %q", id.Source)
	}
}
```

- [ ] **Step 3: Implement JSON importer**

Create `internal/importer/json.go`:

```go
package importer

import (
	"encoding/json"
	"os"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func ImportJSON(path, defaultPlatform, defaultVersion string) (identity.Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return identity.Identity{}, err
	}
	var id identity.Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return identity.Identity{}, err
	}
	id = id.WithDefaults(defaultPlatform, defaultVersion, "json")
	if err := id.Validate(); err != nil {
		return identity.Identity{}, err
	}
	return id, nil
}
```

- [ ] **Step 4: Write Chrome importer tests**

Create `internal/importer/chrome_test.go`:

```go
package importer

import (
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

func TestImportChromeReadsLocalExtensionSettings(t *testing.T) {
	profile := t.TempDir()
	extID := "abcdefghijklmnopabcdefghijklmnop"
	dbPath := filepath.Join(profile, "Local Extension Settings", extID)
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("deviceUuid"), []byte(`"du"`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("deviceToken"), []byte(`"dt"`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("pushToken"), []byte(`"pt"`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	id, err := ImportChrome(profile, extID, "Chrome-Linux", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if id.DeviceUUID != "du" || id.DeviceToken != "dt" || id.PushToken != "pt" {
		t.Fatalf("identity mismatch: %+v", id)
	}
}
```

- [ ] **Step 5: Implement Chrome importer**

Create `internal/importer/chrome.go`. Implement `ImportChrome(profilePath, extensionID, defaultPlatform, defaultVersion string) (identity.Identity, error)`. The importer must:

- Require explicit `profilePath` and `extensionID`.
- Read `Local Extension Settings/<extensionID>` LevelDB.
- Also read `Local Storage/leveldb` and consider keys containing `chrome-extension://<extensionID>`.
- Copy each LevelDB directory to a temporary directory before opening it, so a running Chrome profile lock does not block read attempts.
- Extract string values for `deviceUuid`, `deviceToken`, and `pushToken`.
- Parse JSON string values such as `"abc"` and raw string values such as `abc`.
- Set source to `chrome`.
- Validate the resulting identity before returning.

Use these helper functions:

```go
func ImportChrome(profilePath, extensionID, defaultPlatform, defaultVersion string) (identity.Identity, error)
func readLevelDBIdentity(path string, keyFilter func(string) bool) (map[string]string, error)
func normalizeChromeStorageKey(key string) string
func decodeChromeStorageValue(value []byte) string
```

- [ ] **Step 6: Run importer tests**

Run:

```bash
go test ./internal/importer
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

Run:

```bash
git add go.mod go.sum internal/importer
git commit -m "feat: add identity importers"
```

## Task 4: Implement WxPusher Protocol Package

**Files:**
- Create: `internal/wxpusher/protocol.go`
- Create: `internal/wxpusher/protocol_test.go`
- Create: `internal/wxpusher/client.go`
- Create: `internal/wxpusher/client_test.go`

- [ ] **Step 1: Add WebSocket dependency**

Run:

```bash
go get github.com/gorilla/websocket@latest
```

Expected: `go.mod` includes `github.com/gorilla/websocket`.

- [ ] **Step 2: Write protocol tests**

Create `internal/wxpusher/protocol_test.go`:

```go
package wxpusher

import (
	"net/http"
	"testing"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func TestBuildWebSocketURLMatchesExtension(t *testing.T) {
	id := identity.Identity{PushToken: "pt", Platform: "Chrome-Linux", Version: "1.1.0"}
	got := BuildWebSocketURL("wxpusher.zjiecode.com", id)
	want := "wss://wxpusher.zjiecode.com/ws?version=1.1.0&platform=Chrome-Linux&pushToken=pt"
	if got != want {
		t.Fatalf("url = %q want %q", got, want)
	}
}

func TestBuildHeadersMatchesExtension(t *testing.T) {
	id := identity.Identity{DeviceToken: "dt", Platform: "Chrome-Linux", Version: "1.1.0"}
	h := BuildHTTPHeaders(id)
	checks := map[string]string{
		"platform":     "Chrome-Linux",
		"version":      "1.1.0",
		"deviceToken":  "dt",
		"Content-Type": "application/json;charset=UTF-8",
	}
	for key, want := range checks {
		if got := h.Get(key); got != want {
			t.Fatalf("%s = %q want %q", key, got, want)
		}
	}
	if got := h.Get("Cookie"); got != "" {
		t.Fatalf("Cookie header must be empty, got %q", got)
	}
	_ = http.Header(h)
}

func TestParseNotification(t *testing.T) {
	msg, err := ParseMessage([]byte(`{"msgType":20001,"content":"hello","qid":"q1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != MsgTypeNotification || msg.Content != "hello" || msg.QID != "q1" {
		t.Fatalf("message mismatch: %+v", msg)
	}
}
```

- [ ] **Step 3: Implement protocol helpers**

Create `internal/wxpusher/protocol.go`:

```go
package wxpusher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

const (
	MsgTypeHeartUp      = 101
	MsgTypeHeart        = 201
	MsgTypeInit         = 202
	MsgTypeError        = 203
	MsgTypeUpdate       = 204
	MsgTypeNotification = 20001
)

type Message struct {
	Type      int             `json:"msgType"`
	Content   string          `json:"content"`
	QID       string          `json:"qid"`
	PushToken string          `json:"pushToken"`
	Title     string          `json:"title"`
	URL       string          `json:"url"`
	Raw       json.RawMessage `json:"-"`
}

func BuildWebSocketURL(host string, id identity.Identity) string {
	values := url.Values{}
	values.Set("version", id.Version)
	values.Set("platform", id.Platform)
	if id.PushToken != "" {
		values.Set("pushToken", id.PushToken)
	}
	return fmt.Sprintf("wss://%s/ws?%s", host, values.Encode())
}

func BuildHTTPHeaders(id identity.Identity) http.Header {
	h := http.Header{}
	h.Set("platform", id.Platform)
	h.Set("version", id.Version)
	h.Set("deviceToken", id.DeviceToken)
	h.Set("Content-Type", "application/json;charset=UTF-8")
	return h
}

func ParseMessage(raw []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, err
	}
	msg.Raw = append([]byte(nil), raw...)
	return msg, nil
}

func HeartbeatPayload() []byte {
	return []byte(`{"msgType":101}`)
}
```

- [ ] **Step 4: Write client tests**

Create `internal/wxpusher/client_test.go`:

```go
package wxpusher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func TestRegisterDeviceUsesExtensionHeaders(t *testing.T) {
	var gotDeviceToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDeviceToken = r.Header.Get("deviceToken")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1000,"data":{"deviceUuid":"du","deviceToken":"newdt"}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, http.DefaultClient)
	id := identity.Identity{DeviceUUID: "du", DeviceToken: "dt", Platform: "Chrome-Linux", Version: "1.1.0"}
	next, err := c.UpdatePushToken(context.Background(), id, "pt2")
	if err != nil {
		t.Fatal(err)
	}
	if gotDeviceToken != "dt" {
		t.Fatalf("deviceToken header = %q", gotDeviceToken)
	}
	if next.DeviceToken != "newdt" || next.PushToken != "pt2" {
		t.Fatalf("identity mismatch: %+v", next)
	}
}
```

- [ ] **Step 5: Implement HTTP register-device client and WebSocket runner**

Create `internal/wxpusher/client.go`. It must include:

- `type HTTPClient struct { baseURL string; client *http.Client }`
- `func NewHTTPClient(baseURL string, client *http.Client) *HTTPClient`
- `func (c *HTTPClient) UpdatePushToken(ctx context.Context, id identity.Identity, pushToken string) (identity.Identity, error)`
- `type WSClient struct` with config fields for host, current identity, logger, and message handler callback.
- `func (c *WSClient) Run(ctx context.Context) error`

`UpdatePushToken` must POST to `/api/device/register-device` with JSON:

```json
{"pushToken":"<pushToken>","deviceUuid":"<deviceUuid>"}
```

It must parse `{"code":1000,"data":{"deviceUuid":"...","deviceToken":"..."}}`. Any other `code` returns an error containing the server message.

`WSClient.Run` must:

- Connect to `BuildWebSocketURL`.
- Send `HeartbeatPayload()` every 26 seconds.
- Parse messages with `ParseMessage`.
- Call the handler for init, update, and notification messages.
- Return when context is canceled.
- Reconnect responsibility stays in `internal/app` so the WebSocket package stays focused.

- [ ] **Step 6: Run WxPusher tests**

Run:

```bash
go test ./internal/wxpusher
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

Run:

```bash
git add go.mod go.sum internal/wxpusher
git commit -m "feat: add wxpusher protocol client"
```

## Task 5: Implement WeCom Sender

**Files:**
- Create: `internal/wecom/wecom.go`
- Create: `internal/wecom/wecom_test.go`

- [ ] **Step 1: Write WeCom tests**

Create `internal/wecom/wecom_test.go`:

```go
package wecom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildOriginalMarkdown(t *testing.T) {
	payload := BuildOriginalMarkdown("q1", "hello")
	if !strings.Contains(payload.Content, "WxPusher 通知") {
		t.Fatalf("missing title: %q", payload.Content)
	}
	if !strings.Contains(payload.Content, "q1") || !strings.Contains(payload.Content, "hello") {
		t.Fatalf("missing content: %q", payload.Content)
	}
}

func TestSendPostsMarkdownPayload(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	client := New(srv.URL, http.DefaultClient)
	if err := client.Send(context.Background(), BuildOriginalMarkdown("q1", "hello")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"msgtype":"markdown"`) {
		t.Fatalf("body = %s", body)
	}
}
```

- [ ] **Step 2: Implement WeCom sender**

Create `internal/wecom/wecom.go`:

```go
package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type MarkdownPayload struct {
	Content string
}

type requestBody struct {
	MsgType  string          `json:"msgtype"`
	Markdown MarkdownPayload `json:"markdown"`
}

type Client struct {
	webhook string
	http    *http.Client
}

func New(webhook string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{webhook: webhook, http: httpClient}
}

func BuildOriginalMarkdown(qid, content string) MarkdownPayload {
	return MarkdownPayload{Content: fmt.Sprintf("**WxPusher 通知**\n\n消息 ID: `%s`\n\n%s", qid, content)}
}

func BuildEnrichedMarkdown(qid, pageURL, title, summary, screenshotPath string) MarkdownPayload {
	return MarkdownPayload{Content: fmt.Sprintf("**WxPusher 链接富化**\n\n消息 ID: `%s`\n\nURL: %s\n\n标题: %s\n\n摘要:\n%s\n\n截图: `%s`", qid, pageURL, title, summary, screenshotPath)}
}

func (c *Client) Send(ctx context.Context, payload MarkdownPayload) error {
	body, err := json.Marshal(requestBody{MsgType: "markdown", Markdown: payload})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json;charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wecom status %d", resp.StatusCode)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func (c *Client) SendMarkdown(ctx context.Context, content string) error {
	return c.Send(ctx, MarkdownPayload{Content: content})
}
```

- [ ] **Step 3: Run WeCom tests**

Run:

```bash
go test ./internal/wecom
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/wecom
git commit -m "feat: add wecom sender"
```

## Task 6: Implement Browser Enrichment

**Files:**
- Create: `internal/browser/browser.go`
- Create: `internal/browser/browser_test.go`

- [ ] **Step 1: Add browser dependency**

Run:

```bash
go get github.com/chromedp/chromedp@latest
```

Expected: `go.mod` includes `github.com/chromedp/chromedp`.

- [ ] **Step 2: Write URL extraction tests**

Create `internal/browser/browser_test.go`:

```go
package browser

import "testing"

func TestExtractURLs(t *testing.T) {
	got := ExtractURLs("read https://example.com/a?b=1 and http://x.test/path.")
	if len(got) != 2 {
		t.Fatalf("urls = %+v", got)
	}
	if got[0] != "https://example.com/a?b=1" {
		t.Fatalf("first url = %q", got[0])
	}
	if got[1] != "http://x.test/path" {
		t.Fatalf("second url = %q", got[1])
	}
}

func TestTrimSummaryKeepsRuneBoundary(t *testing.T) {
	got := TrimSummary("你好世界", 3)
	if got != "你好世" {
		t.Fatalf("summary = %q", got)
	}
}
```

- [ ] **Step 3: Implement browser helpers and fetcher**

Create `internal/browser/browser.go`. Include:

```go
package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type Config struct {
	Enabled         bool
	ChromePath      string
	ScreenshotDir   string
	Timeout         time.Duration
	SummaryMaxChars int
}

type Result struct {
	URL        string
	Title      string
	Summary    string
	Screenshot string
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func ExtractURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		m = strings.TrimRight(m, ".,;!?)，。；！）")
		if _, err := url.ParseRequestURI(m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func TrimSummary(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

type Fetcher struct {
	cfg Config
}

func NewFetcher(cfg Config) *Fetcher {
	return &Fetcher{cfg: cfg}
}

func (f *Fetcher) Fetch(ctx context.Context, pageURL string) (Result, error) {
	if err := os.MkdirAll(f.cfg.ScreenshotDir, 0o755); err != nil {
		return Result{}, err
	}
	timeout := f.cfg.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Headless, chromedp.DisableGPU)
	if f.cfg.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(f.cfg.ChromePath))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var title string
	var innerText string
	var screenshot []byte
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Text("body", &innerText, chromedp.ByQuery),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256([]byte(pageURL))
	path := filepath.Join(f.cfg.ScreenshotDir, hex.EncodeToString(sum[:])+".png")
	if err := os.WriteFile(path, screenshot, 0o644); err != nil {
		return Result{}, err
	}
	return Result{URL: pageURL, Title: title, Summary: TrimSummary(innerText, f.cfg.SummaryMaxChars), Screenshot: path}, nil
}
```

- [ ] **Step 4: Run browser unit tests**

Run:

```bash
go test ./internal/browser
```

Expected: unit tests pass without launching Chrome.

- [ ] **Step 5: Commit**

Run:

```bash
git add go.mod go.sum internal/browser
git commit -m "feat: add browser enrichment helpers"
```

## Task 7: Implement Dispatch Pipeline

**Files:**
- Create: `internal/dispatch/dispatch.go`
- Create: `internal/dispatch/dispatch_test.go`

- [ ] **Step 1: Write dispatch tests**

Create `internal/dispatch/dispatch_test.go`:

```go
package dispatch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
)

type fakeSender struct {
	payloads []string
	fail     bool
}

func (f *fakeSender) SendMarkdown(ctx context.Context, content string) error {
	if f.fail {
		return errFake
	}
	f.payloads = append(f.payloads, content)
	return nil
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(ctx context.Context, pageURL string) (EnrichmentResult, error) {
	return EnrichmentResult{URL: pageURL, Title: "Example", Summary: "Body", Screenshot: "/tmp/example.png"}, nil
}

func TestHandleMessagePersistsAndQueues(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	svc := New(st, nil, nil, Config{WeComMaxAttempts: 3, EnrichmentMaxAttempts: 2})
	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q1", MsgType: 20001, Content: "hello https://example.com", RawPayload: []byte(`{}`), ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("delivery count = %d", len(deliveries))
	}
	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(enrichments) != 1 || enrichments[0].URL != "https://example.com" {
		t.Fatalf("enrichment tasks = %+v", enrichments)
	}
}
```

- [ ] **Step 2: Implement dispatch service**

Create `internal/dispatch/dispatch.go`. Define:

```go
package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/browser"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wecom"
)

var errFake = errors.New("fake")

type Sender interface {
	SendMarkdown(context.Context, string) error
}

type Fetcher interface {
	Fetch(context.Context, string) (EnrichmentResult, error)
}

type IncomingMessage struct {
	QID        string
	MsgType    int
	Content    string
	RawPayload []byte
	ReceivedAt time.Time
}

type EnrichmentResult struct {
	URL        string
	Title      string
	Summary    string
	Screenshot string
}

type Config struct {
	WeComMaxAttempts      int
	EnrichmentMaxAttempts int
	InitialBackoff        time.Duration
	MaxBackoff            time.Duration
}

type Service struct {
	store   store.Store
	sender  Sender
	fetcher Fetcher
	cfg     Config
}

func New(st store.Store, sender Sender, fetcher Fetcher, cfg Config) *Service {
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = 2 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 60 * time.Second
	}
	return &Service{store: st, sender: sender, fetcher: fetcher, cfg: cfg}
}

func (s *Service) HandleMessage(ctx context.Context, msg IncomingMessage) error {
	saved, err := s.store.SaveMessage(ctx, store.Message{QID: msg.QID, MsgType: msg.MsgType, Content: msg.Content, RawPayload: msg.RawPayload, ReceivedAt: msg.ReceivedAt})
	if err != nil {
		return err
	}
	if !saved.Inserted {
		return nil
	}
	payload := wecom.BuildOriginalMarkdown(msg.QID, msg.Content).Content
	if _, err := s.store.EnqueueDelivery(ctx, store.DeliveryTask{MessageID: saved.ID, Kind: store.DeliveryOriginal, Payload: payload, NextAttempt: time.Now().UTC()}); err != nil {
		return err
	}
	for _, u := range browser.ExtractURLs(msg.Content) {
		if _, err := s.store.EnqueueEnrichment(ctx, store.EnrichmentTask{MessageID: saved.ID, URL: u, NextAttempt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}
```

Then add `RunDeliveryWorker`, `RunEnrichmentWorker`, and `nextAttempt(attempts int) time.Time`. Delivery worker claims pending tasks, calls `sender.SendMarkdown`, marks done, retry, or failed. Enrichment worker claims tasks, calls `fetcher.Fetch`, saves enrichment, marks done, and enqueues enriched delivery using `wecom.BuildEnrichedMarkdown`.

- [ ] **Step 3: Run dispatch tests**

Run:

```bash
go test ./internal/dispatch
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

Run:

```bash
git add internal/dispatch
git commit -m "feat: add dispatch pipeline"
```

## Task 8: Compose CLI And Runtime App

**Files:**
- Create: `cmd/wxpusher-bridge/main.go`
- Create: `internal/app/app.go`
- Create: `internal/app/app_test.go`

- [ ] **Step 1: Write app wiring test**

Create `internal/app/app_test.go`:

```go
package app

import (
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
)

func TestBackoffCapsAtMax(t *testing.T) {
	cfg := config.Default()
	cfg.Retry.InitialBackoffSeconds = 2
	cfg.Retry.MaxBackoffSeconds = 10
	got := Backoff(cfg, 10)
	if got != 10*time.Second {
		t.Fatalf("backoff = %s", got)
	}
}
```

- [ ] **Step 2: Implement app composition**

Create `internal/app/app.go`. Include:

```go
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
)

type App struct {
	cfg config.Config
	log *slog.Logger
	st  store.Store
}

func New(cfg config.Config, log *slog.Logger, st store.Store) *App {
	return &App{cfg: cfg, log: log, st: st}
}

func Backoff(cfg config.Config, attempts int) time.Duration {
	initial := time.Duration(cfg.Retry.InitialBackoffSeconds) * time.Second
	max := time.Duration(cfg.Retry.MaxBackoffSeconds) * time.Second
	d := initial
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

func (a *App) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
```

After this minimal version passes, expand `Run` to:

- Load identity from store.
- Start dispatch delivery worker.
- Start enrichment worker when browser is enabled.
- Start WxPusher WebSocket loop.
- On `msgType=202`, call `UpdatePushToken`, save identity only when API succeeds, and record app events on failure.
- On `msgType=204`, record a version event.
- On `msgType=20001`, call dispatch `HandleMessage`.
- Reconnect after WebSocket returns unexpectedly using `Backoff`.

- [ ] **Step 3: Implement CLI commands**

Create `cmd/wxpusher-bridge/main.go`. Use the standard `flag` package. Supported commands:

```text
wxpusher-bridge run -config /etc/wxpusher-bridge/config.toml
wxpusher-bridge import-json -config ./config.local.toml -file identity.json
wxpusher-bridge import-chrome -config ./config.local.toml -profile "/path/to/Profile" -extension-id "<id>"
```

`run` loads config, opens SQLite, constructs `app.App`, and waits for SIGINT/SIGTERM. `import-json` and `import-chrome` load config, import identity, open SQLite, save identity, and print a redacted success message that includes `deviceUuid` but not the full `deviceToken`.

- [ ] **Step 4: Run CLI and app tests**

Run:

```bash
go test ./internal/app ./cmd/wxpusher-bridge
```

Expected: all tests pass.

- [ ] **Step 5: Build binary**

Run:

```bash
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
```

Expected: `bin/wxpusher-bridge` exists.

- [ ] **Step 6: Commit**

Run:

```bash
git add cmd internal/app
git commit -m "feat: wire runtime and cli"
```

## Task 9: Add Deployment Files And Acceptance Documentation

**Files:**
- Create: `docs/systemd/wxpusher-bridge.service`
- Create: `docs/acceptance.md`
- Modify: `README.md`

- [ ] **Step 1: Add systemd service**

Create `docs/systemd/wxpusher-bridge.service`:

```ini
[Unit]
Description=WxPusher to WeCom bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=wxpusher-bridge
Group=wxpusher-bridge
EnvironmentFile=-/etc/wxpusher-bridge/env
ExecStart=/usr/local/bin/wxpusher-bridge run -config /etc/wxpusher-bridge/config.toml
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/wxpusher-bridge

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: Add acceptance checklist**

Create `docs/acceptance.md`:

```markdown
# Acceptance Checklist

1. Build the binary with `go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge`.
2. Create a local config from `configs/config.example.toml`.
3. Set `WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL` in the shell or systemd env file.
4. Import identity with `bin/wxpusher-bridge import-json -config config.local.toml -file identity.local.json` or `bin/wxpusher-bridge import-chrome -config config.local.toml -profile "<profile>" -extension-id "<id>"`.
5. Start the bridge with `bin/wxpusher-bridge run -config config.local.toml`.
6. Send a WxPusher test message without a link.
7. Confirm SQLite has one row in `messages` and a completed original delivery.
8. Confirm WeCom receives the original message.
9. Send a WxPusher test message containing `https://example.com`.
10. Confirm SQLite has an enrichment task and enrichment result.
11. Confirm the screenshot file exists under the configured screenshot directory.
12. Confirm WeCom receives the enriched follow-up message.
13. Stop network access and confirm reconnect events are recorded in `app_events`.
```

- [ ] **Step 3: Add README**

Create `README.md`:

```markdown
# WxPusher WeCom Bridge

Go service that receives WxPusher messages using the same protocol shape as the Chrome extension, persists messages locally, forwards originals to a WeCom robot, and enriches linked messages with Headless Chrome text extraction and screenshots.

## Safety Notes

- The bridge does not read or send Chrome cookies.
- The bridge only imports explicit identity fields from a specified Chrome profile or JSON file.
- The WxPusher protocol implementation keeps the extension's headers and WebSocket parameters: `platform`, `version`, `deviceToken`, and optional `pushToken`.

## Quick Start

```bash
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
cp configs/config.example.toml config.local.toml
export WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
bin/wxpusher-bridge import-json -config config.local.toml -file identity.local.json
bin/wxpusher-bridge run -config config.local.toml
```

See `docs/acceptance.md` for manual verification.
```

- [ ] **Step 4: Run full verification**

Run:

```bash
go test ./...
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
```

Expected: all tests pass and the binary builds.

- [ ] **Step 5: Commit**

Run:

```bash
git add README.md configs docs
git commit -m "docs: add deployment and acceptance guide"
```

## Task 10: Final Verification And Operator Notes

**Files:**
- Modify: `docs/acceptance.md`

- [ ] **Step 1: Run final automated checks**

Run:

```bash
go test ./...
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
git status --short
```

Expected:

- `go test ./...` passes.
- `go build` exits with code 0.
- `git status --short` shows only intentionally uncommitted files, or no output after final commit.

- [ ] **Step 2: Record local manual test commands**

Append this section to `docs/acceptance.md` after running the commands available in the current environment:

```markdown
## Local Verification Log

- `go test ./...`: PASS
- `go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge`: PASS
- Manual WxPusher message test: not run in this environment
- Manual WeCom delivery test: not run in this environment
- Manual Headless Chrome screenshot test: not run in this environment
```

If real webhook and identity credentials are available, replace the three manual lines with the actual result and timestamp.

- [ ] **Step 3: Commit final verification notes**

Run:

```bash
git add docs/acceptance.md
git commit -m "docs: record verification status"
```

- [ ] **Step 4: Summarize remaining manual work**

Tell the operator:

```text
Automated tests and build pass. The remaining verification requires live WxPusher identity data and a WeCom robot webhook: import identity, start the bridge, send one plain message, then send one message containing a URL.
```
