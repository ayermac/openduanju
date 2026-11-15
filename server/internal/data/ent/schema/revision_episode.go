package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RevisionEpisode is one selectable episode in a draft revision.
type RevisionEpisode struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (RevisionEpisode) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "revision_episode"}}
}

// Fields of the revision episode.
func (RevisionEpisode) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("revision_id"),
		// An imported episode carries the provider identifier; a manual one
		// has none and is identified by its revision and original number.
		field.String("external_id").MaxLen(64).Optional().Nillable(),
		field.Int32("episode_no"),
		field.Bool("selected").Default(false),
		// A confirmed manual upload replaces the SDK as the input of this
		// episode; input_state=ready means the object was confirmed by the
		// server, not that the episode has ready material.
		field.String("input_kind").MaxLen(16).Default("sdk"),
		field.Int64("input_ref").Default(0),
		field.String("input_state").MaxLen(16).Default(""),
		field.Int64("asset_id").Default(0),
		field.String("asset_state").MaxLen(16).Default(""),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the revision episode.
func (RevisionEpisode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("revision_id", "external_id").Unique(),
		index.Fields("revision_id", "episode_no").Unique(),
	}
}
