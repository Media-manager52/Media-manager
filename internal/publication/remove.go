package publication

import (
	"context"
	"time"

	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (service *Service) HandleDisplacedRemoval(ctx context.Context, event postgres.OutboxEvent) error {
	link, err := service.Store.LoadDisplacedLink(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if link.Removed {
		return nil
	}
	for _, companion := range link.Companions {
		if err := library.RemoveSymlink(link.DestinationRoot, companion.DestinationRelative, companion.ExpectedTarget); err != nil {
			return err
		}
	}
	if err := library.RemoveSymlink(link.DestinationRoot, link.DestinationRelative, link.ExpectedTarget); err != nil {
		return err
	}
	return service.Store.RecordDisplacedLinkRemoved(ctx, link, time.Now().UTC())
}
