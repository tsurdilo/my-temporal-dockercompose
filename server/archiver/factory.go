package archiver

import (
	"go.temporal.io/server/common/archiver"
	"go.temporal.io/server/common/archiver/provider"
)

// NewHistoryArchiverFactory returns a CustomHistoryArchiverFactory that handles
// the "minio" scheme. For any other scheme it returns ErrUnknownScheme, allowing
// the ArchiverProvider to fall through to the built-in implementations.
func NewHistoryArchiverFactory() provider.CustomHistoryArchiverFactory {
	return provider.CustomHistoryArchiverFactoryFunc(func(params provider.NewCustomHistoryArchiverParams) (archiver.HistoryArchiver, error) {
		if params.Scheme != Scheme {
			return nil, provider.ErrUnknownScheme
		}
		cfg, err := ParseConfig(params.Configs)
		if err != nil {
			return nil, err
		}
		return newHistoryArchiver(params.ExecutionManager, params.Logger, params.MetricsHandler, cfg)
	})
}

// NewVisibilityArchiverFactory returns a CustomVisibilityArchiverFactory that handles
// the "minio" scheme. For any other scheme it returns ErrUnknownScheme.
func NewVisibilityArchiverFactory() provider.CustomVisibilityArchiverFactory {
	return provider.CustomVisibilityArchiverFactoryFunc(func(params provider.NewCustomVisibilityArchiverParams) (archiver.VisibilityArchiver, error) {
		if params.Scheme != Scheme {
			return nil, provider.ErrUnknownScheme
		}
		cfg, err := ParseConfig(params.Configs)
		if err != nil {
			return nil, err
		}
		return newVisibilityArchiver(params.Logger, params.MetricsHandler, cfg)
	})
}
