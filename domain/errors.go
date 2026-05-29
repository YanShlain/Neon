package domain

import "errors"

// ErrHoldConflict is returned when a seat is already held by another order.
var ErrHoldConflict = errors.New("seat hold conflict")

// ErrFlightNotFound is returned when no flight matches the given ID.
var ErrFlightNotFound = errors.New("flight not found")

// ErrInvalidConfirm is returned when a seat is not in the expected held state for confirmation.
var ErrInvalidConfirm = errors.New("seat not held by order for confirm")

// ErrInvalidRelease is returned when a seat is not held by the requesting order.
var ErrInvalidRelease = errors.New("seat not held by order")
