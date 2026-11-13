package quicksetup

import (
	"context"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/postgres"
)

type plexConnection struct{ client *plex.Client }

func (connection plexConnection) Sections(ctx context.Context) ([]PlexSection, error) {
	sections, err := connection.client.Sections(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PlexSection, 0, len(sections))
	for _, section := range sections {
		item := PlexSection{Key: section.Key, Title: section.Title, Type: section.Type}
		for _, location := range section.Locations {
			item.Locations = append(item.Locations, location.Path)
		}
		result = append(result, item)
	}
	return result, nil
}

func (connection plexConnection) CreateSection(ctx context.Context, kind, name, language, folder string) error {
	return connection.client.CreateSection(ctx, kind, name, language, folder)
}

func (connection plexConnection) Browse(ctx context.Context, path string) ([]string, error) {
	return connection.client.Browse(ctx, path)
}

// OpenPlex opens Plex connections with secrets resolved by resolveSecret.
func OpenPlex(resolveSecret acquisition.SecretResolver) func(context.Context, postgres.Integration) (Plex, error) {
	return func(_ context.Context, integration postgres.Integration) (Plex, error) {
		client, err := acquisition.PlexClient(integration, resolveSecret)
		if err != nil {
			return nil, err
		}
		return plexConnection{client: client}, nil
	}
}
