package nemau

import (
	"context"
	"fmt"
	"net/url"
)

// submit performs a write and decodes the 202 TaskResponse.
//
// Every write endpoint on this API answers with the same envelope, so all the
// services use this method. A response without a task id is an error: the
// request did not reach an asynchronous write, and an empty reference would
// make WaitForTask wait forever.
//
// Each call is one logical write: it sends one Idempotency-Key (the caller's,
// see [WithIdempotencyKey], or a fresh UUID v4) and keeps it across the retries
// of do, so a 429, a 503 or a lost response cannot queue the work twice.
func (c *Client) submit(ctx context.Context, method, path string, query url.Values, body any) (*TaskRef, error) {
	key, err := idempotencyKeyFor(ctx)
	if err != nil {
		return nil, err
	}
	var ref TaskRef
	err = c.do(ctx, request{
		method: method, path: path, query: query, body: body,
		idempotencyKey: key, replayed: &ref.Replayed,
	}, &ref)
	if err != nil {
		return nil, err
	}
	if ref.TaskID == "" {
		return nil, fmt.Errorf("nemau: %s %s: response carried no task id", method, path)
	}
	return &ref, nil
}
