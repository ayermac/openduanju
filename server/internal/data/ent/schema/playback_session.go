package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PlaybackSession is one anonymous playback of one frozen asset.
type PlaybackSession struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (PlaybackSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "playback_session"}}
}

// Fields of the playback session.
func (PlaybackSession) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("token_hash").MaxLen(64),
		field.Int64("publication_id"),
		// publication_generation pins the session to one publication run.
		field.Int("publication_generation").Default(1),
		field.Int64("series_id"),
		field.Int64("revision_id"),
		field.Int64("episode_id"),
		field.Int64("asset_id"),
		field.String("object_provider").MaxLen(16).Default(""),
		field.String("object_bucket").MaxLen(128).Default(""),
		field.String("object_region").MaxLen(64).Default(""),
		field.String("object_key").MaxLen(512).Default(""),
		field.String("object_version").MaxLen(128).Default(""),
		field.Time("expires_at"),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the playback session.
func (PlaybackSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("token_hash").Unique(),
		index.Fields("publication_id"),
	}
}
