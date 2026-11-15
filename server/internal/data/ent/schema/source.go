package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Source is the content owner an operator can configure and pause.
type Source struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (Source) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "source"}}
}

// Fields of the source.
func (Source) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("code").MaxLen(32),
		field.String("name").MaxLen(80),
		// status is enabled or paused; pausing stops new collection only.
		field.String("status").MaxLen(16).Default("enabled"),
		field.Int32("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the source.
func (Source) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique()}
}
