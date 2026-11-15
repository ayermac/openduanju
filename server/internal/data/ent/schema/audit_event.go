package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuditEvent is one recorded write of the console.
type AuditEvent struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (AuditEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "audit_event"}}
}

// Fields of the audit event.
func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		// trace_id is the identity of the request the event belongs to.
		field.String("trace_id").MaxLen(64).Default(""),
		field.Int64("actor_id").Default(0),
		field.String("actor_name").MaxLen(64).Default(""),
		field.String("action").MaxLen(64),
		field.String("resource_type").MaxLen(32),
		field.String("resource_id").MaxLen(64).Default(""),
		field.Int32("resource_version").Default(0),
		field.String("result").MaxLen(16).Default("success"),
		field.String("reason").MaxLen(128).Default(""),
		field.Time("created_at").Default(timeNow),
	}
}

// Indexes of the audit event.
func (AuditEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("resource_type", "resource_id"),
		index.Fields("actor_id"),
		index.Fields("trace_id"),
		index.Fields("created_at"),
	}
}
