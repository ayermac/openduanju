package data

import (
	"context"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/source"
	"openduanju-server/internal/data/ent/sourcechannel"
)

// ListSources returns every source with its version.
func (r *SourceCatalogRepo) ListSources(ctx context.Context) ([]biz.SourceConfig, error) {
	rows, err := r.db.Source.Query().Order(ent.Asc(source.FieldCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.SourceConfig, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSourceConfig(row))
	}
	return out, nil
}

// UpdateSource renames or pauses one source with a version check.
func (r *SourceCatalogRepo) UpdateSource(ctx context.Context, id int64, expectedVersion int, name, status string) (*biz.SourceConfig, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	update := tx.Source.Update().Where(source.IDEQ(id), source.VersionEQ(int32(expectedVersion))).SetName(name)
	if status != "" {
		update = update.SetStatus(status)
	}
	affected, err := update.AddVersion(1).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if affected == 0 {
		_ = tx.Rollback()
		exists, err := r.db.Source.Query().Where(source.IDEQ(id)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, biz.ErrSourceNotFound
		}
		return nil, biz.ErrSourceVersion
	}
	row, err := tx.Source.Get(ctx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	out := toSourceConfig(row)
	return &out, nil
}

// ListChannels returns the channels of one source, or all of them.
func (r *SourceCatalogRepo) ListChannels(ctx context.Context, sourceID int64) ([]biz.ChannelConfig, error) {
	query := r.db.SourceChannel.Query()
	if sourceID > 0 {
		query = query.Where(sourcechannel.SourceIDEQ(sourceID))
	}
	rows, err := query.Order(ent.Asc(sourcechannel.FieldSourceID), ent.Asc(sourcechannel.FieldCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.ChannelConfig, 0, len(rows))
	for _, row := range rows {
		channel, err := r.toChannelConfig(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, nil
}

// GetChannel returns one channel with its source status.
func (r *SourceCatalogRepo) GetChannel(ctx context.Context, id int64) (*biz.ChannelConfig, error) {
	row, err := r.db.SourceChannel.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	channel, err := r.toChannelConfig(ctx, row)
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetChannelByCode resolves the pair a plan names.
func (r *SourceCatalogRepo) GetChannelByCode(ctx context.Context, sourceCode, channelCode string) (*biz.ChannelConfig, error) {
	owner, err := r.db.Source.Query().Where(source.CodeEQ(sourceCode)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	row, err := r.db.SourceChannel.Query().
		Where(sourcechannel.SourceIDEQ(owner.ID), sourcechannel.CodeEQ(channelCode)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	channel, err := r.toChannelConfig(ctx, row)
	if err != nil {
		return nil, err
	}
	return &channel, nil
}

// UpdateChannel writes the editable configuration with a version check. The
// adapter key and the channel code are not editable here: changing them would
// silently repoint plans that already name this channel.
func (r *SourceCatalogRepo) UpdateChannel(ctx context.Context, id int64, expectedVersion int, update biz.ChannelUpdate) (*biz.ChannelConfig, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	affected, err := tx.SourceChannel.Update().
		Where(sourcechannel.IDEQ(id), sourcechannel.VersionEQ(int32(expectedVersion))).
		SetName(update.Name).
		SetEntryKind(update.EntryKind).
		SetEntryCode(update.EntryCode).
		SetBaseURL(update.BaseURL).
		SetAllowedHosts(update.AllowedHosts).
		SetRatePerMinute(int32(update.RatePerMinute)).
		SetTimeoutSeconds(int32(update.TimeoutSecond)).
		SetSecretRef(update.SecretRef).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if affected == 0 {
		_ = tx.Rollback()
		exists, err := r.db.SourceChannel.Query().Where(sourcechannel.IDEQ(id)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, biz.ErrChannelNotFound
		}
		return nil, biz.ErrChannelVersion
	}
	row, err := tx.SourceChannel.Get(ctx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetChannel(ctx, id)
}

// SetChannelStatus enables or pauses one channel with a version check.
func (r *SourceCatalogRepo) SetChannelStatus(ctx context.Context, id int64, expectedVersion int, status string) (*biz.ChannelConfig, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	affected, err := tx.SourceChannel.Update().
		Where(sourcechannel.IDEQ(id), sourcechannel.VersionEQ(int32(expectedVersion))).
		SetStatus(status).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if affected == 0 {
		_ = tx.Rollback()
		exists, err := r.db.SourceChannel.Query().Where(sourcechannel.IDEQ(id)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, biz.ErrChannelNotFound
		}
		return nil, biz.ErrChannelVersion
	}
	row, err := tx.SourceChannel.Get(ctx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetChannel(ctx, id)
}

func (r *SourceCatalogRepo) toChannelConfig(ctx context.Context, row *ent.SourceChannel) (biz.ChannelConfig, error) {
	owner, err := r.db.Source.Get(ctx, row.SourceID)
	if err != nil {
		return biz.ChannelConfig{}, err
	}
	out := biz.ChannelConfig{
		ID:            row.ID,
		SourceID:      owner.ID,
		SourceCode:    owner.Code,
		SourceName:    owner.Name,
		SourceStatus:  owner.Status,
		Code:          row.Code,
		Name:          row.Name,
		AdapterKey:    row.AdapterKey,
		EntryKind:     row.EntryKind,
		EntryCode:     row.EntryCode,
		BaseURL:       row.BaseURL,
		AllowedHosts:  row.AllowedHosts,
		RatePerMinute: int(row.RatePerMinute),
		TimeoutSecond: int(row.TimeoutSeconds),
		SecretRef:     row.SecretRef,
		HasSecret:     row.SecretRef != "",
		Status:        row.Status,
		Version:       int(row.Version),
		UpdatedAt:     row.UpdatedAt,
	}
	if out.ChannelPaused() {
		out.Paused = true
		if owner.Status == biz.SourceStatusPaused {
			out.PausedCause = "source"
		} else {
			out.PausedCause = "channel"
		}
	}
	return out, nil
}

func toSourceConfig(row *ent.Source) biz.SourceConfig {
	return biz.SourceConfig{
		ID:        row.ID,
		Code:      row.Code,
		Name:      row.Name,
		Status:    row.Status,
		Version:   int(row.Version),
		UpdatedAt: row.UpdatedAt,
	}
}
