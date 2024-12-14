package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"log"
)

// RetryInterceptor is a transport interceptor that retries the request
// when the response status code is 429 (Too Many Requests) or 503 (Service Unavailable).
type RetryInterceptor struct {
	Transport http.RoundTripper
}

// WithMaxAttempt is a helper function that retries the function when the error occurs.
func WithMaxAttempt(ctx context.Context, attempt int, f func() error) error {
	var err error
	for i := 0; i < attempt; i++ {
		err = f()
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to execute function after %d attempts: %w", attempt, err)
}

func (i *RetryInterceptor) RoundTrip(req *http.Request) (*http.Response, error) {

	var resp *http.Response
	var err error
	sleepTime := 100 * time.Millisecond
	attempt := 0

	rerr := WithMaxAttempt(req.Context(), 3, func() error {
		log.Printf("attempt %d", attempt)
		attempt++

		resp, err = i.Transport.RoundTrip(req)
		if err != nil {
			// return nil to suppress retry when unexpected error occurs
			return nil
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			// sleep with jitter
			time.Sleep(sleepTime)
			sleepTime = sleepTime + 2*time.Second
			return fmt.Errorf("http status code %d", resp.StatusCode) // return error to retry
		}
		return nil
	})
	if rerr != nil {
		err = rerr
	}
	if err != nil {
		return nil, fmt.Errorf("failed to send http request - %w", err)
	}
	return resp, nil
}

func main() {
	cli := &http.Client{
		Transport: &RetryInterceptor{
			// http.DefaultTransportを利用しリクエストを送信
			Transport: http.DefaultTransport,
		},
	}
	cli.Get("https://example.com")
}
