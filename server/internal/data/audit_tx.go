package data

import (
	"context"
	"fmt"
	"strconv"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
)

// The audit row of a successful change is written in the transaction that
// performs the change. A repository that commits a write the console has to
// answer for calls commitAudited instead of Commit: the event either lands
// with the change or the change is refused.
//
// A failed operation is recorded by the use case after the transaction is
// gone (biz.AuditScope.End), because there is no row to commit it with.

// commitAudited commits one business transaction and the audit event declared
// by the use case. A change whose event cannot be written is rolled back: an
// unrecoverable operation is better than an untraceable one.
func commitAudited(ctx context.Context, tx *ent.Tx) error {
	intent := biz.AuditIntentFrom(ctx)
	if intent == nil || !intent.Pending() {
		// Not an audited operation, or an idempotent replay that changed
		// nothing.
		return tx.Commit()
	}
	event := biz.AuditEvent{
		TraceID:         biz.TraceID(ctx),
		Action:          intent.Action,
		ResourceType:    intent.ResourceType,
		ResourceID:      intent.ResourceID,
		ResourceVersion: intent.ResourceVersion,
		Result:          biz.AuditResultSuccess,
		// An explicit action may carry the reason the operator stated.
		Reason: intent.Note,
	}
	if actor, ok := biz.AuditActorFrom(ctx); ok {
		event.ActorID = actor.ID
		event.ActorName = actor.Username
	}
	if err := insertAuditEvent(ctx, tx.Client(), event); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("%w: %v", biz.ErrAuditWriteFailed, err)
	}
	intent.MarkCommitted()
	return tx.Commit()
}

// auditResource names the row the pending event belongs to. The repository
// knows the identity and the version of what it wrote, so it fills them in
// before the commit.
func auditResource(ctx context.Context, id int64, version int) {
	intent := biz.AuditIntentFrom(ctx)
	if intent == nil {
		return
	}
	intent.SetResource(strconv.FormatInt(id, 10), version)
}

// insertAuditEvent writes one row. It is shared by the business transaction
// and by the independent failure record, so both keep the same shape.
func insertAuditEvent(ctx context.Context, client *ent.Client, event biz.AuditEvent) error {
	if event.Action == "" || event.ResourceType == "" {
		return fmt.Errorf("audit event needs an action and a resource type")
	}
	create := client.AuditEvent.Create().
		SetTraceID(event.TraceID).
		SetActorID(event.ActorID).
		SetActorName(event.ActorName).
		SetAction(event.Action).
		SetResourceType(event.ResourceType).
		SetResourceID(event.ResourceID).
		SetResourceVersion(int32(event.ResourceVersion)).
		SetResult(event.Result).
		SetReason(event.Reason)
	if !event.CreatedAt.IsZero() {
		create = create.SetCreatedAt(event.CreatedAt)
	}
	_, err := create.Save(ctx)
	return err
}
