package altmount

import "strings"

func Reconcile(input ReconcileInput) Phase {
	if input.Queue == nil {
		if input.QueueNotFound && input.DeleteConfirmed {
			return PhaseRemoved
		}
		return PhaseUnknown
	}
	item := input.Queue
	switch item.Status {
	case StatusPending:
		return PhaseQueued
	case StatusPaused:
		return PhasePaused
	case StatusProcessing:
		if input.Output.Mapped && input.Output.Exists && input.Output.Readable {
			return PhaseStreamingReady
		}
		return PhaseProcessing
	case StatusCompleted:
		if input.Output.Mapped && input.Output.Exists && input.Output.Readable && input.Output.InventoryComplete {
			return PhaseImportComplete
		}
		return PhaseNeedsAttention
	case StatusFailed:
		if input.CancelRequested && item.ErrorMessage != nil && strings.EqualFold(strings.TrimSpace(*item.ErrorMessage), "Processing cancelled by user request") {
			return PhaseCancelled
		}
		return PhaseFailed
	case StatusFallback:
		return PhaseNeedsAttention
	default:
		return PhaseUnknown
	}
}
