package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"neon/domain"
	"neon/internal/api/dto"
	"neon/internal/infrastructure/memory"
)

// FlightHandler serves flight catalog and seat map endpoints.
type FlightHandler struct {
	flights domain.FlightRepository
	seats   domain.SeatRepository
}

// NewFlightHandler creates a FlightHandler.
func NewFlightHandler(flights domain.FlightRepository, seats domain.SeatRepository) *FlightHandler {
	return &FlightHandler{flights: flights, seats: seats}
}

// ListFlights handles GET /api/v1/flights.
func (h *FlightHandler) ListFlights(c *gin.Context) {
	ctx := c.Request.Context()
	slog.Info("inbound request", "method", c.Request.Method, "path", c.Request.URL.Path)

	flights, err := h.flights.List(ctx)
	if err != nil {
		slog.Error("list flights failed", "error", err, "exc_info", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	out := make([]dto.FlightResponse, 0, len(flights))
	for _, f := range flights {
		out = append(out, dto.FlightResponse{
			ID:          f.ID,
			DepartureAt: f.DepartureAt,
			Capacity:    f.Capacity,
		})
	}
	c.JSON(http.StatusOK, gin.H{"flights": out})
}

// GetSeatMap handles GET /api/v1/flights/:flight_id/seats.
func (h *FlightHandler) GetSeatMap(c *gin.Context) {
	ctx := c.Request.Context()
	flightID := c.Param("flight_id")
	orderID := c.Query("order_id")
	slog.Info("inbound request",
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"flight_id", flightID,
		"order_id", orderID,
	)

	seats, err := h.seats.ListByFlight(ctx, flightID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "flight not found"})
			return
		}
		slog.Error("list seats failed", "flight_id", flightID, "error", err, "exc_info", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	resp := dto.SeatMapResponse{
		FlightID: flightID,
		Seats:    make([]dto.SeatResponse, 0, len(seats)),
	}
	for _, seat := range seats {
		resp.Seats = append(resp.Seats, dto.SeatResponse{
			SeatID:  seat.SeatID,
			Status:  string(seat.Status),
			OrderID: seat.OrderID,
			IsMine:  orderID != "" && seat.OrderID == orderID,
		})
	}
	c.JSON(http.StatusOK, resp)
}

func isNotFound(err error) bool {
	return errors.Is(err, memory.ErrFlightNotFound)
}
