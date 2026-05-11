package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	st := &SQLiteStore{db: db}
	if err := st.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS identity (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			device_uuid TEXT NOT NULL,
			device_token TEXT NOT NULL,
			push_token TEXT NOT NULL,
			platform TEXT NOT NULL,
			version TEXT NOT NULL,
			source TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY,
			qid TEXT NOT NULL,
			dedupe_key TEXT UNIQUE NOT NULL,
			msg_type INTEGER NOT NULL,
			content TEXT NOT NULL,
			raw_payload BLOB NOT NULL,
			received_at TEXT NOT NULL,
			processing_status TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS delivery_attempts (
			id INTEGER PRIMARY KEY,
			message_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			payload TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL,
			next_attempt_at TEXT NOT NULL,
			last_error TEXT NOT NULL,
			response_body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS enrichment_tasks (
			id INTEGER PRIMARY KEY,
			message_id INTEGER NOT NULL,
			url TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL,
			next_attempt_at TEXT NOT NULL,
			last_error TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(message_id, url)
		)`,
		`CREATE TABLE IF NOT EXISTS enrichments (
			id INTEGER PRIMARY KEY,
			message_id INTEGER NOT NULL,
			url TEXT NOT NULL,
			title TEXT NOT NULL,
			summary TEXT NOT NULL,
			screenshot_path TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(message_id, url)
		)`,
		`CREATE TABLE IF NOT EXISTS app_events (
			id INTEGER PRIMARY KEY,
			kind TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) SaveIdentity(ctx context.Context, id identity.Identity) error {
	if id.UpdatedAt.IsZero() {
		id.UpdatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO identity
		(id, device_uuid, device_token, push_token, platform, version, source, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			device_uuid = excluded.device_uuid,
			device_token = excluded.device_token,
			push_token = excluded.push_token,
			platform = excluded.platform,
			version = excluded.version,
			source = excluded.source,
			updated_at = excluded.updated_at`,
		id.DeviceUUID, id.DeviceToken, id.PushToken, id.Platform, id.Version, id.Source, encodeTime(id.UpdatedAt))
	return err
}

func (s *SQLiteStore) LoadIdentity(ctx context.Context) (identity.Identity, bool, error) {
	var id identity.Identity
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT device_uuid, device_token, push_token, platform, version, source, updated_at FROM identity WHERE id = 1`).
		Scan(&id.DeviceUUID, &id.DeviceToken, &id.PushToken, &id.Platform, &id.Version, &id.Source, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Identity{}, false, nil
	}
	if err != nil {
		return identity.Identity{}, false, err
	}
	id.UpdatedAt, err = decodeTime(updatedAt)
	if err != nil {
		return identity.Identity{}, false, err
	}
	return id, true, nil
}

func (s *SQLiteStore) SaveMessage(ctx context.Context, msg Message) (SaveMessageResult, error) {
	dedupeKey := msg.DedupeKey
	if dedupeKey == "" {
		dedupeKey = messageDedupeKey(msg)
	}
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now().UTC()
	}
	rawPayload := msg.RawPayload
	if rawPayload == nil {
		rawPayload = []byte{}
	}

	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO messages
		(qid, dedupe_key, msg_type, content, raw_payload, received_at, processing_status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.QID, dedupeKey, msg.MsgType, msg.Content, rawPayload, encodeTime(msg.ReceivedAt), "pending")
	if err != nil {
		return SaveMessageResult{}, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return SaveMessageResult{}, err
		}
		return SaveMessageResult{ID: id, Inserted: true}, nil
	}

	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM messages WHERE dedupe_key = ?`, dedupeKey).Scan(&id); err != nil {
		return SaveMessageResult{}, err
	}
	return SaveMessageResult{ID: id, Inserted: false}, nil
}

func (s *SQLiteStore) EnqueueDelivery(ctx context.Context, task DeliveryTask) (int64, error) {
	now := time.Now().UTC()
	if task.NextAttempt.IsZero() {
		task.NextAttempt = now
	}
	if task.Status == "" {
		task.Status = TaskStatusPending
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO delivery_attempts
		(message_id, kind, payload, status, attempts, next_attempt_at, last_error, response_body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.MessageID, task.Kind, task.Payload, task.Status, task.Attempts, encodeTime(task.NextAttempt), task.LastError, "", encodeTime(now), encodeTime(now))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) ClaimDeliveryTasks(ctx context.Context, limit int, now time.Time) ([]DeliveryTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	tasks, err := selectDeliveryTasks(ctx, tx, limit, now)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for i := range tasks {
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET status = ?, updated_at = ? WHERE id = ?`, TaskStatusRunning, encodeTime(time.Now().UTC()), tasks[i].ID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		tasks[i].Status = TaskStatusRunning
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *SQLiteStore) MarkDeliveryDone(ctx context.Context, id int64, responseBody string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delivery_attempts SET status = ?, response_body = ?, updated_at = ? WHERE id = ?`, TaskStatusDone, responseBody, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) MarkDeliveryRetry(ctx context.Context, id int64, attempts int, nextAttempt time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delivery_attempts SET status = ?, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		TaskStatusPending, attempts, encodeTime(nextAttempt), lastError, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) MarkDeliveryFailed(ctx context.Context, id int64, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delivery_attempts SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`, TaskStatusFailed, lastError, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) EnqueueEnrichment(ctx context.Context, task EnrichmentTask) (int64, error) {
	now := time.Now().UTC()
	if task.NextAttempt.IsZero() {
		task.NextAttempt = now
	}
	if task.Status == "" {
		task.Status = TaskStatusPending
	}
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO enrichment_tasks
		(message_id, url, status, attempts, next_attempt_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		task.MessageID, task.URL, task.Status, task.Attempts, encodeTime(task.NextAttempt), task.LastError, encodeTime(now), encodeTime(now))
	if err != nil {
		return 0, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 1 {
		return res.LastInsertId()
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM enrichment_tasks WHERE message_id = ? AND url = ?`, task.MessageID, task.URL).Scan(&id)
	return id, err
}

func (s *SQLiteStore) ClaimEnrichmentTasks(ctx context.Context, limit int, now time.Time) ([]EnrichmentTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	tasks, err := selectEnrichmentTasks(ctx, tx, limit, now)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for i := range tasks {
		if _, err := tx.ExecContext(ctx, `UPDATE enrichment_tasks SET status = ?, updated_at = ? WHERE id = ?`, TaskStatusRunning, encodeTime(time.Now().UTC()), tasks[i].ID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		tasks[i].Status = TaskStatusRunning
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *SQLiteStore) SaveEnrichment(ctx context.Context, enrichment Enrichment) error {
	if enrichment.CreatedAt.IsZero() {
		enrichment.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO enrichments
		(message_id, url, title, summary, screenshot_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, url) DO UPDATE SET
			title = excluded.title,
			summary = excluded.summary,
			screenshot_path = excluded.screenshot_path,
			created_at = excluded.created_at`,
		enrichment.MessageID, enrichment.URL, enrichment.Title, enrichment.Summary, enrichment.Screenshot, encodeTime(enrichment.CreatedAt))
	return err
}

func (s *SQLiteStore) MarkEnrichmentDone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE enrichment_tasks SET status = ?, updated_at = ? WHERE id = ?`, TaskStatusDone, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) MarkEnrichmentRetry(ctx context.Context, id int64, attempts int, nextAttempt time.Time, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE enrichment_tasks SET status = ?, attempts = ?, next_attempt_at = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		TaskStatusPending, attempts, encodeTime(nextAttempt), lastError, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) MarkEnrichmentFailed(ctx context.Context, id int64, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE enrichment_tasks SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`, TaskStatusFailed, lastError, encodeTime(time.Now().UTC()), id)
	return err
}

