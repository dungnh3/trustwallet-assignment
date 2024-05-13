package rest

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/x509"
	"fmt"
	"go.uber.org/zap"
	"io"
	"io/ioutil"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var (
	// Default retry configuration
	defaultRetryWaitMin = 1 * time.Second
	defaultRetryWaitMax = 30 * time.Second
	defaultRetryMax     = 4

	// We need to consume response bodies to maintain http connections, but
	// limit the size we consume to respReadLimit.
	respReadLimit = int64(4096)

	// A regular expression to match the error returned by net/http when the
	// configured number of redirects is exhausted. This error isn't typed
	// specifically, so we resort to matching on the error string.
	redirectsErrorRe = regexp.MustCompile(`stopped after \d+ redirects\z`)

	// A regular expression to match the error returned by net/http when the
	// scheme specified in the URL is invalid. This error isn't typed
	// specifically, so we resort to matching on the error string.
	schemeErrorRe = regexp.MustCompile(`unsupported protocol scheme`)
)

// CheckRetry specifies a policy for handling retries. It is called
// following each request with the response and error values returned by
// the http.Client. If CheckRetry returns false, the Client stops retrying
// and returns the response to the caller. If CheckRetry returns an error,
// that error value is returned in lieu of the error from the request. The
// Client will close any response body when retrying, but if the retry is
// aborted it is up to the CheckRetry callback to properly close any
// response body before returning.
type CheckRetry func(ctx context.Context, resp *http.Response, err error) (bool, error)

// Backoff specifies a policy for how long to wait between retries.
// It is called after a failing request to determine the amount of time
// that should pass before trying again.
type Backoff func(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration

// ErrorHandler is called if retries are expired, containing the last status
// from the http library. If not specified, default behavior for the library is
// to close the body and return an error indicating how many tries were
// attempted. If overriding this, be sure to close the body if needed.
type ErrorHandler func(resp *http.Response, err error, numTries int) (*http.Response, error)

type RetryDoer struct {
	HTTPClient Doer // Internal HTTP client.

	RetryWaitMin time.Duration // Minimum time to wait
	RetryWaitMax time.Duration // Maximum time to wait
	RetryMax     int           // Maximum number of retries

	// CheckRetry specifies the policy for handling retries, and is called
	// after each request. The default policy is DefaultRetryPolicy.
	CheckRetry CheckRetry

	// Backoff specifies the policy for how long to wait between retries
	Backoff Backoff

	// ErrorHandler specifies the custom error handler to use, if any
	ErrorHandler ErrorHandler

	log *zap.Logger
}

type RetryOption func(doer *RetryDoer)

func WithRetryTimes(times int) RetryOption {
	return func(doer *RetryDoer) {
		doer.RetryMax = times
	}
}
func WithRetryWaitMin(wait time.Duration) RetryOption {
	return func(doer *RetryDoer) {
		doer.RetryWaitMin = wait
	}
}
func WithRetryWaitMax(wait time.Duration) RetryOption {
	return func(doer *RetryDoer) {
		doer.RetryWaitMax = wait
	}
}

func WithRetryPolicy(retryPolicy CheckRetry) RetryOption {
	return func(doer *RetryDoer) {
		doer.CheckRetry = retryPolicy
	}
}

func WithRetryBackoff(backoff Backoff) RetryOption {
	return func(doer *RetryDoer) {
		doer.Backoff = backoff
	}
}

// NewRetryDoer creates a new Client with default settings.
func NewRetryDoer(doer Doer, log *zap.Logger, opts ...RetryOption) *RetryDoer {
	if doer == nil {
		doer = defaultClient
	}

	res := &RetryDoer{
		HTTPClient:   doer,
		RetryWaitMin: defaultRetryWaitMin,
		RetryWaitMax: defaultRetryWaitMax,
		RetryMax:     defaultRetryMax,
		CheckRetry:   DefaultRetryPolicy,
		Backoff:      DefaultBackoff,
		log:          log,
	}

	for _, opt := range opts {
		opt(res)
	}
	return res
}

// DefaultRetryPolicy provides a default callback for Client.CheckRetry, which
// will retry on connection errors and server errors.
func DefaultRetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// do not retry on context.Canceled or context.DeadlineExceeded
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	if err != nil {
		if v, ok := err.(*url.Error); ok {
			// Don't retry if the error was due to too many redirects.
			if redirectsErrorRe.MatchString(v.Error()) {
				return false, nil
			}

			// Don't retry if the error was due to an invalid protocol scheme.
			if schemeErrorRe.MatchString(v.Error()) {
				return false, nil
			}

			// Don't retry if the error was due to TLS cert verification failure.
			if _, ok := v.Err.(x509.UnknownAuthorityError); ok {
				return false, nil
			}
		}

		// The error is likely recoverable so retry.
		return true, nil
	}

	// 429 Too Many Requests is recoverable. Sometimes the server puts
	// a Retry-After response header to indicate when the server is
	// available to start processing request from client.
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}

	// Check the response code. We retry on 500-range responses to allow
	// the server time to recover, as 500's are typically not permanent
	// errors and may relate to outages on the server side. This will catch
	// invalid response codes as well, like 0 and 999.
	if resp.StatusCode == 0 || (resp.StatusCode >= 500 && resp.StatusCode != 501) {
		return true, nil
	}

	return false, nil
}

