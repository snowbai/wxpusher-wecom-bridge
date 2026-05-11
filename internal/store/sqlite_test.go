package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()

	st, err := OpenSQLite(t.TempDir() + "/store.db")
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return st
}

func TestSQLiteIdentityRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	want := identity.Identity{
		DeviceUUID:  "device-uuid",
		DeviceToken: "device-token",
		PushToken:   "push-token",
		Platform:    "Chrome-macOS",
		Version:     "1.2.3",
		Source:      "json",
		UpdatedAt:   time.Date(2026, 5, 11, 10, 30, 0, 0, time.UTC),
	}

	if err := st.SaveIdentity(ctx, want); err != nil {
		t.Fatalf("SaveIdentity() error = %v", err)
	}
	got, err := st.LoadIdentity(ctx)
	if err != nil {
		t.Fatalf("LoadIdentity() error = %v", err)
	}
	if got != want {
		t.Fatalf("LoadIdentity() = %+v, want %+v", got, want)
	}
}

func TestSQLiteMessageDedupesByQID(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	msg := Message{QID: "qid-1", MsgType: 1, Content: "hello", RawPayload: []byte(`{"qid":"qid-1"}`)}

	first, err := st.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("first SaveMessage() error = %v", err)
	}
	second, err := st.SaveMessage(ctx, Message{QID: "qid-1", MsgType: 1, Content: "changed"})
	if err != nil {
		t.Fatalf("second SaveMessage() error = %v", err)
	}

	if !first.Inserted {
		t.Fatal("first SaveMessage() Inserted = false")
	}
	if second.Inserted {
		t.Fatal("second SaveMessage() Inserted = true")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate ID = %d, want %d", second.ID, first.ID)
	}
}

func TestSQLiteMessageIgnoresCallerSuppliedDedupeKey(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	first, err := st.SaveMessage(ctx, Message{QID: "q1", DedupeKey: "bad", MsgType: 1, Content: "hello"})
	if err != nil {
		t.Fatalf("first SaveMessage() error = %v", err)
	}
	second, err := st.SaveMessage(ctx, Message{QID: "q1", MsgType: 1, Content: "changed"})
	if err != nil {
		t.Fatalf("second SaveMessage() error = %v", err)
	}

	if !first.Inserted {
		t.Fatal("first SaveMessage() Inserted = false")
	}
	if second.Inserted {
		t.Fatal("second SaveMessage() Inserted = true")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate ID = %d, want %d", second.ID, first.ID)
	}
	var dedupeKey string
	if err := st.db.QueryRowContext(ctx, `SELECT dedupe_key FROM messages WHERE id = ?`, first.ID).Scan(&dedupeKey); err != nil {
		t.Fatalf("query dedupe_key error = %v", err)
	}
	if dedupeKey != "qid:q1" {
		t.Fatalf("dedupe_key = %q, want qid:q1", dedupeKey)
	}
}

func TestSQLiteMessageDedupesWithoutQID(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	msg := Message{MsgType: 2, Content: "same", RawPayload: []byte(`{"body":"same"}`)}

	first, err := st.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("first SaveMessage() error = %v", err)
	}
	second, err := st.SaveMessage(ctx, msg)
	if err != nil {
		t.Fatalf("second SaveMessage() error = %v", err)
	}

	if !first.Inserted {
		t.Fatal("first SaveMessage() Inserted = false")
	}
	if second.Inserted {
		t.Fatal("second SaveMessage() Inserted = true")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate ID = %d, want %d", second.ID, first.ID)
	}
}

func TestSQLiteTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 5, 11, 11, 0, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "task-qid", MsgType: 1, Content: "body"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	taskID, err := st.EnqueueDelivery(ctx, DeliveryTask{MessageID: msg.ID, Kind: DeliveryOriginal, Payload: "payload"})
	if err != nil {
		t.Fatalf("EnqueueDelivery() error = %v", err)
	}
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}
	if claimed[0].ID != taskID || claimed[0].Status != TaskRunning || claimed[0].Kind != DeliveryOriginal {
		t.Fatalf("claimed task = %+v, want id %d running original", claimed[0], taskID)
	}
	if err := st.MarkDeliveryDone(ctx, taskID, "ok"); err != nil {
		t.Fatalf("MarkDeliveryDone() error = %v", err)
	}
	claimed, err = st.ClaimDeliveryTasks(ctx, 10, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks after done, want 0", len(claimed))
	}
}

func TestSQLiteEnqueueDeliveryForcesPendingStatus(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 5, 11, 11, 30, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "forced-pending-qid", MsgType: 1, Content: "body"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	taskID, err := st.EnqueueDelivery(ctx, DeliveryTask{MessageID: msg.ID, Kind: DeliveryOriginal, Payload: "payload", Status: TaskFailed})
	if err != nil {
		t.Fatalf("EnqueueDelivery() error = %v", err)
	}
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}
	if claimed[0].ID != taskID || claimed[0].Status != TaskRunning {
		t.Fatalf("claimed task = %+v, want id %d running", claimed[0], taskID)
	}
}

func TestSQLiteEnqueueDeliveryDedupesTask(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 5, 11, 11, 45, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "delivery-dedupe-qid", MsgType: 1, Content: "body"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}
	task := DeliveryTask{MessageID: msg.ID, Kind: DeliveryOriginal, Payload: "payload"}

	firstID, err := st.EnqueueDelivery(ctx, task)
	if err != nil {
		t.Fatalf("first EnqueueDelivery() error = %v", err)
	}
	secondID, err := st.EnqueueDelivery(ctx, task)
	if err != nil {
		t.Fatalf("second EnqueueDelivery() error = %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate task ID = %d, want %d", secondID, firstID)
	}
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}
}

