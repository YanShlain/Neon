package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"neon/domain"
	"neon/internal/workflow/booking"
)

// ReconcileInventory rebuilds in-memory seat holds from running Temporal workflows.
// Call on worker/API startup so a process restart does not leave inventory empty
// while workflows still believe seats are held.
//
// MVP limitation: BOOKED seats from CONFIRMED workflows are not replayed on restart
// because the in-memory store has no durable write path. On restart, a CONFIRMED
// order's seats return to AVAILABLE until the in-memory store is replaced with
// persistent storage (e.g. Postgres with a unique constraint on (flight_id, seat_id)).
func ReconcileInventory(ctx context.Context, c client.Client, seats domain.SeatRepository) error {
	req := &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: booking.Namespace,
		Query:     fmt.Sprintf("WorkflowType = %q AND ExecutionStatus = %q", booking.WorkflowName, "Running"),
	}

	var applied int
	for {
		resp, err := c.ListWorkflow(ctx, req)
		if err != nil {
			return fmt.Errorf("list running workflows: %w", err)
		}

		for _, exec := range resp.Executions {
			orderID := exec.Execution.GetWorkflowId()

			qresp, err := c.QueryWorkflow(ctx, orderID, "", booking.QueryGetStatus)
			if err != nil {
				slog.Warn("reconcile skip query failed", "order_id", orderID, "error", err)
				continue
			}
			var status booking.StatusResponse
			if err := qresp.Get(&status); err != nil {
				slog.Warn("reconcile skip decode failed", "order_id", orderID, "error", err)
				continue
			}
			if status.Status.IsTerminal() || len(status.HeldSeatIDs) == 0 {
				continue
			}
			if err := seats.ApplyHold(ctx, status.FlightID, orderID, status.HeldSeatIDs); err != nil {
				if errors.Is(err, domain.ErrHoldConflict) {
					return fmt.Errorf("reconcile hold conflict order=%s flight=%s seats=%v: %w",
						orderID, status.FlightID, status.HeldSeatIDs, err)
				}
				return fmt.Errorf("reconcile apply hold order=%s: %w", orderID, err)
			}
			applied++
		}

		if len(resp.NextPageToken) == 0 {
			break
		}
		req.NextPageToken = resp.NextPageToken
	}

	slog.Info("hold reconciliation complete", "orders_reconciled", applied)
	return nil
}
