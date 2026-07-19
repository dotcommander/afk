package task

import "errors"

// RetryDisposition names the operator-selected outcome for a failed attempt.
// It is a bounded vocabulary only; AFK does not automatically retry tasks.
type RetryDisposition string

const (
	// RetryDispositionManual opens a new attempt immediately.
	RetryDispositionManual RetryDisposition = "manual"
	// RetryDispositionDeferred returns work to todo until available_at.
	RetryDispositionDeferred RetryDisposition = "deferred"
)

// ErrInvalidRetryDisposition reports an unsupported retry outcome.
var ErrInvalidRetryDisposition = errors.New("retry disposition must be manual or deferred")

// ErrDeferredRetryRequiresAvailableAt reports a deferred retry without a
// future eligibility timestamp.
var ErrDeferredRetryRequiresAvailableAt = errors.New("deferred retry requires available-at")

// ErrDeferredRetryNotFuture reports a deferred retry whose eligibility time
// has already arrived.
var ErrDeferredRetryNotFuture = errors.New("deferred retry available-at must be in the future")

// ErrManualRetryWithAvailableAt reports an ambiguous immediate retry request.
var ErrManualRetryWithAvailableAt = errors.New("manual retry does not accept available-at")

// ParseRetryDisposition returns a known retry disposition. Empty input uses
// the manual default so callers must opt in to deferred scheduling.
func ParseRetryDisposition(value string) (RetryDisposition, error) {
	if value == "" {
		return RetryDispositionManual, nil
	}
	switch RetryDisposition(value) {
	case RetryDispositionManual, RetryDispositionDeferred:
		return RetryDisposition(value), nil
	default:
		return "", ErrInvalidRetryDisposition
	}
}

// ValidateRetryDisposition validates disposition-specific scheduling input
// and returns a canonical UTC eligibility timestamp.
func ValidateRetryDisposition(disposition RetryDisposition, availableAt string) (string, error) {
	canonical, err := CanonicalAvailableAt(availableAt)
	if err != nil {
		return "", err
	}
	switch disposition {
	case RetryDispositionManual:
		if canonical != "" {
			return "", ErrManualRetryWithAvailableAt
		}
	case RetryDispositionDeferred:
		if canonical == "" {
			return "", ErrDeferredRetryRequiresAvailableAt
		}
	default:
		return "", ErrInvalidRetryDisposition
	}
	return canonical, nil
}
