package services

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// ProviderHTTPError is an answer from an AI provider that was not a success. It
// keeps the status code so a caller can decide whether another model is worth trying.
type ProviderHTTPError struct {
	Status  int
	Message string
}

func (e *ProviderHTTPError) Error() string { return e.Message }

// ProviderErrorClass says what a failed model call means for a combo.
type ProviderErrorClass string

const (
	// ErrorRetryable: another model may well succeed (rate limit, provider outage, timeout, network).
	ErrorRetryable ProviderErrorClass = "retryable"
	// ErrorFatal: the request or the key is wrong. Another model may still work, but this one
	// will keep failing, so it should not be retried or counted as an outage.
	ErrorFatal ProviderErrorClass = "fatal"
)

// ClassifyProviderError sorts a failed provider call. Anything that does not clearly
// look like a bad request or a bad key is treated as retryable: for a combo, trying the
// next model is cheaper than giving up.
func ClassifyProviderError(err error) ProviderErrorClass {
	if err == nil {
		return ErrorRetryable
	}
	var httpErr *ProviderHTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.Status == http.StatusTooManyRequests,
			httpErr.Status == http.StatusRequestTimeout,
			httpErr.Status >= 500:
			return ErrorRetryable
		case httpErr.Status == http.StatusBadRequest,
			httpErr.Status == http.StatusUnauthorized,
			httpErr.Status == http.StatusForbidden,
			httpErr.Status == http.StatusNotFound,
			httpErr.Status == http.StatusUnprocessableEntity:
			return ErrorFatal
		}
		return ErrorRetryable
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) {
		return ErrorRetryable
	}
	// An empty or unreadable answer from a provider that said 200 is a provider problem, not a request problem.
	return ErrorRetryable
}
