package domain

// OrderStatus represents the booking order lifecycle state.
type OrderStatus string

const (
	OrderStatusCreated          OrderStatus = "CREATED"
	OrderStatusSeatsHeld        OrderStatus = "SEATS_HELD"
	OrderStatusAwaitingPayment  OrderStatus = "AWAITING_PAYMENT"
	OrderStatusConfirmed        OrderStatus = "CONFIRMED"
	OrderStatusExpired          OrderStatus = "EXPIRED"
	OrderStatusCancelled        OrderStatus = "CANCELLED"
	OrderStatusPaymentFailed    OrderStatus = "PAYMENT_FAILED"
)

// IsTerminal reports whether the order cannot accept further changes.
func (s OrderStatus) IsTerminal() bool {
	switch s {
	case OrderStatusConfirmed, OrderStatusExpired, OrderStatusCancelled, OrderStatusPaymentFailed:
		return true
	default:
		return false
	}
}