// ErrorPropagatedRetryPolicy is the same as DefaultRetryPolicy, except it
// propagates errors back instead of returning nil. This allows you to inspect
// why it decided to retry or not.
func ErrorPropagatedRetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// do not retry on context.Canceled or context.DeadlineExceeded
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	if err != nil {
		if v, ok := err.(*url.Error); ok {
			// Don't retry if the error was due to too many redirects.
			if redirectsErrorRe.MatchString(v.Error()) {
				return false, v
			}

			// Don't retry if the error was due to an invalid protocol scheme.
			if schemeErrorRe.MatchString(v.Error()) {
				return false, v
			}

			// Don't retry if the error was due to TLS cert verification failure.
			if _, ok := v.Err.(x509.UnknownAuthorityError); ok {
				return false, v
			}
		}

		// The error is likely recoverable so retry.
		return true, nil
	}

	// Check the response code. We retry on 500-range responses to allow
	// the server time to recover, as 500's are typically not permanent
	// errors and may relate to outages on the server side. This will catch
	// invalid response codes as well, like 0 and 999.
	if resp.StatusCode == 0 || (resp.StatusCode >= 500 && resp.StatusCode != 501) {
		return true, fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	return false, nil
}

// DefaultBackoff provides a default callback for Client.Backoff which
// will perform exponential backoff based on the attempt number and limited
// by the provided minimum and maximum durations.
//
// It also tries to parse Retry-After response header when a http.StatusTooManyRequests
// (HTTP Code 429) is found in the resp parameter. Hence it will return the number of
// seconds the server states it may be ready to process more requests from this client.
func DefaultBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	if resp != nil {
		if resp.StatusCode == http.StatusTooManyRequests {
			if s, ok := resp.Header["Retry-After"]; ok {
				if sleep, err := strconv.ParseInt(s[0], 10, 64); err == nil {
					return time.Second * time.Duration(sleep)
				}
			}
		}
	}

	mult := math.Pow(2, float64(attemptNum)) * float64(min)
	sleep := time.Duration(mult)
	if float64(sleep) != mult || sleep > max {
		sleep = max
	}
	return sleep
}

func randomFloat() (float64, error) {
	maxInt := ^int64(0)
	randed, err := crand.Int(crand.Reader, big.NewInt(maxInt))
	if err != nil {
		return 0, err
	}
	randedF := float64(randed.Int64()) / float64(maxInt)

	return randedF, nil
}

// LinearJitterBackoff provides a callback for Client.Backoff which will
// perform linear backoff based on the attempt number and with jitter to
// prevent a thundering herd.
//
// min and max here are *not* absolute values. The number to be multiplied by
// the attempt number will be chosen at random from between them, thus they are
// bounding the jitter.
//
// For instance:
// * To get strictly linear backoff of one second increasing each retry, set
// both to one second (1s, 2s, 3s, 4s, ...)
// * To get a small amount of jitter centered around one second increasing each
// retry, set to around one second, such as a min of 800ms and max of 1200ms
// (892ms, 2102ms, 2945ms, 4312ms, ...)
// * To get extreme jitter, set to a very wide spread, such as a min of 100ms
// and a max of 20s (15382ms, 292ms, 51321ms, 35234ms, ...)
func LinearJitterBackoff(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
	// attemptNum always starts at zero but we want to start at 1 for multiplication
	attemptNum++

	if max <= min {
		// Unclear what to do here, or they are the same, so return min *
		// attemptNum
		return min * time.Duration(attemptNum)
	}

	randedF, err := randomFloat()
	if err != nil {
		return min * time.Duration(attemptNum)
	}
	// Seed rand; doing this every time is fine
	// Pick a random number that lies somewhere between the min and max and
	// multiply by the attemptNum. attemptNum starts at zero so we always
	// increment here. We first get a random percentage, then apply that to the
	// difference between min and max, and add to min.
	jitter := randedF * float64(max-min)
	jitterMin := int64(jitter) + int64(min)
	return time.Duration(jitterMin * int64(attemptNum))
}

// ReaderFunc is the type of function that can be given natively to NewRequest
type ReaderFunc func() (io.Reader, error)

var _ Doer = &RetryDoer{}

