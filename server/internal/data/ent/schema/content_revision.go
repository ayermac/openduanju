package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ContentRevision is one editable snapshot of a series.
type ContentRevision struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (ContentRevision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "content_revision"}}
}

// Fields of the revision.
func (ContentRevision) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("series_id"),
		field.Int32("revision_no"),
		field.String("state").MaxLen(16).Default("draft"),
		field.Int32("version").Default(1),
		field.String("title").MaxLen(255).Default(""),
		field.Text("description").Default(""),
		field.String("cover_url").MaxLen(512).Default(""),
		field.JSON("tags", []string{}).Optional(),
		// cover_asset_id is the immutable cover object bound to this
		// revision. A batch inherits it, so the cover is not uploaded twice.
		field.Int64("cover_asset_id").Default(0),
		// The authorization the revision was frozen with. The hash covers the
		// permission bits, the reference and the validity window; the grant
		// itself is never copied into the revision.
		field.Int64("authorization_grant_id").Default(0),
		field.Int("authorization_version").Default(0),
		field.String("authorization_hash").MaxLen(64).Default(""),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the revision.
func (ContentRevision) Indexes() []ent.Index {
	return []ent.Index{index.Fields("series_id", "revision_no").Unique()}
}
