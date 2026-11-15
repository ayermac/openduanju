package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MediaItem is one episode task inside a media batch.
type MediaItem struct{ ent.Schema }

// Annotations binds the entity to the migration-owned table.
func (MediaItem) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "media_item"}}
}

// Fields of the item.
func (MediaItem) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("batch_id"),
		field.Int64("episode_id"),
		field.String("external_id").MaxLen(64).Default(""),
		field.Int("episode_no").Default(0),
		// trace_id is the identity of the submission that created the task.
		// A worker that resumes the task after a restart reads it back from
		// the row and logs it with the stage, the attempt and the lease.
		field.String("trace_id").MaxLen(64).Default(""),
		field.String("status").MaxLen(16).Default("queued"),
		field.Int("attempt").Default(0),
		field.Bool("retryable").Default(false),
		field.Time("next_eligible_at").Optional().Nillable(),
		field.String("error_code").MaxLen(64).Default(""),
		field.String("error_message").MaxLen(512).Default(""),
		field.Int("lease_generation").Default(0),
		field.String("lease_owner").MaxLen(64).Default(""),
		field.Time("lease_until").Optional().Nillable(),
		field.Int("input_version").Default(0),
		// input_upload_id names the confirmed upload session that feeds this
		// task instead of the resolver. Zero keeps the SDK path.
		field.Int64("input_upload_id").Default(0),
		field.Int64("asset_id").Default(0),
		field.String("object_provider").MaxLen(16).Default(""),
		field.String("object_bucket").MaxLen(128).Default(""),
		field.String("object_region").MaxLen(64).Default(""),
		field.String("object_key").MaxLen(512).Default(""),
		field.String("object_version").MaxLen(128).Default(""),
		field.String("input_artifact_path").MaxLen(512).Default(""),
		field.String("input_artifact_sha256").MaxLen(64).Default(""),
		field.String("output_artifact_path").MaxLen(512).Default(""),
		field.String("output_artifact_sha256").MaxLen(64).Default(""),
		field.Int64("output_artifact_size").Default(0),
		field.String("sha256").MaxLen(64).Default(""),
		field.Int64("size_bytes").Default(0),
		field.String("content_type").MaxLen(64).Default(""),
		field.String("codec").MaxLen(32).Default(""),
		field.Int64("duration_ms").Default(0),
		field.Int("width").Default(0),
		field.Int("height").Default(0),
		field.Int("version").Default(1),
		field.Time("created_at").Default(timeNow),
		field.Time("updated_at").Default(timeNow).UpdateDefault(timeNow),
	}
}

// Indexes of the item.
func (MediaItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("batch_id", "episode_id").Unique(),
		index.Fields("status", "next_eligible_at"),
		index.Fields("lease_until"),
	}
}
