package data

import (
	"context"
	"database/sql"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/data/ent"

	"github.com/go-kratos/kratos/v3/log"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/wire"
)

// ProviderSet is data providers.
var ProviderSet = wire.NewSet(
	NewData,
	NewTodoRepo,
	NewSourceCatalogRepo,
	NewWebMetadataAdapter,
	NewMediaBatchRepo,
	NewUploadRepo,
	NewAuthorizationRepo,
	NewStaffRepo,
	NewReviewRepo,
	NewPreviewStorage,
	NewWebRepo,
	NewAuditRepo,
	NewMediaWorkerProvider,
	NewUploadsProvider,
	NewStagingHandler,
	wire.Bind(new(biz.MediaBatchRepo), new(*MediaBatchRepo)),
	wire.Bind(new(biz.MediaContentRepo), new(*MediaBatchRepo)),
	wire.Bind(new(biz.UploadRepo), new(*UploadRepo)),
	wire.Bind(new(biz.UploadContentRepo), new(*UploadRepo)),
	wire.Bind(new(biz.AuthorizationRepo), new(*AuthorizationRepo)),
	wire.Bind(new(biz.StaffRepo), new(*StaffRepo)),
	wire.Bind(new(biz.ReviewRepo), new(*ReviewRepo)),
	wire.Bind(new(biz.WebRepo), new(*WebRepo)),
)

// Data holds the long-lived storage clients shared by repos.
type Data struct {
	db    *ent.Client
	sqlDB *sql.DB
}

// SQL exposes the shared SQL pool for migrations and health checks.
func (d *Data) SQL() *sql.DB { return d.sqlDB }

// Ent exposes the typed client for business entity access.
func (d *Data) Ent() *ent.Client { return d.db }

// NewData opens the database client and returns it with a cleanup function.
func NewData(c *conf.Data) (*Data, func(), error) {
	dc := c.GetDatabase()
	db, err := ent.Open(dc.GetDriver(), dc.GetSource())
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := sql.Open(dc.GetDriver(), dc.GetSource())
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	if dc.GetDebug() {
		db = db.Debug()
	}
	// Auto migration is a convenience for local development. In production,
	// apply schema changes as a separate reviewed step instead.
	if dc.GetAutoMigrate() {
		if err := db.Schema.Create(context.Background()); err != nil {
			db.Close()
			sqlDB.Close()
			return nil, nil, err
		}
	}
	cleanup := func() {
		log.Info("closing the data resources")
		if err := db.Close(); err != nil {
			log.Error("failed closing the database", "err", err)
		}
		if err := sqlDB.Close(); err != nil {
			log.Error("failed closing the SQL pool", "err", err)
		}
	}
	return &Data{db: db, sqlDB: sqlDB}, cleanup, nil
}
