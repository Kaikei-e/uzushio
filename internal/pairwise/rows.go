package pairwise

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Rows reads a dataset split through a paged JSON rows endpoint: a URL that
// takes a dataset, a configuration, a split, an offset and a length, and
// answers with `{"num_rows_total": n, "rows": [{"row": {…}}, …]}`.
//
// It is here rather than in the command that uses it because paging, retrying
// and refusing to loop for ever are the same wherever the rows come from. What
// the rows mean is the caller's business: Rows hands back the raw objects.
type Rows struct {
	// Endpoint is the rows URL, without a query.
	Endpoint string
	// Dataset, Config and Split name what to read.
	Dataset string
	Config  string
	Split   string
	// Page is how many rows to ask for at a time. Zero is DefaultPage.
	Page int
	// Limit stops after this many rows. Zero reads the split.
	Limit int
	// Client is the HTTP client. Nil is a client with DefaultTimeout.
	Client *http.Client
	// Pause is how long to wait between pages, so a public endpoint is not
	// hammered. Zero is DefaultPause.
	Pause time.Duration
	// Retries is how many times a page is re-fetched after a throttle or a
	// server error before the read fails. Zero is DefaultRetries.
	Retries int
	// Log receives one line per page, for a person watching a long read.
	Log func(string)
}

// The defaults. The page size is the largest a rows endpoint of this shape
// usually serves; the pause and the retry budget are what it takes to read a
// few thousand rows off a shared public service without being throttled off
// it, which is a thing that happens rather than a thing to guard against in
// theory.
const (
	DefaultPage    = 100
	DefaultPause   = 1500 * time.Millisecond
	DefaultRetries = 8
	DefaultTimeout = 120 * time.Second
)

// rowsResponse is the part of a page this package reads.
type rowsResponse struct {
	Total int `json:"num_rows_total"`
	Rows  []struct {
		Row json.RawMessage `json:"row"`
	} `json:"rows"`
}

// All reads the whole split, in order.
func (r Rows) All(ctx context.Context) ([]json.RawMessage, error) {
	page := r.Page
	if page <= 0 {
		page = DefaultPage
	}
	pause := r.Pause
	if pause == 0 {
		pause = DefaultPause
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}

	var out []json.RawMessage
	total := -1
	for offset := 0; total < 0 || offset < total; offset += page {
		if r.Limit > 0 && len(out) >= r.Limit {
			break
		}
		body, err := r.page(ctx, client, offset, page)
		if err != nil {
			return nil, err
		}
		if body.Total > 0 {
			total = body.Total
		}
		if len(body.Rows) == 0 {
			break
		}
		for _, row := range body.Rows {
			out = append(out, row.Row)
		}
		if r.Log != nil {
			r.Log(fmt.Sprintf("read %d of %d rows", len(out), total))
		}
		if err := sleep(ctx, pause); err != nil {
			return nil, err
		}
	}
	if r.Limit > 0 && len(out) > r.Limit {
		out = out[:r.Limit]
	}
	return out, nil
}

// page fetches one page, retrying a throttle or a server error with a linear
// backoff. A 4xx that is not a throttle is not retried: asking the same wrong
// question again is not a strategy.
func (r Rows) page(ctx context.Context, client *http.Client, offset, length int) (rowsResponse, error) {
	retries := r.Retries
	if retries <= 0 {
		retries = DefaultRetries
	}
	query := url.Values{}
	query.Set("dataset", r.Dataset)
	query.Set("config", r.Config)
	query.Set("split", r.Split)
	query.Set("offset", strconv.Itoa(offset))
	query.Set("length", strconv.Itoa(length))
	target := r.Endpoint + "?" + query.Encode()

	var last error
	for attempt := range retries {
		if attempt > 0 {
			if err := sleep(ctx, time.Duration(attempt)*4*time.Second); err != nil {
				return rowsResponse{}, err
			}
			if r.Log != nil {
				r.Log(fmt.Sprintf("retry %d at offset %d: %v", attempt, offset, last))
			}
		}
		body, retryable, err := fetch(ctx, client, target)
		if err == nil {
			return body, nil
		}
		last = err
		if !retryable {
			return rowsResponse{}, err
		}
	}
	return rowsResponse{}, fmt.Errorf("%w: offset %d: %w", ErrCorpus, offset, last)
}

// fetch performs one request and says whether a failure is worth repeating.
func fetch(ctx context.Context, client *http.Client, target string) (rowsResponse, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return rowsResponse{}, false, fmt.Errorf("%w: %w", ErrCorpus, err)
	}
	response, err := client.Do(request)
	if err != nil {
		return rowsResponse{}, true, fmt.Errorf("%w: %w", ErrCorpus, err)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= 500:
		return rowsResponse{}, true, fmt.Errorf("%w: %s", ErrCorpus, response.Status)
	default:
		return rowsResponse{}, false, fmt.Errorf("%w: %s", ErrCorpus, response.Status)
	}
	var body rowsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return rowsResponse{}, true, fmt.Errorf("%w: %w", ErrCorpus, err)
	}
	return body, false, nil
}

// sleep waits, and gives up where the caller has.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
