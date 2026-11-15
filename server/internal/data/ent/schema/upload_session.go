package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UploadSession is one short-lived manual upload of an episode file or a
// cover image. The session owns the staging object key, so a renewed
// authorization signs the same object instead of starting a second upload.
type UploadSession struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (UploadSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "upload_session"}}
}

// Fields of the upload session.
func (UploadSession) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("revision_id"),
		field.Int64("episode_id").Default(0),
		field.String("kind").MaxLen(16).Default("episode_input"),
		field.String("status").MaxLen(16).Default("pending"),
		field.Int("version").Default(1),
		field.String("provider").MaxLen(16).Default(""),
		field.String("bucket").MaxLen(128).Default(""),
		field.String("region").MaxLen(64).Default(""),
		field.String("staging_key").MaxLen(512),
		field.String("object_key").MaxLen(512).Default(""),
		field.String("object_version").MaxLen(128).Default(""),
		field.String("filename").MaxLen(255).Default(""),
		field.String("content_type").MaxLen(128).Default(""),
		field.Int64("size_bytes").Default(0),
		field.String("expected_sha256").MaxLen(64).Default(""),
		field.String("sha256").MaxLen(64).Default(""),
		field.Int("width").Default(0),
		field.Int("height").Default(0),
		field.Int64("asset_id").Default(0),
		field.String("created_by").MaxLen(64).Default(""),
		field.Int("revision_version").Default(0),
		field.Time("expires_at"),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the upload session.
func (UploadSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "bucket", "region", "staging_key").Unique(),
		index.Fields("revision_id", "kind", "status"),
		index.Fields("status", "expires_at"),
	}
}
