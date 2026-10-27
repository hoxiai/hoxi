package app

import (
	"log/slog"

	"github.com/hoxiai/hoxi/internal/hoxipaths"
	"github.com/hoxiai/hoxi/internal/reclaim"
)

// openReclaimLedger opens the shared record of OpenCode daemons, temporary
// Chrome processes, profiles and plugin overlays. Without it Hoxi still runs;
// resources are then only cleaned up on a normal shutdown.
func openReclaimLedger(logger *slog.Logger) *reclaim.Ledger {
	path := hoxipaths.ReclaimLedgerPath()
	if path == "" {
		return nil
	}
	ledger, err := reclaim.Open(path, logger)
	if err != nil {
		logger.Warn("leftover reclaim disabled", "error", err)
		return nil
	}
	return ledger
}

// sweepReclaimLedger cleans up what exited Hoxi processes recorded but never
// released. Records of running processes, this one included, are left alone.
func sweepReclaimLedger(ledger *reclaim.Ledger, logger *slog.Logger) {
	if ledger == nil {
		return
	}
	r := ledger.Sweep()
	if r.Processes > 0 || r.Dirs > 0 || r.Skipped > 0 || r.Pending > 0 {
		logger.Info("reclaimed leftovers of exited Hoxi processes",
			"processes", r.Processes, "dirs", r.Dirs, "skipped", r.Skipped, "pending", r.Pending)
	}
}
