package booking_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"neon/domain"
	"neon/internal/infrastructure/memory"
	"neon/internal/workflow/booking"
)

type workflowSuite struct {
	testsuite.WorkflowTestSuite
	seats *memory.SeatRepository
}

func newSuite(t *testing.T) (*workflowSuite, *testsuite.TestWorkflowEnvironment) {
	return newSuiteWithRNG(t, &alwaysSucceedRNG{})
}

func newSuiteWithRNG(t *testing.T, rng booking.PaymentRNG) (*workflowSuite, *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	s := &workflowSuite{seats: memory.NewSeatRepository()}
	flights := memory.NewFlightRepository()
	require.NoError(t, memory.Seed(flights, s.seats, memory.DefaultSeedConfig()))

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(booking.BookingWorkflow)
	env.RegisterActivity(&booking.Activities{Seats: s.seats, PaymentRNG: rng})
	return s, env
}

type alwaysSucceedRNG struct{}

func (alwaysSucceedRNG) Float64() float64 { return 1 }

type alwaysFailRNG struct{}

func (alwaysFailRNG) Float64() float64 { return 0 }

func scheduleUpdateSeats(t *testing.T, env *testsuite.TestWorkflowEnvironment, delay time.Duration, seatIDs []string, assertFn func(booking.StatusResponse)) {
	t.Helper()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(booking.UpdateUpdateSeats, fmt.Sprintf("update-%d", time.Now().UnixNano()), &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				require.NoError(t, err)
			},
			OnComplete: func(result interface{}, err error) {
				require.NoError(t, err)
				if assertFn != nil {
					assertFn(result.(booking.StatusResponse))
				}
			},
		}, booking.UpdateSeatsRequest{SeatIDs: seatIDs})
	}, delay)
}

func scheduleUpdateSeatsExpectError(t *testing.T, env *testsuite.TestWorkflowEnvironment, delay time.Duration, seatIDs []string) {
	t.Helper()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(booking.UpdateUpdateSeats, fmt.Sprintf("update-err-%d", time.Now().UnixNano()), &testsuite.TestUpdateCallback{
			OnComplete: func(_ interface{}, err error) {
				require.Error(t, err)
			},
		}, booking.UpdateSeatsRequest{SeatIDs: seatIDs})
	}, delay)
}

func scheduleCancel(t *testing.T, env *testsuite.TestWorkflowEnvironment, delay time.Duration, assertFn func(booking.StatusResponse)) {
	t.Helper()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(booking.UpdateCancelOrder, fmt.Sprintf("cancel-%d", time.Now().UnixNano()), &testsuite.TestUpdateCallback{
			OnReject: func(err error) {
				require.NoError(t, err)
			},
			OnComplete: func(result interface{}, err error) {
				require.NoError(t, err)
				if assertFn != nil {
					assertFn(result.(booking.StatusResponse))
				}
			},
		})
	}, delay)
}

func executeBooking(env *testsuite.TestWorkflowEnvironment, orderID, flightID string, hold time.Duration) {
	env.ExecuteWorkflow(booking.BookingWorkflow, booking.WorkflowInput{
		OrderID:      orderID,
		FlightID:     flightID,
		HoldDuration: hold,
	})
}

func queryStatus(t *testing.T, env *testsuite.TestWorkflowEnvironment) booking.StatusResponse {
	t.Helper()
	value, err := env.QueryWorkflow(booking.QueryGetStatus)
	require.NoError(t, err)
	var resp booking.StatusResponse
	require.NoError(t, value.Get(&resp))
	return resp
}

