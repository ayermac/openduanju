package service

import (
	"context"
	"errors"

	adminv1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminSourceService manages the source and channel configuration. Reads are
// open to every signed-in role; writes need ingestion_write, so a reviewer can
// see the configuration and cannot change it.
type AdminSourceService struct {
	adminv1.UnimplementedAdminSourceServiceServer
	uc *biz.SourceCatalog
}

// NewAdminSourceService builds the source service.
func NewAdminSourceService(uc *biz.SourceCatalog) *AdminSourceService {
	return &AdminSourceService{uc: uc}
}

// ListSources returns every configured source.
func (s *AdminSourceService) ListSources(ctx context.Context, _ *adminv1.ListSourcesRequest) (*adminv1.SourceSet, error) {
	sources, err := s.uc.ListSources(ctx)
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &adminv1.SourceSet{Items: make([]*adminv1.Source, 0, len(sources))}
	for _, source := range sources {
		out.Items = append(out.Items, toSourceReply(source))
	}
	return out, nil
}

// UpdateSource renames or pauses one source with a version check.
func (s *AdminSourceService) UpdateSource(ctx context.Context, req *adminv1.UpdateSourceRequest) (*adminv1.Source, error) {
	source, err := s.uc.UpdateSource(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetName(), req.GetStatus())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toSourceReply(*source), nil
}

// ListChannels returns the channels, optionally of one source.
func (s *AdminSourceService) ListChannels(ctx context.Context, req *adminv1.ListChannelsRequest) (*adminv1.ChannelSet, error) {
	channels, err := s.uc.ListChannels(ctx, req.GetSourceId())
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &adminv1.ChannelSet{Items: make([]*adminv1.Channel, 0, len(channels))}
	for _, channel := range channels {
		out.Items = append(out.Items, toChannelReply(channel))
	}
	return out, nil
}

// GetChannel returns one channel.
func (s *AdminSourceService) GetChannel(ctx context.Context, req *adminv1.GetChannelRequest) (*adminv1.Channel, error) {
	channel, err := s.uc.GetChannel(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toChannelReply(*channel), nil
}

// UpdateChannel edits the configuration with a version check.
func (s *AdminSourceService) UpdateChannel(ctx context.Context, req *adminv1.UpdateChannelRequest) (*adminv1.Channel, error) {
	channel, err := s.uc.UpdateChannel(ctx, req.GetId(), int(req.GetExpectedVersion()), biz.ChannelUpdate{
		Name:          req.GetName(),
		EntryKind:     req.GetEntryKind(),
		EntryCode:     req.GetEntryCode(),
		BaseURL:       req.GetBaseUrl(),
		AllowedHosts:  req.GetAllowedHosts(),
		RatePerMinute: int(req.GetRatePerMinute()),
		TimeoutSecond: int(req.GetTimeoutSeconds()),
		SecretRef:     req.GetSecretRef(),
	})
	if err != nil {
		return nil, toKratosError(err)
	}
	return toChannelReply(*channel), nil
}

// SetChannelStatus enables or pauses one channel.
func (s *AdminSourceService) SetChannelStatus(ctx context.Context, req *adminv1.SetChannelStatusRequest) (*adminv1.Channel, error) {
	channel, err := s.uc.SetChannelStatus(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetStatus())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toChannelReply(*channel), nil
}

// TestChannel runs one explicit request against the source. Saving a
// configuration never calls out; this does, under the channel timeout.
func (s *AdminSourceService) TestChannel(ctx context.Context, req *adminv1.TestChannelRequest) (*adminv1.ChannelProbe, error) {
	probe, err := s.uc.TestChannel(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	return &adminv1.ChannelProbe{
		Ok:           probe.OK,
		Channel:      probe.Channel,
		LatencyMs:    probe.LatencyMS,
		Items:        int32(probe.Items),
		TotalPages:   int32(probe.TotalPages),
		ErrorCode:    probe.ErrorCode,
		ErrorMessage: probe.ErrorMessage,
		ProbedAt:     formatMediaTime(probe.ProbedAt),
	}, nil
}

func toSourceReply(source biz.SourceConfig) *adminv1.Source {
	return &adminv1.Source{
		Id: source.ID, Code: source.Code, Name: source.Name,
		Status: source.Status, Version: int32(source.Version),
		UpdatedAt: formatMediaTime(source.UpdatedAt),
	}
}

func toChannelReply(channel biz.ChannelConfig) *adminv1.Channel {
	return &adminv1.Channel{
		Id: channel.ID, SourceId: channel.SourceID, SourceCode: channel.SourceCode,
		SourceName: channel.SourceName, SourceStatus: channel.SourceStatus,
		Code: channel.Code, Name: channel.Name, AdapterKey: channel.AdapterKey,
		EntryKind: channel.EntryKind, EntryCode: channel.EntryCode,
		BaseUrl: channel.BaseURL, AllowedHosts: channel.AllowedHosts,
		RatePerMinute: int32(channel.RatePerMinute), TimeoutSeconds: int32(channel.TimeoutSecond),
		HasSecret: channel.HasSecret, Status: channel.Status,
		Paused: channel.Paused, PausedCause: channel.PausedCause,
		Version: int32(channel.Version), UpdatedAt: formatMediaTime(channel.UpdatedAt),
	}
}

// sourceError maps the configuration failures to stable HTTP/gRPC codes.
func sourceError(err error) error {
	switch {
	case errors.Is(err, biz.ErrSourceNotFound):
		return kratoserrors.NotFound("SOURCE_NOT_FOUND", "来源不存在")
	case errors.Is(err, biz.ErrChannelNotFound):
		return kratoserrors.NotFound("CHANNEL_NOT_FOUND", "通道不存在")
	case errors.Is(err, biz.ErrSourceVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "来源已被他人修改，请刷新后重试")
	case errors.Is(err, biz.ErrChannelVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "通道已被他人修改，请刷新后重试")
	case errors.Is(err, biz.ErrSourceName):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "来源名称必填，且不超过80字")
	case errors.Is(err, biz.ErrSourceStatus):
		return kratoserrors.BadRequest("INVALID_STATUS", "状态只支持 enabled 或 paused")
	case errors.Is(err, biz.ErrChannelName):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "通道名称必填，且不超过80字")
	case errors.Is(err, biz.ErrChannelEntry):
		return kratoserrors.BadRequest("UNSUPPORTED_ENTRY", "该入口未在服务端注册，无法保存")
	case errors.Is(err, biz.ErrChannelAdapter):
		return kratoserrors.BadRequest("UNSUPPORTED_ADAPTER", "该适配器未在服务端注册")
	case errors.Is(err, biz.ErrChannelURL):
		return kratoserrors.BadRequest("INVALID_SOURCE_URL", "入口地址必须是 HTTPS 源站，且主机在白名单内")
	case errors.Is(err, biz.ErrChannelRate):
		return kratoserrors.BadRequest("INVALID_RATE", "限频需在1~600次/分钟之间")
	case errors.Is(err, biz.ErrChannelTimeout):
		return kratoserrors.BadRequest("INVALID_TIMEOUT", "超时需在5~120秒之间")
	case errors.Is(err, biz.ErrSourcePaused):
		return kratoserrors.Conflict("SOURCE_PAUSED", "来源或通道已暂停，不能发起新的采集")
	case errors.Is(err, biz.ErrChannelProbeUsage):
		return kratoserrors.Conflict("CHANNEL_PAUSED", "通道已暂停，请先启用再测试连接")
	}
	return nil
}
