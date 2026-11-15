package server

import (
	"testing"

	"openduanju-server/internal/biz"
)

// TestCapabilityForKeepsPublishSeparate is the regression that the review
// routes must not inherit the review capability for publication.
func TestCapabilityForKeepsPublishSeparate(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		mutating  bool
		want      biz.Capability
	}{
		{"http publish", "/api/admin/v1/series/1/publications", true, biz.CapPublish},
		{"http unpublish", "/api/admin/v1/series/1/publications/unpublish", true, biz.CapPublish},
		{"http publication read", "/api/admin/v1/series/1/publication", false, biz.CapRead},
		{"http review list", "/api/admin/v1/reviews", false, biz.CapRead},
		{"http review claim", "/api/admin/v1/reviews/1/claim", true, biz.CapReview},
		{"http review decision", "/api/admin/v1/reviews/1/decision", true, biz.CapReview},
		{"grpc publish", "/admin.v1.AdminReviewService/PublishRevision", true, biz.CapPublish},
		{"grpc unpublish", "/admin.v1.AdminReviewService/UnpublishSeries", true, biz.CapPublish},
		{"grpc claim", "/admin.v1.AdminReviewService/ClaimReview", true, biz.CapReview},
		{"grpc decision", "/admin.v1.AdminReviewService/DecideReview", true, biz.CapReview},
		{"grpc media submit", "/admin.v1.AdminMediaService/SubmitRevision", true, biz.CapMediaWrite},
		{"grpc staff create", "/admin.v1.AdminStaffService/CreateStaff", true, biz.CapStaffManage},
		// A manual work is created on the same content surface as an imported
		// one, so the reviewer role is refused on both transports.
		// The source configuration drives collection: a reviewer may read it
		// but not change it, on both transports.
		{"http list sources", "/api/admin/v1/sources", false, biz.CapRead},
		{"http update source", "/api/admin/v1/sources/1", true, biz.CapIngestionWrite},
		{"http list channels", "/api/admin/v1/source-channels", false, biz.CapRead},
		{"http update channel", "/api/admin/v1/source-channels/1", true, biz.CapIngestionWrite},
		{"http pause channel", "/api/admin/v1/source-channels/1/status", true, biz.CapIngestionWrite},
		{"http probe channel", "/api/admin/v1/source-channels/1/probe", true, biz.CapIngestionWrite},
		{"grpc list sources", "/admin.v1.AdminSourceService/ListSources", false, biz.CapRead},
		{"grpc update channel", "/admin.v1.AdminSourceService/UpdateChannel", true, biz.CapIngestionWrite},
		{"http create series", "/api/admin/v1/series", true, biz.CapContentWrite},
		{"grpc create series", "/admin.v1.AdminContentService/CreateSeries", true, biz.CapContentWrite},
		// The audit list is readable by every signed-in role and is never a
		// write surface, on both transports.
		{"http audit list", "/api/admin/v1/audit", false, biz.CapRead},
		{"grpc audit list", "/admin.v1.AdminAuditService/ListAuditEvents", false, biz.CapRead},
	}
	for _, testCase := range cases {
		got, required := capabilityFor(testCase.operation, testCase.mutating)
		if !required {
			t.Fatalf("%s: the operation is not guarded", testCase.name)
		}
		if got != testCase.want {
			t.Fatalf("%s: capability = %s, want %s", testCase.name, got, testCase.want)
		}
	}
}

// TestPublishCapabilityExcludesReviewers proves the role boundary the report
// had wrong: operators publish, reviewers do not.
func TestPublishCapabilityExcludesReviewers(t *testing.T) {
	if !biz.RoleAllows(biz.RoleOperator, biz.CapPublish) {
		t.Fatal("operators must be able to publish")
	}
	if !biz.RoleAllows(biz.RoleAdmin, biz.CapPublish) {
		t.Fatal("administrators must be able to publish")
	}
	if biz.RoleAllows(biz.RoleReviewer, biz.CapPublish) {
		t.Fatal("reviewers must not publish")
	}
	if biz.RoleAllows(biz.RoleReviewer, biz.CapMediaWrite) {
		t.Fatal("reviewers must not submit media batches")
	}
	if !biz.RoleAllows(biz.RoleOperator, biz.CapMediaWrite) {
		t.Fatal("operators must be able to submit media batches")
	}
}

// TestReadAndWriteComeFromTheOperation proves the read and write decision
// comes from the operation the server defines. A legal gRPC read is a read,
// and a client cannot downgrade a write by claiming a read.
func TestReadAndWriteComeFromTheOperation(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		wantWrite bool
	}{
		{"grpc read", "/admin.v1.AdminContentService/ListSeries", false},
		{"grpc detail read", "/admin.v1.AdminContentService/GetSeries", false},
		{"grpc write", "/admin.v1.AdminContentService/UpdateRevision", true},
		{"grpc create", "/admin.v1.AdminContentService/CreateSeries", true},
		{"grpc candidate read", "/admin.v1.AdminCandidateService/ListCandidates", false},
		{"grpc ingestion write", "/admin.v1.AdminIngestionService/CreatePlan", true},
	}
	for _, testCase := range cases {
		if got := isMutatingOperation(testCase.operation); got != testCase.wantWrite {
			t.Fatalf("%s: mutating = %v, want %v", testCase.name, got, testCase.wantWrite)
		}
	}
	// A legal read needs only the read capability.
	capability, required := capabilityFor("/admin.v1.AdminContentService/ListSeries", false)
	if !required || capability != biz.CapRead {
		t.Fatalf("the legal read: capability = %s required = %v", capability, required)
	}
	// A write keeps the write capability even when the client claims a read
	// in a header: the header is not part of the decision.
	capability, required = capabilityFor("/admin.v1.AdminContentService/UpdateRevision", true)
	if !required || capability != biz.CapContentWrite {
		t.Fatalf("the forged read header downgraded the write: capability = %s required = %v", capability, required)
	}
	if biz.RoleAllows(biz.RoleReviewer, capability) {
		t.Fatal("a reviewer must not edit a draft")
	}
}