// U-B0: New order on 101 — Timer ≈15m immediately (flight selected / workflow started)
func TestU_B0_TimerStartsOnWorkflowStart(t *testing.T) {
	_, env := newSuite(t)
	hold := 15 * time.Minute

	env.RegisterDelayedCallback(func() {
		status := queryStatus(t, env)
		require.Equal(t, domain.OrderStatusCreated, status.Status)
		require.InDelta(t, hold.Seconds(), float64(status.TimerRemainingSeconds), 2)
	}, time.Millisecond)
	scheduleUpdateSeats(t, env, 2*time.Millisecond, []string{"1A"}, nil)
	scheduleCancel(t, env, 2*time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-B1: New order on 101 — First UpdateSeats [1A] — SEATS_HELD; timer refreshes to ≈15m
func TestU_B1_FirstSeatUpdateStartsTimer(t *testing.T) {
	_, env := newSuite(t)
	hold := 15 * time.Minute

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, func(resp booking.StatusResponse) {
		require.Equal(t, domain.OrderStatusSeatsHeld, resp.Status)
		require.Equal(t, []string{"1A"}, resp.HeldSeatIDs)
		require.InDelta(t, hold.Seconds(), float64(resp.TimerRemainingSeconds), 2)
	})
	scheduleCancel(t, env, time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-B2: Holding 1A; 8m elapsed — UpdateSeats [1A,1B] — Timer ≈15m
func TestU_B2_SeatChangeResetsTimer(t *testing.T) {
	_, env := newSuite(t)
	hold := 15 * time.Minute

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	scheduleUpdateSeats(t, env, 8*time.Minute, []string{"1A", "1B"}, func(resp booking.StatusResponse) {
		require.Equal(t, domain.OrderStatusSeatsHeld, resp.Status)
		require.InDelta(t, hold.Seconds(), float64(resp.TimerRemainingSeconds), 2)
	})
	scheduleCancel(t, env, 8*time.Minute+time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-B3: Holding 1A — UpdateSeats [2A] — 1A released; 2A held
func TestU_B3_SeatSwapReleasesPreviousSeats(t *testing.T) {
	s, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	scheduleUpdateSeats(t, env, 2*time.Millisecond, []string{"2A"}, func(resp booking.StatusResponse) {
		require.Equal(t, []string{"2A"}, resp.HeldSeatIDs)
		list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
		require.NoError(t, err)
		for _, seat := range list {
			switch seat.SeatID {
			case "1A":
				require.Equal(t, domain.SeatStatusAvailable, seat.Status)
			case "2A":
				require.Equal(t, domain.SeatStatusHeld, seat.Status)
				require.Equal(t, "O1", seat.OrderID)
			}
		}
	})
	scheduleCancel(t, env, 500*time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-B4: Holding seats — CancelOrder — CANCELLED; seats free
func TestU_B4_CancelReleasesSeats(t *testing.T) {
	s, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A", "1B"}, nil)
	scheduleCancel(t, env, time.Millisecond, func(resp booking.StatusResponse) {
		require.Equal(t, domain.OrderStatusCancelled, resp.Status)
	})

	executeBooking(env, "O1", memory.Flight1ID, hold)

	list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
	require.NoError(t, err)
	for _, seat := range list {
		if seat.SeatID == "1A" || seat.SeatID == "1B" {
			require.Equal(t, domain.SeatStatusAvailable, seat.Status)
		}
	}
}

// U-B5b: Order created, no seats held — Timer fires — EXPIRED
func TestU_B5b_TimerExpiryWithoutSeats(t *testing.T) {
	_, env := newSuite(t)
	hold := 2 * time.Second

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusExpired, status.Status)
}

// U-B5: Holding 1A — Timer fires — EXPIRED; 1A free
func TestU_B5_TimerExpiryReleasesSeats(t *testing.T) {
	s, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusExpired, status.Status)

	list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
	require.NoError(t, err)
	for _, seat := range list {
		if seat.SeatID == "1A" {
			require.Equal(t, domain.SeatStatusAvailable, seat.Status)
		}
	}
}

// U-B6: O1 holds 1A on 101 — O2 holds 1A on 101 — O2 fails
func TestU_B6_HoldConflictSameFlight(t *testing.T) {
	s, env := newSuite(t)
	hold := 30 * time.Second

	require.NoError(t, s.seats.TryHold(t.Context(), memory.Flight1ID, []string{"1A"}, "O1"))

	scheduleUpdateSeatsExpectError(t, env, 0, []string{"1A"})
	scheduleCancel(t, env, time.Millisecond, nil)
	executeBooking(env, "O2", memory.Flight1ID, hold)
}

// U-B7: O1 holds 1A on 101 — O2 holds 1A on 102 — O2 succeeds
func TestU_B7_IsolatedFlightsAllowSameSeatID(t *testing.T) {
	_, env1 := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env1, 0, []string{"1A"}, nil)
	scheduleCancel(t, env1, time.Millisecond, nil)
	executeBooking(env1, "O1", memory.Flight1ID, hold)

	s2, env2 := newSuite(t)
	scheduleUpdateSeats(t, env2, 0, []string{"1A"}, func(resp booking.StatusResponse) {
		require.Equal(t, domain.OrderStatusSeatsHeld, resp.Status)
		list, err := s2.seats.ListByFlight(t.Context(), memory.Flight2ID)
		require.NoError(t, err)
		for _, seat := range list {
			if seat.SeatID == "1A" {
				require.Equal(t, domain.SeatStatusHeld, seat.Status)
				require.Equal(t, "O2", seat.OrderID)
			}
		}
	})
	scheduleCancel(t, env2, time.Millisecond, nil)
	executeBooking(env2, "O2", memory.Flight2ID, hold)
}

func schedulePayment(t *testing.T, env *testsuite.TestWorkflowEnvironment, delay time.Duration, code string) {
	t.Helper()
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(booking.SignalSubmitPayment, booking.SubmitPaymentRequest{Code: code})
	}, delay)
}

func schedulePaymentExpectQuery(t *testing.T, env *testsuite.TestWorkflowEnvironment, signalDelay, queryDelay time.Duration, code string, assertFn func(booking.StatusResponse)) {
	t.Helper()
	schedulePayment(t, env, signalDelay, code)
	env.RegisterDelayedCallback(func() {
		assertFn(queryStatus(t, env))
	}, queryDelay)
}

// U-C1: SEATS_HELD — Pay success — CONFIRMED; seats BOOKED
func TestU_C1_PaymentSuccessConfirmsSeats(t *testing.T) {
	s, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, time.Millisecond, "12345")

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusConfirmed, status.Status)

	list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
	require.NoError(t, err)
	for _, seat := range list {
		if seat.SeatID == "1A" {
			require.Equal(t, domain.SeatStatusBooked, seat.Status)
		}
	}
}

