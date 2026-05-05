// Package audit writes immutable audit events to toris_control.audit_events.
//
// Audit events are append-only. No update or delete is ever issued against
// this table. The writer is non-blocking: events are queued in a buffered
// channel and flushed by a background goroutine so the hot path is never
// blocked by a slow control DB write.
package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tobibamidele/toris/internal/logging"
	"github.com/tobibamidele/toris/internal/util"
	"github.com/tobibamidele/toris/pkg/model"
)

const defaultQueueDepth = 512

// Writer queues and persists audit events.
type Writer struct {
	log   *logging.Logger
	pool  *pgxpool.Pool
	queue chan model.AuditEvent
	done  chan struct{}
}

// New creates a Writer. Call Run in a goroutine to start draining the queue.
func New(log *logging.Logger, pool *pgxpool.Pool) *Writer {
	return &Writer{
		log:   log,
		pool:  pool,
		queue: make(chan model.AuditEvent, defaultQueueDepth),
		done:  make(chan struct{}),
	}
}

// EnsureSchema creates the audit_events table if it does not exist. Idempotent.
func (w *Writer) EnsureSchema(ctx context.Context) error {
	_, err := w.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS toris_control.audit_events (
			id          TEXT        NOT NULL,
			cluster_id  TEXT        NOT NULL,
			kind        TEXT        NOT NULL,
			actor_id    TEXT        NOT NULL,
			subject_id  TEXT        NOT NULL,
			generation  BIGINT      NOT NULL DEFAULT 0,
			message     TEXT        NOT NULL DEFAULT '',
			metadata    JSONB,
			occurred_at TIMESTAMPTZ NOT NULL,

			CONSTRAINT audit_events_pkey PRIMARY KEY (id)
		);

		CREATE INDEX IF NOT EXISTS audit_events_cluster_idx
			ON toris_control.audit_events (cluster_id, occurred_at DESC);

		CREATE INDEX IF NOT EXISTS audit_events_kind_idx
			ON toris_control.audit_events (kind, occurred_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("ensuring audit schema: %w", err)
	}
	return nil
}

// Emit enqueues an audit event. Non-blocking: if the queue is full the event
// is dropped with a warning log. The queue depth is generous (512) so drops
// should only occur under extreme load where the control DB is struggling.
func (w *Writer) Emit(e model.AuditEvent) {
	if e.ID == "" {
		e.ID = util.NewID()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = util.NowUTC()
	}
	select {
	case w.queue <- e:
	default:
		w.log.Warn("audit queue full — dropping event",
			"kind", string(e.Kind),
			"subject_id", e.SubjectID,
		)
	}
}

// EmitNow is a convenience wrapper that builds and emits an AuditEvent
// from its constituent parts.
func (w *Writer) EmitNow(clusterID string, kind model.AuditEventKind, actorID, subjectID string, generation int64, msg string) {
	w.Emit(model.AuditEvent{
		ID:         util.NewID(),
		ClusterID:  clusterID,
		Kind:       kind,
		ActorID:    actorID,
		SubjectID:  subjectID,
		Generation: generation,
		Message:    msg,
		OccurredAt: util.NowUTC(),
	})
}

// Run drains the audit queue and writes events to the control DB.
// It returns when ctx is canceled. Call in a dedicated goroutine.
func (w *Writer) Run(ctx context.Context) error {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			// Drain remaining events before returning.
			w.drainRemaining()
			return ctx.Err()
		case evt := <-w.queue:
			w.persist(ctx, evt)
		}
	}
}

func (w *Writer) persist(ctx context.Context, e model.AuditEvent) {
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := w.pool.Exec(writeCtx, `
		INSERT INTO toris_control.audit_events
			(id, cluster_id, kind, actor_id, subject_id,
			 generation, message, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO NOTHING
	`,
		e.ID, e.ClusterID, string(e.Kind), e.ActorID, e.SubjectID,
		e.Generation, e.Message, e.OccurredAt,
	)
	if err != nil {
		w.log.Error("failed to persist audit event",
			"kind", string(e.Kind),
			"subject_id", e.SubjectID,
			"error", err.Error(),
		)
	}
}

// drainRemaining flushes any events still in the queue using a background
// context so shutdown does not lose events queued moments before ctx cancel.
func (w *Writer) drainRemaining() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for {
		select {
		case evt := <-w.queue:
			w.persist(ctx, evt)
		default:
			return
		}
	}
}

// Wait blocks until the writer has finished draining. Call after ctx cancel.
func (w *Writer) Wait() {
	<-w.done
}

// Recent returns the most recent N audit events for the cluster in descending
// order. Used by toris leader status and toris doctor.
func (w *Writer) Recent(ctx context.Context, clusterID string, limit int) ([]model.AuditEvent, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT id, cluster_id, kind, actor_id, subject_id,
		       generation, message, occurred_at
		FROM toris_control.audit_events
		WHERE cluster_id = $1
		ORDER BY occurred_at DESC
		LIMIT $2
	`, clusterID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying audit events: %w", err)
	}
	defer rows.Close()

	var events []model.AuditEvent
	for rows.Next() {
		var e model.AuditEvent
		var kind string
		if err := rows.Scan(
			&e.ID, &e.ClusterID, &kind, &e.ActorID, &e.SubjectID,
			&e.Generation, &e.Message, &e.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scanning audit event: %w", err)
		}
		e.Kind = model.AuditEventKind(kind)
		events = append(events, e)
	}
	return events, rows.Err()
}
