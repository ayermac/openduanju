package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MediaAsset is one confirmed object bound to a revision episode.
type MediaAsset struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (MediaAsset) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "media_asset"}}
}

// Fields of the asset.
func (MediaAsset) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("provider").MaxLen(16).Default(""),
		field.String("bucket").MaxLen(128).Default(""),
		field.String("region").MaxLen(64).Default(""),
		field.String("object_key").MaxLen(512).Default(""),
		field.String("object_version").MaxLen(128).Default(""),
		field.String("sha256").MaxLen(64).Default(""),
		field.Int64("size_bytes").Default(0),
		field.String("content_type").MaxLen(64).Default(""),
		field.String("codec").MaxLen(32).Default(""),
		field.Int64("duration_ms").Default(0),
		field.Int("width").Default(0),
		field.Int("height").Default(0),
		field.String("state").MaxLen(16).Default("verified"),
		// kind separates episode media from covers, so a cover never enters
		// the episode counters of a batch.
		field.String("kind").MaxLen(16).Default("episode"),
		field.Int64("revision_id").Default(0),
		field.Int64("episode_id").Default(0),
		field.Int64("batch_id").Default(0),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the asset.
func (MediaAsset) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "bucket", "region", "object_key", "object_version").Unique(),
		index.Fields("revision_id"),
	}
}
