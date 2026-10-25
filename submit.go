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

// submitIssuingSecret performs a write whose 202 carries a one-time secret, such
// as a database password. It sends no Idempotency-Key: the platform refuses one
// on these routes (400), because a retried request would be answered 422 and
// the secret of the first call would be lost. For the same reason the call is
// not retried after a transport failure or a 429 (see do): the repeat could
// create a second database. A caller that loses the answer reads the resource
// or rotates the secret instead.
func (c *Client) submitIssuingSecret(ctx context.Context, method, path string, body any, out any) error {
	return c.do(ctx, request{method: method, path: path, body: body}, out)
}
