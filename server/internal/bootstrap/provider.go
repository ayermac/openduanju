package bootstrap

import (
	"openduanju-server/internal/conf"

	"github.com/google/wire"
)

// ProviderSet exposes the resolved database identity to the services.
var ProviderSet = wire.NewSet(NewIdentity)

// NewIdentity resolves the database identity from the configuration. The
// startup check rejects an unusable DSN before this provider runs.
func NewIdentity(data *conf.Data) (Identity, error) {
	return ParseDSN(data.GetDatabase().GetSource())
}
