package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ReviewTask is the single review task a ready batch creates for a revision.
type ReviewTask struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (ReviewTask) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "review_task"}}
}

// Fields of the review task.
func (ReviewTask) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("revision_id"),
		field.Int64("series_id"),
		field.Int64("batch_id"),
		field.String("status").MaxLen(16).Default("open"),
		field.String("assignee").MaxLen(64).Default(""),
		field.String("submitted_by").MaxLen(64).Default(""),
		field.Int("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the review task.
func (ReviewTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("revision_id").Unique(),
		index.Fields("status"),
	}
}
