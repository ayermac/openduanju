package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IngestionRunBudget is an audit record for a manual budget extension.
type IngestionRunBudget struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (IngestionRunBudget) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ingestion_run_budget"}}
}

// Fields of the audit record.
func (IngestionRunBudget) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("run_id"),
		field.Int32("additional_requests").Default(0),
		field.Int32("additional_items").Default(0),
		field.String("reason").MaxLen(255).Default(""),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the audit record.
func (IngestionRunBudget) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id")}
}
