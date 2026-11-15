package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IngestionRun is a persistent collection job with a checkpoint and lease.
type IngestionRun struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (IngestionRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ingestion_run"}}
}

// Fields of the run.
func (IngestionRun) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("plan_id").Default(0),
		field.JSON("plan_snapshot", map[string]any{}).Optional(),
		field.String("kind").MaxLen(16).Default("ranking"),
		field.Int64("candidate_id").Optional().Nillable(),
		field.String("source_code").MaxLen(32),
		field.String("channel").MaxLen(64),
		field.Int32("page_start"),
		field.Int32("page_end"),
		field.String("status").MaxLen(16).Default("queued"),
		field.Int32("request_budget").Default(0),
		field.Int32("item_budget").Default(0),
		field.Int32("requests_used").Default(0),
		field.Int32("rate_per_minute").Default(30),
		field.Time("rate_window_start").Optional().Nillable(),
		field.Int32("rate_window_count").Default(0),
		field.Int32("page_cursor").Default(0),
		field.Int32("pages_fetched").Default(0),
		field.Int32("items_seen").Default(0),
		field.Int32("items_upserted").Default(0),
		field.Int32("detail_fetched").Default(0),
		field.Int32("episodes_fetched").Default(0),
		field.Bool("cancel_requested").Default(false),
		field.String("lease_owner").MaxLen(64).Default(""),
		field.Time("lease_until").Optional().Nillable(),
		field.Time("next_request_at").Optional().Nillable(),
		field.String("error_code").MaxLen(64).Default(""),
		field.String("error_message").MaxLen(1000).Default(""),
		// trace_id is the identity of the trigger. The run outlives the
		// request that queued it - and may be resumed by a worker that never
		// saw the request - so the identity is stored, not just logged.
		field.String("trace_id").MaxLen(64).Default(""),
		field.Int32("version").Default(1),
		field.Time("started_at").Default(timeNow),
		field.Time("finished_at").Optional().Nillable(),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the run.
func (IngestionRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_code", "started_at"),
		index.Fields("status"),
	}
}
