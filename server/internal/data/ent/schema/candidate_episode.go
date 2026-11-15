package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CandidateEpisode is one real episode identity observed for a candidate.
type CandidateEpisode struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (CandidateEpisode) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "candidate_episode"}}
}

// Fields of the observed episode.
func (CandidateEpisode) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("candidate_id"),
		field.String("external_id").MaxLen(64),
		field.Int32("episode_no"),
		field.String("title").MaxLen(255).Default(""),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the observed episode.
func (CandidateEpisode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("candidate_id", "external_id").Unique(),
		index.Fields("candidate_id", "episode_no").Unique(),
	}
}
