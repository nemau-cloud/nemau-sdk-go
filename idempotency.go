package nemau

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

// IdempotencyKeyHeader names the request header that makes an asynchronous
// write safe to repeat.
const IdempotencyKeyHeader = "Idempotency-Key"

// ReplayedHeader is the response header the platform sets to "true" when it
// answered a write as a replay of an earlier one with the same key.
const ReplayedHeader = "Idempotent-Replayed"

// ErrorCodeIdempotencyKeyMismatch is the message of the 422 refusing a key that
// was already used for a request with different parameters. See
// [IsIdempotencyKeyMismatch].
const ErrorCodeIdempotencyKeyMismatch = "idempotency_key_mismatch"

// maxIdempotencyKeyLength and idempotencyKeyPattern mirror the server's rule:
// 1 to 128 characters of [A-Za-z0-9._:-].
const maxIdempotencyKeyLength = 128

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

type idempotencyKeyCtx struct{}

// WithIdempotencyKey returns a context that makes the next write use key as its
// Idempotency-Key instead of a generated one.
//
// Every asynchronous write already sends a fresh UUID v4 and reuses it on the
// SDK's own retries, so most callers never need this. Pass one when the retry
// happens outside the SDK: a job that may run twice, a CLI invocation repeated
// after a lost connection. The platform then answers the repeat with the
// original task (see [TaskRef.Replayed]) instead of queueing a second one, for
// 24 hours. Reusing a key with different parameters is refused with a 422,
// see [IsIdempotencyKeyMismatch].
//
// key must be 1 to 128 characters of letters, digits, '.', '_', ':' and '-'; a
// write made with anything else fails before it is sent. The key scopes one
// write: do not share a context carrying it between different writes.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// validIdempotencyKey checks key against the server's rule.
func validIdempotencyKey(key string) error {
	if key == "" || len(key) > maxIdempotencyKeyLength || !idempotencyKeyPattern.MatchString(key) {
		return fmt.Errorf("nemau: invalid idempotency key: want 1 to %d characters of A-Z a-z 0-9 . _ : -", maxIdempotencyKeyLength)
	}
	return nil
}

// idempotencyKeyFor returns the key of one logical write: the caller's, when
// the context carries one, or a new UUID v4.
func idempotencyKeyFor(ctx context.Context) (string, error) {
	if v, ok := ctx.Value(idempotencyKeyCtx{}).(string); ok {
		if err := validIdempotencyKey(v); err != nil {
			return "", err
		}
		return v, nil
	}
	return newUUIDv4()
}

// newUUIDv4 returns a random (version 4) UUID from the system CSPRNG.
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("nemau: generating idempotency key: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:]), nil
}

// IsIdempotencyKeyMismatch reports whether err is the platform refusing a write
// because its Idempotency-Key was already used for a request with different
// parameters (HTTP 422). Repeating the call cannot succeed: use a new key for
// the new request, or send the original parameters again.
func IsIdempotencyKeyMismatch(err error) bool {
	var apiErr *APIError
	return asAPIError(err, &apiErr) && apiErr.StatusCode == 422 &&
		(apiErr.Message == ErrorCodeIdempotencyKeyMismatch || apiErr.Code == ErrorCodeIdempotencyKeyMismatch)
}
