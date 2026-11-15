package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Staff is one back-office account with a fixed role.
type Staff struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (Staff) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "staff"}}
}

// Fields of the staff account.
func (Staff) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.String("username").MaxLen(64),
		field.String("display_name").MaxLen(128).Default(""),
		field.String("role").MaxLen(16).Default("operator"),
		field.Bool("enabled").Default(true),
		field.String("password_hash").MaxLen(255),
		field.Int("password_version").Default(1),
		field.Int("version").Default(1),
		field.Time("last_login_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the staff account.
func (Staff) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("username").Unique(),
	}
}

// StaffSession is one revocable browser session.
type StaffSession struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (StaffSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "staff_session"}}
}

// Fields of the session.
func (StaffSession) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("staff_id"),
		field.String("token_hash").MaxLen(64),
		field.String("csrf_token").MaxLen(64),
		field.Time("expires_at"),
		field.Time("revoked_at").Optional().Nillable(),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the session.
func (StaffSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("token_hash").Unique(),
		index.Fields("staff_id", "revoked_at"),
	}
}
