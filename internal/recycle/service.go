package recycle

import (
	"context"
	"time"

	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/postgres"
)

type Service struct{ Store *postgres.Store }

func (service *Service) HandleMove(ctx context.Context, event postgres.OutboxEvent) error {
	entry, err := service.Store.LoadRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if entry.State != "planned" {
		return nil
	}
	if err := library.MoveSymlink(entry.DestinationRoot, entry.OriginalRelative, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
		return err
	}
	return service.Store.MarkRecycleRetained(ctx, entry.ID, time.Now().UTC())
}

func (service *Service) HandleRestore(ctx context.Context, event postgres.OutboxEvent) error {
	entry, err := service.Store.LoadRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if entry.State != "restore_planned" {
		return nil
	}
	if err := library.MoveSymlink(entry.DestinationRoot, entry.RecycleRelative, entry.OriginalRelative, entry.ExpectedTarget); err != nil {
		return err
	}
	return service.Store.MarkRecycleRestored(ctx, entry, time.Now().UTC())
}

func (service *Service) HandlePurge(ctx context.Context, event postgres.OutboxEvent) error {
	entry, err := service.Store.LoadRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if entry.State != "purge_planned" {
		return nil
	}
	if err := library.RemoveSymlink(entry.DestinationRoot, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
		return err
	}
	return service.Store.MarkRecyclePurged(ctx, entry.ID, time.Now().UTC())
}