func TestSQLiteEnqueueDeliveryRejectsMissingMessage(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if _, err := st.EnqueueDelivery(ctx, DeliveryTask{MessageID: 999, Kind: DeliveryOriginal, Payload: "payload"}); err == nil {
		t.Fatal("EnqueueDelivery() error = nil, want foreign key error")
	}
}

func TestSQLiteClaimDeliveryTasksDoesNotReturnAlreadyRunningTask(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 5, 11, 11, 50, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "delivery-claim-once-qid", MsgType: 1, Content: "body"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}
	if _, err := st.EnqueueDelivery(ctx, DeliveryTask{MessageID: msg.ID, Kind: DeliveryOriginal, Payload: "payload"}); err != nil {
		t.Fatalf("EnqueueDelivery() error = %v", err)
	}

	first, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("first ClaimDeliveryTasks() error = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first claim returned %d tasks, want 1", len(first))
	}
	second, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("second ClaimDeliveryTasks() error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim returned %d tasks, want 0", len(second))
	}
}

func TestSQLiteEnrichmentLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "enrich-qid", MsgType: 1, Content: "https://example.com"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	taskID, err := st.EnqueueEnrichment(ctx, EnrichmentTask{MessageID: msg.ID, URL: "https://example.com"})
	if err != nil {
		t.Fatalf("EnqueueEnrichment() error = %v", err)
	}
	claimed, err := st.ClaimEnrichmentTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}
	if claimed[0].ID != taskID || claimed[0].Status != TaskRunning {
		t.Fatalf("claimed task = %+v, want id %d running", claimed[0], taskID)
	}
	if err := st.SaveEnrichment(ctx, Enrichment{MessageID: msg.ID, URL: "https://example.com", Title: "Title", Summary: "Summary", Screenshot: "/tmp/shot.png"}); err != nil {
		t.Fatalf("SaveEnrichment() error = %v", err)
	}
	if err := st.MarkEnrichmentDone(ctx, taskID); err != nil {
		t.Fatalf("MarkEnrichmentDone() error = %v", err)
	}
	claimed, err = st.ClaimEnrichmentTasks(ctx, 10, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimEnrichmentTasks() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks after done, want 0", len(claimed))
	}
}

func TestSQLiteEnqueueEnrichmentRejectsMissingMessage(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if _, err := st.EnqueueEnrichment(ctx, EnrichmentTask{MessageID: 999, URL: "https://example.com"}); err == nil {
		t.Fatal("EnqueueEnrichment() error = nil, want foreign key error")
	}
}

func TestSQLiteSaveEnrichmentPersistsStatusAndCompletedAt(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	completedAt := time.Date(2026, 5, 11, 12, 30, 0, 0, time.UTC)
	msg, err := st.SaveMessage(ctx, Message{QID: "enrichment-result-qid", MsgType: 1, Content: "https://example.com"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	err = st.SaveEnrichment(ctx, Enrichment{
		MessageID:   msg.ID,
		URL:         "https://example.com",
		Title:       "Title",
		Summary:     "Summary",
		Screenshot:  "/tmp/shot.png",
		Status:      "fetched",
		CompletedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("SaveEnrichment() error = %v", err)
	}

	var status string
	var completedAtValue string
	if err := st.db.QueryRowContext(ctx, `SELECT content_status, completed_at FROM enrichments WHERE message_id = ? AND url = ?`, msg.ID, "https://example.com").Scan(&status, &completedAtValue); err != nil {
		t.Fatalf("query enrichment result error = %v", err)
	}
	gotCompletedAt, err := decodeTime(completedAtValue)
	if err != nil {
		t.Fatalf("decode completed_at error = %v", err)
	}
	if status != "fetched" {
		t.Fatalf("content_status = %q, want fetched", status)
	}
	if !gotCompletedAt.Equal(completedAt) {
		t.Fatalf("completed_at = %s, want %s", gotCompletedAt, completedAt)
	}
}

func TestSQLiteSaveEnrichmentDefaultsCompletedAtToParseableTime(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	msg, err := st.SaveMessage(ctx, Message{QID: "enrichment-default-completed-qid", MsgType: 1, Content: "https://example.com"})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}

	if err := st.SaveEnrichment(ctx, Enrichment{MessageID: msg.ID, URL: "https://example.com"}); err != nil {
		t.Fatalf("SaveEnrichment() error = %v", err)
	}
	var completedAtValue string
	if err := st.db.QueryRowContext(ctx, `SELECT completed_at FROM enrichments WHERE message_id = ? AND url = ?`, msg.ID, "https://example.com").Scan(&completedAtValue); err != nil {
		t.Fatalf("query completed_at error = %v", err)
	}
	if _, err := decodeTime(completedAtValue); err != nil {
		t.Fatalf("completed_at is not parseable: %v", err)
	}
}

func TestSQLiteCompletedAtMigrationDoesNotUseInvalidEmptyDefault(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/store.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE enrichments (
		id INTEGER PRIMARY KEY,
		message_id INTEGER NOT NULL,
		url TEXT NOT NULL,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		screenshot_path TEXT NOT NULL,
		content_status TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(message_id, url)
	)`)
	if err != nil {
		t.Fatalf("create old enrichments table error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}

	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	rows, err := st.db.QueryContext(ctx, `PRAGMA table_info(enrichments)`)
	if err != nil {
		t.Fatalf("table_info error = %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info error = %v", err)
		}
		if name == "completed_at" && defaultValue.Valid && defaultValue.String == "''" {
			t.Fatal("completed_at default is invalid empty string")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows error = %v", err)
	}
}

func TestSQLiteAppEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.AddAppEvent(ctx, AppEvent{Kind: "startup", Message: "started"}); err != nil {
		t.Fatalf("AddAppEvent() error = %v", err)
	}
}
