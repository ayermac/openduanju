package data

import (
	"context"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediaitem"
)

// InheritedAssetForItem returns the ready material a revision inherited with
// its selection.
//
// A new revision copies the episodes of its base revision, so a task can name
// an episode whose material was already processed and confirmed. The asset row
// alone is not enough to reuse it: the task that produced those bytes names
// the confirmed input and the processing profile, and only that pair proves
// the material still describes this task. A revision whose episode holds no
// ready material, or whose material has no producing task on record, returns
// nil so the task is processed from its input.
func (r *MediaBatchRepo) InheritedAssetForItem(ctx context.Context, item biz.MediaItem) (*biz.InheritedAsset, error) {
	if item.EpisodeID <= 0 {
		return nil, nil
	}
	episode, err := r.client.RevisionEpisode.Get(ctx, item.EpisodeID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if episode.AssetID <= 0 {
		return nil, nil
	}
	assetRow, err := r.client.MediaAsset.Get(ctx, episode.AssetID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	// Only ready episode material is reusable: a cover never enters the
	// episode tasks, and an orphan object is not publishable.
	if assetRow.State != string(biz.MediaAssetReady) || assetRow.Kind != "episode" {
		return nil, nil
	}
	producer, err := r.client.MediaItem.Query().
		Where(mediaitem.AssetIDEQ(assetRow.ID)).
		Order(ent.Asc(mediaitem.FieldID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	producerBatch, err := r.client.MediaBatch.Get(ctx, producer.BatchID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &biz.InheritedAsset{
		Asset: biz.MediaAsset{
			Object: biz.ObjectRef{
				Provider:  assetRow.Provider,
				Bucket:    assetRow.Bucket,
				Region:    assetRow.Region,
				Key:       assetRow.ObjectKey,
				VersionID: assetRow.ObjectVersion,
			},
			State:       biz.MediaAssetState(assetRow.State),
			SizeBytes:   assetRow.SizeBytes,
			SHA256:      assetRow.Sha256,
			ContentType: assetRow.ContentType,
			Info: &biz.MediaInfo{
				DurationMS: assetRow.DurationMs,
				Width:      assetRow.Width,
				Height:     assetRow.Height,
				VideoCodec: assetRow.Codec,
			},
		},
		ProducerInputUploadID: producer.InputUploadID,
		ProducerExternalID:    producer.ExternalID,
		ProducerProfile:       producerBatch.ProfileVersion,
	}, nil
}
