package api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"neon/domain"
	"neon/internal/api/handler"
	"neon/internal/web"
)

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}

// NewRouter registers MVP-A/B/C endpoints and static UI.
func NewRouter(flights domain.FlightRepository, seats domain.SeatRepository, orders handler.OrderService) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())

	staticFS := web.MustFS()
	web.Register(r, staticFS)

	fh := handler.NewFlightHandler(flights, seats)
	oh := handler.NewOrderHandler(orders)
	v1 := r.Group("/api/v1")
	{
		v1.GET("/flights", fh.ListFlights)
		v1.GET("/flights/:flight_id/seats", fh.GetSeatMap)
		v1.POST("/orders", oh.CreateOrder)
		v1.PATCH("/orders/:order_id/seats", oh.UpdateSeats)
		v1.POST("/orders/:order_id/cancel", oh.CancelOrder)
		v1.POST("/orders/:order_id/payment", oh.SubmitPayment)
		v1.GET("/orders/:order_id", oh.GetOrder)
		v1.GET("/orders/:order_id/stream", oh.StreamOrder)
	}
	return r
}
