package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SourceChannel is one web entry of a source.
type SourceChannel struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (SourceChannel) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "source_channel"}}
}

// Fields of the channel.
func (SourceChannel) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("source_id"),
		field.String("code").MaxLen(32),
		field.String("name").MaxLen(80),
		// adapter_key names a metadata adapter the server registered.
		field.String("adapter_key").MaxLen(32),
		field.String("entry_kind").MaxLen(32).Default("rank"),
		field.String("entry_code").MaxLen(32).Default("rank"),
		field.String("base_url").MaxLen(255),
		field.String("allowed_hosts").MaxLen(512),
		field.Int32("rate_per_minute").Default(30),
		field.Int32("timeout_seconds").Default(20),
		// secret_ref names a private server configuration, never the secret.
		field.String("secret_ref").MaxLen(128).Default(""),
		field.String("status").MaxLen(16).Default("enabled"),
		field.Int32("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the channel.
func (SourceChannel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source_id", "code").Unique(),
		index.Fields("status"),
	}
}
