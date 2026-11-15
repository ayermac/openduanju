package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IngestionPlan is one reviewed collection configuration.
type IngestionPlan struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (IngestionPlan) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ingestion_plan"}}
}

// Fields of the plan.
func (IngestionPlan) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("code").MaxLen(64),
		field.String("name").MaxLen(128),
		field.String("source_code").MaxLen(32),
		field.String("channel").MaxLen(64),
		field.Int32("page_start"),
		field.Int32("page_end"),
		field.Int32("request_budget").Default(80),
		field.Int32("item_budget").Default(200),
		field.Int32("rate_per_minute").Default(30),
		field.Bool("fetch_details").Default(true),
		field.Bool("fetch_episodes").Default(true),
		field.Bool("enabled").Default(true),
		field.Int32("version").Default(1),
		// cron is a 5-field cron expression; empty keeps the plan manual.
		field.String("cron").MaxLen(64).Default(""),
		// timezone is the IANA zone used to evaluate the cron expression.
		field.String("timezone").MaxLen(64).Default("Asia/Shanghai"),
		// next_run_at is the next scheduled fire time in UTC.
		field.Time("next_run_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the plan.
func (IngestionPlan) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique()}
}
