package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LoginAttempt is one login try, used for a sliding-window throttle.
type LoginAttempt struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (LoginAttempt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "login_attempt"}}
}

// Fields of the attempt.
func (LoginAttempt) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("username").MaxLen(64),
		field.Time("attempted_at"),
		field.Bool("succeeded").Default(false),
	}
}

// Indexes of the attempt.
func (LoginAttempt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("username", "attempted_at"),
	}
}

// ReviewDecision is the single, final outcome of a review task.
type ReviewDecision struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (ReviewDecision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "review_decision"}}
}

// Fields of the decision.
func (ReviewDecision) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("review_task_id"),
		field.String("decision").MaxLen(16),
		field.String("reason").MaxLen(1000).Default(""),
		field.String("decided_by").MaxLen(64),
		field.Time("decided_at"),
		field.Int("task_version"),
		field.Int64("authorization_grant_id").Default(0),
		field.Int("authorization_version").Default(0),
		field.String("authorization_hash").MaxLen(64).Default(""),
		// checklist is the JSON snapshot of the items the reviewer confirmed.
		field.String("checklist").MaxLen(512).Default(""),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the decision.
func (ReviewDecision) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("review_task_id").Unique(),
	}
}

// Publication is the published revision of one series.
type Publication struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (Publication) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "publication"}}
}

// Fields of the publication.
func (Publication) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("series_id"),
		field.Int64("revision_id"),
		field.String("status").MaxLen(16).Default("published"),
		field.String("published_by").MaxLen(64),
		field.Time("published_at"),
		field.String("unpublished_by").MaxLen(64).Default(""),
		field.Time("unpublished_at").Optional().Nillable(),
		field.Int64("authorization_grant_id").Default(0),
		field.Int("authorization_version").Default(0),
		field.String("authorization_hash").MaxLen(64).Default(""),
		// generation counts the publications of one revision. A playback
		// session stores it, so a re-publication invalidates the old token.
		field.Int("generation").Default(1),
		// category is frozen with the publication.
		field.String("category").MaxLen(64).Default(""),
		field.Int("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the publication.
func (Publication) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("revision_id").Unique(),
		index.Fields("series_id", "status"),
	}
}
