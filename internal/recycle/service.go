package recycle

import (
	"context"
	"errors"
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
	switch entry.EntryKind {
	case "link":
		if err := library.MoveSymlink(entry.DestinationRoot, entry.OriginalRelative, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
			return err
		}
	case "real_file":
		if entry.ExpectedSize == nil || entry.ExpectedModTimeNS == nil {
			return postgres.ErrConcurrentModification
		}
		if err := library.MoveRegularFile(entry.DestinationRoot, entry.OriginalRelative, entry.RecycleRelative, *entry.ExpectedSize, *entry.ExpectedModTimeNS); err != nil {
			return err
		}
	default:
		return postgres.ErrConcurrentModification
	}
	if err := service.Store.MarkRecycleRetained(ctx, entry.ID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func (service *Service) HandleRestore(ctx context.Context, event postgres.OutboxEvent) error {
	entry, err := service.Store.LoadRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if entry.State != "restore_planned" {
		return nil
	}
	switch entry.EntryKind {
	case "link":
		if err := library.MoveSymlink(entry.DestinationRoot, entry.RecycleRelative, entry.OriginalRelative, entry.ExpectedTarget); err != nil {
			return err
		}
		return service.Store.MarkRecycleRestored(ctx, entry, time.Now().UTC())
	case "real_file":
		if entry.Purpose != "upgrade_displacement" || entry.ExpectedSize == nil || entry.ExpectedModTimeNS == nil {
			return postgres.ErrConcurrentModification
		}
		if err := library.MoveRegularFile(entry.DestinationRoot, entry.RecycleRelative, entry.OriginalRelative, *entry.ExpectedSize, *entry.ExpectedModTimeNS); err != nil {
			if errors.Is(err, library.ErrCollision) || errors.Is(err, library.ErrUnsafePath) {
				return service.Store.FailAdoptedRestore(ctx, entry.ID, "retained path no longer matches the reviewed restore plan", time.Now().UTC())
			}
			return err
		}
		return service.Store.MarkAdoptedRestoreAwaitingPlex(ctx, entry.ID, time.Now().UTC())
	default:
		return postgres.ErrConcurrentModification
	}
}

func (service *Service) HandlePurge(ctx context.Context, event postgres.OutboxEvent) error {
	entry, err := service.Store.LoadRecycleEntry(ctx, event.AggregateID)
	if err != nil {
		return err
	}
	if entry.State != "purge_planned" {
		return nil
	}
	switch entry.EntryKind {
	case "link":
		if err := library.RemoveSymlink(entry.DestinationRoot, entry.RecycleRelative, entry.ExpectedTarget); err != nil {
			return err
		}
	case "real_file":
		if entry.ExpectedSize == nil || entry.ExpectedModTimeNS == nil {
			return postgres.ErrConcurrentModification
		}
		if err := library.RemoveRegularFile(entry.DestinationRoot, entry.RecycleRelative, *entry.ExpectedSize, *entry.ExpectedModTimeNS); err != nil {
			return err
		}
	default:
		return postgres.ErrConcurrentModification
	}
	return service.Store.MarkRecyclePurged(ctx, entry.ID, time.Now().UTC())
}