// Request wraps the metadata needed to create HTTP requests.
type Request struct {
	// body is a seekable reader over the request body payload. This is
	// used to rewind the request data in between retries.
	body ReaderFunc

	// Embed an HTTP request directly. This makes a *Request act exactly
	// like an *http.Request so that all meta methods are supported.
	*http.Request
}

func (req *Request) rewind() error {
	if req.body == nil {
		return nil
	}
	body, err := req.body()
	if err != nil {
		return err
	}
	if c, ok := body.(io.ReadCloser); ok {
		req.Body = c
	} else {
		req.Body = ioutil.NopCloser(body)
	}
	return nil
}

func getBodyReaderAndContentLength(rawBody interface{}) (ReaderFunc, int64, error) {
	var bodyReader ReaderFunc
	var contentLength int64

	switch body := rawBody.(type) {
	// Read all in so we can reset
	case io.Reader:
		buf, err := ioutil.ReadAll(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = func() (io.Reader, error) {
			return bytes.NewReader(buf), nil
		}
		contentLength = int64(len(buf))

	// No body provided, nothing to do
	case nil:

	// Unrecognized type
	default:
		return nil, 0, fmt.Errorf("cannot handle type %T", rawBody)
	}
	return bodyReader, contentLength, nil
}

// FromRequest wraps an http.Request in a retryablehttp.Request
func FromRequest(r *http.Request) (*Request, error) {
	bodyReader, _, err := getBodyReaderAndContentLength(r.Body)
	if err != nil {
		return nil, err
	}
	// Could assert contentLength == r.ContentLength
	return &Request{bodyReader, r}, nil
}

func (c *RetryDoer) Do(req *http.Request) (*http.Response, error) {
	re, err := FromRequest(req)
	if err != nil {
		return nil, err
	}
	return c.DoCustom(re)
}

// Try to read the response body so we can reuse this connection.
func (c *RetryDoer) drainBody(body io.ReadCloser) error {
	defer body.Close()
	_, err := io.Copy(ioutil.Discard, io.LimitReader(body, respReadLimit))
	return err
}

// DoCustom wraps calling an HTTP method with retries.
func (c *RetryDoer) DoCustom(req *Request) (*http.Response, error) {
	c.log.Info("performing request", zap.String("method", req.Method), zap.String("url", req.URL.String()))

	var resp *http.Response
	var attempt int
	var shouldRetry bool
	var doErr, checkErr error

	for i := 0; ; i++ {
		attempt++

		var code int // HTTP response code

		// Always rewind the request body when non-nil.
		if err := req.rewind(); err != nil {
			return resp, err
		}

		// Attempt the request
		resp, doErr = c.HTTPClient.Do(req.Request)
		if resp != nil {
			code = resp.StatusCode
		}

		// Check if we should continue with retries.
		shouldRetry, checkErr = c.CheckRetry(req.Context(), resp, doErr)
		if doErr != nil {
			c.log.Error("request failed", zap.String("method", req.Method), zap.String("url", req.URL.String()), zap.Error(doErr))
		}

		if !shouldRetry {
			break
		}

		// We do this before drainBody because there's no need for the I/O if
		// we're breaking out
		remain := c.RetryMax - i
		if remain <= 0 {
			break
		}

		// We're going to retry, consume any response to reuse the connection.
		if doErr == nil {
			err := c.drainBody(resp.Body)
			if err != nil {
				c.log.Error("error reading response body", zap.Error(err))
			}
		}

		wait := c.Backoff(c.RetryWaitMin, c.RetryWaitMax, i, resp)
		desc := fmt.Sprintf("%s %s", req.Method, req.URL)
		if code > 0 {
			desc = fmt.Sprintf("%s (status: %d)", desc, code)
		}

		c.log.Info("retrying request", zap.String("request", desc), zap.String("timeout", wait.String()), zap.Int("remaining", remain))
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}

		// Make shallow copy of http Request so that we can modify its body
		// without racing against the closeBody call in persistConn.writeLoop.
		httpreq := *req.Request
		req.Request = &httpreq
	}

	// this is the closest we have to success criteria
	if doErr == nil && checkErr == nil && !shouldRetry {
		return resp, nil
	}

	err := doErr
	if checkErr != nil {
		err = checkErr
	}

	if c.ErrorHandler != nil {
		return c.ErrorHandler(resp, err, attempt)
	}

	// By default, we close the response body and return an error without
	// returning the response
	if resp != nil {
		err := c.drainBody(resp.Body)
		if err != nil {
			c.log.Error("error reading response body", zap.Error(err))
		}
	}

	// this means CheckRetry thought the request was a failure, but didn't
	// communicate why
	if err == nil {
		return nil, fmt.Errorf("%s %s giving up after %d attempt(s)",
			req.Method, req.URL, attempt)
	}

	return nil, fmt.Errorf("%s %s giving up after %d attempt(s): %w",
		req.Method, req.URL, attempt, err)
}
