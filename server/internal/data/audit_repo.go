package data

import (
	"context"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/auditevent"
)

// AuditRepo stores the recorded writes and reads them back.
type AuditRepo struct {
	db *ent.Client
}

// NewAuditRepo builds the audit repository on the shared data.
func NewAuditRepo(d *Data) biz.AuditRepo {
	return &AuditRepo{db: d.Ent()}
}

// NewAuditRepoWithDB builds the audit repository on an explicit client.
func NewAuditRepoWithDB(db *ent.Client) *AuditRepo {
	return &AuditRepo{db: db}
}

// Record writes one event that is not part of a business transaction. It is
// used for failures, which have no committed change to share a transaction
// with. A successful change is written by commitAudited instead.
func (r *AuditRepo) Record(ctx context.Context, event biz.AuditEvent) error {
	return insertAuditEvent(ctx, r.db, event)
}

// List returns the recorded events, newest first. A query error is returned
// as an error: an unreadable list is never reported as an empty one.
func (r *AuditRepo) List(ctx context.Context, query biz.AuditQuery) ([]biz.AuditEvent, int, error) {
	page := query.Page
	if page <= 0 {
		page = 1
	}
	size := query.PageSize
	if size <= 0 || size > 200 {
		size = 20
	}
	filter := r.db.AuditEvent.Query()
	if query.ResourceType != "" {
		filter = filter.Where(auditevent.ResourceTypeEQ(query.ResourceType))
	}
	if query.ResourceID != "" {
		filter = filter.Where(auditevent.ResourceIDEQ(query.ResourceID))
	}
	if query.Action != "" {
		filter = filter.Where(auditevent.ActionEQ(query.Action))
	}
	if query.ActorID > 0 {
		filter = filter.Where(auditevent.ActorIDEQ(query.ActorID))
	}
	if query.ActorName != "" {
		filter = filter.Where(auditevent.ActorNameEQ(query.ActorName))
	}
	if query.TraceID != "" {
		filter = filter.Where(auditevent.TraceIDEQ(query.TraceID))
	}
	total, err := filter.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	// The order is total, not merely descending: two events of the same
	// instant are separated by their identity, so a page boundary cannot
	// repeat or skip a row.
	rows, err := filter.
		Order(ent.Desc(auditevent.FieldCreatedAt), ent.Desc(auditevent.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.AuditEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAuditEvent(row))
	}
	return out, total, nil
}

func toAuditEvent(row *ent.AuditEvent) biz.AuditEvent {
	return biz.AuditEvent{
		ID:              row.ID,
		TraceID:         row.TraceID,
		ActorID:         row.ActorID,
		ActorName:       row.ActorName,
		Action:          row.Action,
		ResourceType:    row.ResourceType,
		ResourceID:      row.ResourceID,
		ResourceVersion: int(row.ResourceVersion),
		Result:          row.Result,
		Reason:          row.Reason,
		CreatedAt:       row.CreatedAt,
	}
}
