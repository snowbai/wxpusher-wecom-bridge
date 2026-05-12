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
	DeliveryOriginal   DeliveryKind = "original"
	DeliveryEnriched   DeliveryKind = "enriched"
	DeliveryScreenshot DeliveryKind = "screenshot"
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
	MessageID   int64
	URL         string
	Title       string
	Summary     string
	Screenshot  string
	Status      string
	CreatedAt   time.Time
	CompletedAt time.Time
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
