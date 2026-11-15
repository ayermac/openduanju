package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthorizationGrant is one series-level authorization record. A grant is
// immutable after creation: a corrected permission or validity window is a
// new row, and an old row can only be revoked.
type AuthorizationGrant struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (AuthorizationGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "authorization_grant"}}
}

// Fields of the grant.
func (AuthorizationGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("series_id"),
		field.String("reference").MaxLen(255).Default(""),
		// The permission bits default to false: an omitted bit must never
		// grant a right.
		field.Bool("allow_process").Default(false),
		field.Bool("allow_display").Default(false),
		field.Bool("allow_playback").Default(false),
		field.Time("valid_from"),
		field.Time("valid_until").Optional().Nillable(),
		field.String("status").MaxLen(16).Default("active"),
		field.Int("version").Default(1),
		field.String("confirmed_by").MaxLen(64).Default(""),
		field.Time("confirmed_at"),
		field.String("revoked_by").MaxLen(64).Default(""),
		field.Time("revoked_at").Optional().Nillable(),
		field.String("revoke_reason").MaxLen(512).Default(""),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the grant.
func (AuthorizationGrant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("series_id", "status"),
		index.Fields("status", "valid_until"),
	}
}
