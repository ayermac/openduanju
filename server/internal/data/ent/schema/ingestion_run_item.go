package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IngestionRunItem is one row one collection run produced. The relation is
// stored with the result of that attempt, so a later run cannot erase it.
type IngestionRunItem struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (IngestionRunItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ingestion_run_item"}}
}

// Fields of the run item.
func (IngestionRunItem) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("run_id"),
		field.Int64("candidate_id"),
		field.String("candidate_external_id").MaxLen(64).Default(""),
		// stage is the step of the run: summary, detail or episodes.
		field.String("stage").MaxLen(32).Default(""),
		// result is success or failed.
		field.String("result").MaxLen(16).Default("success"),
		field.String("error_code").MaxLen(64).Default(""),
		field.String("error_message").MaxLen(512).Default(""),
		field.Int("attempt").Default(1),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the run item.
func (IngestionRunItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "id"),
		index.Fields("candidate_id"),
	}
}