func (s *SQLiteStore) AddAppEvent(ctx context.Context, event AppEvent) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_events (kind, message, created_at) VALUES (?, ?, ?)`, event.Kind, event.Message, encodeTime(event.CreatedAt))
	return err
}

func selectDeliveryTasks(ctx context.Context, tx *sql.Tx, limit int, now time.Time) ([]DeliveryTask, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, message_id, kind, payload, status, attempts, next_attempt_at, last_error
		FROM delivery_attempts
		WHERE status = ? AND next_attempt_at <= ?
		ORDER BY id
		LIMIT ?`, TaskStatusPending, encodeTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []DeliveryTask
	for rows.Next() {
		var task DeliveryTask
		var nextAttempt string
		if err := rows.Scan(&task.ID, &task.MessageID, &task.Kind, &task.Payload, &task.Status, &task.Attempts, &nextAttempt, &task.LastError); err != nil {
			return nil, err
		}
		var err error
		task.NextAttempt, err = decodeTime(nextAttempt)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func selectEnrichmentTasks(ctx context.Context, tx *sql.Tx, limit int, now time.Time) ([]EnrichmentTask, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, message_id, url, status, attempts, next_attempt_at, last_error
		FROM enrichment_tasks
		WHERE status = ? AND next_attempt_at <= ?
		ORDER BY id
		LIMIT ?`, TaskStatusPending, encodeTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []EnrichmentTask
	for rows.Next() {
		var task EnrichmentTask
		var nextAttempt string
		if err := rows.Scan(&task.ID, &task.MessageID, &task.URL, &task.Status, &task.Attempts, &nextAttempt, &task.LastError); err != nil {
			return nil, err
		}
		var err error
		task.NextAttempt, err = decodeTime(nextAttempt)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func messageDedupeKey(msg Message) string {
	if msg.QID != "" {
		return "qid:" + msg.QID
	}
	h := sha256.New()
	h.Write([]byte(strconv.Itoa(msg.MsgType)))
	h.Write([]byte{0})
	h.Write([]byte(msg.Content))
	h.Write([]byte{0})
	h.Write(msg.RawPayload)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func encodeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func decodeTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return t, nil
}
