package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ContentSeries is the local series with a draft revision pointer.
type ContentSeries struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (ContentSeries) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "content_series"}}
}

// Fields of the series.
func (ContentSeries) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("source_code").MaxLen(32),
		// A manual work has no provider identity, so the identifier is absent
		// rather than invented. The unique index keeps treating a real
		// identifier as unique and lets any number of manual rows exist.
		field.String("external_id").MaxLen(64).Optional().Nillable(),
		// request_id is the idempotency key of the creation form; the
		// fingerprint records what the key was used for.
		field.String("request_id").MaxLen(64).Optional().Nillable(),
		field.String("request_fingerprint").MaxLen(64).Default(""),
		field.Int64("candidate_id").Optional().Nillable(),
		field.String("title").MaxLen(255),
		field.Text("description").Default(""),
		field.String("cover_url").MaxLen(512).Default(""),
		// category is a public label used by the web list filter.
		field.String("category").MaxLen(64).Default(""),
		field.String("status").MaxLen(16).Default("draft"),
		field.Int32("revision").Default(1),
		field.Int64("current_draft_revision_id").Optional().Nillable(),
		field.Int64("published_revision_id").Optional().Nillable(),
		field.Int32("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the series.
func (ContentSeries) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_code", "external_id").Unique(),
		index.Fields("candidate_id"),
	}
}
