package service

import (
	"context"
	"strings"

	adminv1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"
)

// AdminAuditService reads the recorded writes of the console.
type AdminAuditService struct {
	adminv1.UnimplementedAdminAuditServiceServer
	auditor *biz.Auditor
}

// NewAdminAuditService builds the audit service.
func NewAdminAuditService(auditor *biz.Auditor) *AdminAuditService {
	return &AdminAuditService{auditor: auditor}
}

// ListAuditEvents returns the recorded events. A failed read is an error, not
// an empty page: an operator who cannot read the list must not be told that
// nothing was recorded.
func (s *AdminAuditService) ListAuditEvents(ctx context.Context, req *adminv1.ListAuditEventsRequest) (*adminv1.AuditEventSet, error) {
	events, total, err := s.auditor.List(ctx, biz.AuditQuery{
		ResourceType: strings.TrimSpace(req.GetResourceType()),
		ResourceID:   strings.TrimSpace(req.GetResourceId()),
		Action:       strings.TrimSpace(req.GetAction()),
		ActorID:      req.GetActorId(),
		ActorName:    strings.TrimSpace(req.GetActorName()),
		TraceID:      strings.TrimSpace(req.GetTraceId()),
		Page:         int(req.GetPage()),
		PageSize:     int(req.GetPageSize()),
	})
	if err != nil {
		return nil, toKratosError(err)
	}
	page := int(req.GetPage())
	if page <= 0 {
		page = 1
	}
	size := int(req.GetPageSize())
	if size <= 0 {
		size = 20
	}
	out := &adminv1.AuditEventSet{
		Items:    make([]*adminv1.AuditEvent, 0, len(events)),
		Total:    int32(total),
		Page:     int32(page),
		PageSize: int32(size),
	}
	for _, event := range events {
		out.Items = append(out.Items, &adminv1.AuditEvent{
			Id: event.ID, TraceId: event.TraceID, ActorId: event.ActorID, ActorName: event.ActorName,
			Action: event.Action, ResourceType: event.ResourceType, ResourceId: event.ResourceID,
			ResourceVersion: int32(event.ResourceVersion), Result: event.Result, Reason: event.Reason,
			CreatedAt: formatMediaTime(event.CreatedAt),
		})
	}
	return out, nil
}
