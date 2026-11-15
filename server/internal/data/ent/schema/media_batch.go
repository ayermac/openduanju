package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MediaBatch is one persistent processing run over a frozen revision.
type MediaBatch struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (MediaBatch) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "media_batch"}}
}

// Fields of the batch.
func (MediaBatch) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("series_id"),
		field.Int64("revision_id"),
		field.String("selection_hash").MaxLen(64).Default(""),
		field.String("profile_version").MaxLen(64).Default(""),
		field.String("input_kind").MaxLen(16).Default("sdk"),
		field.String("status").MaxLen(16).Default("queued"),
		field.String("cover_status").MaxLen(16).Default("pending"),
		field.Int("item_total").Default(0),
		field.Int("item_ready").Default(0),
		field.Int("item_failed").Default(0),
		field.Bool("cancel_requested").Default(false),
		field.String("lease_owner").MaxLen(64).Default(""),
		field.Time("lease_until").Optional().Nillable(),
		field.Int("version").Default(1),
		field.String("error_code").MaxLen(64).Default(""),
		field.String("error_message").MaxLen(512).Default(""),
		field.String("created_by").MaxLen(64).Default(""),
		// trace_id is the identity of the submission that created the batch.
		// The batch is processed by a worker, possibly after a restart, so
		// the identity travels with the row instead of the request.
		field.String("trace_id").MaxLen(64).Default(""),
		field.Int64("review_task_id").Default(0),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the batch.
func (MediaBatch) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("revision_id").Unique(),
		index.Fields("series_id"),
		index.Fields("status"),
	}
}