// U-C2: Fail once — Retry same code success — CONFIRMED
func TestU_C2_PaymentRetryAfterFailure(t *testing.T) {
	_, env := newSuiteWithRNG(t, &seqFailRNG{failUntil: 1})
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, time.Millisecond, "12345")
	schedulePayment(t, env, 2*time.Millisecond, "12345")

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusConfirmed, status.Status)
}

type seqFailRNG struct {
	failUntil int
	calls     int
}

func (r *seqFailRNG) Float64() float64 {
	r.calls++
	if r.calls <= r.failUntil {
		return 0
	}
	return 1
}

// U-C3: 3 failures same code — order transitions to PAYMENT_FAILED; seats released
func TestU_C3_PaymentAttemptsExhausted(t *testing.T) {
	s, env := newSuiteWithRNG(t, alwaysFailRNG{})
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	for i := 0; i < 3; i++ {
		schedulePayment(t, env, time.Duration(i+1)*time.Millisecond, "12345")
	}

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusPaymentFailed, status.Status)

	list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
	require.NoError(t, err)
	for _, seat := range list {
		if seat.SeatID == "1A" {
			require.Equal(t, domain.SeatStatusAvailable, seat.Status)
		}
	}
}

// U-C4: Payment running — Query AWAITING_PAYMENT; timer running
func TestU_C4_AwaitingPaymentWhileValidationRuns(t *testing.T) {
	t.Setenv("PAYMENT_VALIDATION_DELAY", "2s")
	_, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePaymentExpectQuery(t, env, time.Millisecond, 500*time.Millisecond, "12345", func(resp booking.StatusResponse) {
		require.Equal(t, domain.OrderStatusAwaitingPayment, resp.Status)
		require.Greater(t, resp.TimerRemainingSeconds, 0)
	})

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-C5: Code 1234 — Format error; stays SEATS_HELD
func TestU_C5_InvalidPaymentCodeLength(t *testing.T) {
	_, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, time.Millisecond, "1234")
	env.RegisterDelayedCallback(func() {
		status := queryStatus(t, env)
		require.Equal(t, domain.OrderStatusSeatsHeld, status.Status)
		require.NotEmpty(t, status.PaymentEvents)
		require.Equal(t, booking.PaymentEventFormatInvalid, status.PaymentEvents[0].Type)
	}, 2*time.Millisecond)
	scheduleCancel(t, env, 5*time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-C6: Code abcde — Format error
func TestU_C6_InvalidPaymentCodeLetters(t *testing.T) {
	_, env := newSuite(t)
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, time.Millisecond, "abcde")
	env.RegisterDelayedCallback(func() {
		status := queryStatus(t, env)
		require.Equal(t, domain.OrderStatusSeatsHeld, status.Status)
		require.Equal(t, booking.PaymentEventFormatInvalid, status.PaymentEvents[0].Type)
	}, 2*time.Millisecond)
	scheduleCancel(t, env, 5*time.Millisecond, nil)

	executeBooking(env, "O1", memory.Flight1ID, hold)
}

// U-D4: Payment running; timer=0 — Timer branch — EXPIRED; payment rejected; seats free
func TestU_D4_TimerRejectsInFlightPayment(t *testing.T) {
	t.Setenv("PAYMENT_VALIDATION_DELAY", "10s")
	s, env := newSuite(t)
	hold := 2 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, 100*time.Millisecond, "12345")

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusExpired, status.Status)
	foundRejection := false
	for _, ev := range status.PaymentEvents {
		if ev.Type == booking.PaymentEventRejectedByTimer {
			foundRejection = true
		}
	}
	require.True(t, foundRejection, "expected rejected_by_timer payment event")

	list, err := s.seats.ListByFlight(t.Context(), memory.Flight1ID)
	require.NoError(t, err)
	for _, seat := range list {
		if seat.SeatID == "1A" {
			require.Equal(t, domain.SeatStatusAvailable, seat.Status)
		}
	}
}

// U-D5: A different 5-digit code can be used on any retry attempt.
func TestU_D5_DifferentCodeSucceedsOnRetry(t *testing.T) {
	_, env := newSuiteWithRNG(t, &seqFailRNG{failUntil: 1})
	hold := 30 * time.Second

	scheduleUpdateSeats(t, env, 0, []string{"1A"}, nil)
	schedulePayment(t, env, time.Millisecond, "11111")   // fails
	schedulePayment(t, env, 2*time.Millisecond, "22222") // different code, succeeds

	executeBooking(env, "O1", memory.Flight1ID, hold)

	status := queryStatus(t, env)
	require.Equal(t, domain.OrderStatusConfirmed, status.Status)
}
