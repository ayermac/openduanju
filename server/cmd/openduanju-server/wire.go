//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built in the final build.

package main

import (
	"log/slog"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/bootstrap"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/data"
	"openduanju-server/internal/server"
	"openduanju-server/internal/service"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
)

// wireApp init kratos application.
func wireApp(*conf.Server, *conf.Data, *conf.Source, *conf.Media, *conf.Admin, *slog.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, data.ProviderSet, biz.ProviderSet, service.ProviderSet, bootstrap.ProviderSet, newApp))
}
