package booking

import (
	"math/rand"
	"os"
	"strconv"
	"sync"
)

const (
	paymentFailureRate = 0.15
	maxFailuresPerCode = 3
)

// PaymentRNG supplies randomness for simulated payment failures (injectable in tests).
type PaymentRNG interface {
	Float64() float64
}

type defaultPaymentRNG struct{}

func (defaultPaymentRNG) Float64() float64 {
	return rand.Float64()
}

type alwaysFailRNG struct{}

func (alwaysFailRNG) Float64() float64 { return 0 }

type alwaysSucceedRNG struct{}

func (alwaysSucceedRNG) Float64() float64 { return 1 }

// seqPaymentRNG fails until the configured number of calls, then succeeds.
type seqPaymentRNG struct {
	mu        sync.Mutex
	failUntil int
	calls     int
}

func (r *seqPaymentRNG) Float64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.failUntil {
		return 0
	}
	return 1
}

// PaymentRNGFromEnv returns a test RNG when payment test env vars are set.
func PaymentRNGFromEnv() PaymentRNG {
	if os.Getenv("PAYMENT_ALWAYS_FAIL") == "1" {
		return alwaysFailRNG{}
	}
	if os.Getenv("PAYMENT_NEVER_FAIL") == "1" {
		return alwaysSucceedRNG{}
	}
	raw := os.Getenv("PAYMENT_FAIL_UNTIL")
	if raw == "" {
		return defaultPaymentRNG{}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return defaultPaymentRNG{}
	}
	return &seqPaymentRNG{failUntil: n}
}

func isValidPaymentCode(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func simulatePaymentFailure(rng PaymentRNG) bool {
	return rng.Float64() < paymentFailureRate
}
