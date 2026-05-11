package store

import (
	"context"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
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
	got, ok, err := st.LoadIdentity(ctx)
	if err != nil {
		t.Fatalf("LoadIdentity() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadIdentity() ok = false")
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
	if claimed[0].ID != taskID || claimed[0].Status != TaskStatusRunning || claimed[0].Kind != DeliveryOriginal {
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
	if claimed[0].ID != taskID || claimed[0].Status != TaskStatusRunning {
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

func TestSQLiteAppEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.AddAppEvent(ctx, AppEvent{Kind: "startup", Message: "started"}); err != nil {
		t.Fatalf("AddAppEvent() error = %v", err)
	}
}
