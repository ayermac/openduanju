package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IngestionRunPage records one page attempt of a run.
type IngestionRunPage struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (IngestionRunPage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ingestion_run_page"}}
}

// Fields of the page attempt.
func (IngestionRunPage) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("run_id"),
		field.Int32("page_no"),
		field.String("status").MaxLen(16).Default("pending"),
		field.Int32("items_seen").Default(0),
		field.String("error_code").MaxLen(64).Default(""),
		field.Time("fetched_at").Optional().Nillable(),
	}
}

// Indexes of the page attempt.
func (IngestionRunPage) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "page_no").Unique()}
}
