package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SourceCandidate is the local snapshot of one source ranking entry.
type SourceCandidate struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (SourceCandidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "source_candidate"}}
}

// Fields of the candidate snapshot.
func (SourceCandidate) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("source_code").MaxLen(32),
		field.String("channel").MaxLen(64).Default(""),
		field.String("external_id").MaxLen(64),
		field.String("title").MaxLen(255),
		field.String("cover_url").MaxLen(512).Default(""),
		field.Text("description").Default(""),
		field.JSON("tags", []string{}).Optional(),
		field.Int32("episode_count").Default(0),
		field.Int32("open_episode_count").Default(0),
		field.Int32("rank_no").Default(0),
		field.JSON("metrics", map[string]string{}).Optional(),
		field.String("content_hash").MaxLen(64),
		field.String("completeness").MaxLen(16).Default("summary"),
		field.Time("detail_fetched_at").Optional().Nillable(),
		field.Time("episodes_fetched_at").Optional().Nillable(),
		field.Int64("series_id").Optional().Nillable(),
		field.Int64("last_run_id").Optional().Nillable(),
		field.Time("first_seen_at"),
		field.Time("last_seen_at"),
		field.Time("updated_at").Default(timeNow),
	}
}

// Indexes of the candidate snapshot.
func (SourceCandidate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_code", "external_id").Unique(),
		index.Fields("last_seen_at"),
		index.Fields("title"),
	}
}
