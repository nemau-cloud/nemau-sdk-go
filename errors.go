package nemau

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError is a non-2xx response from the API.
//
// Message is the error text from the server. An expected refusal carries a
// specific message ("project quota exceeded for instances: 10/10 in use"); any
// other error carries a generic one. A Message is safe to print.
type APIError struct {
	StatusCode int
	Message    string
	// Reason is the machine-readable code some refusals carry:
	// "permission_denied" on authorization failures, [ReasonSubnetOverlap] on a
	// subnet whose range is taken. Empty otherwise.
	Reason string
	// Code is the stable, machine-readable cause some refusals carry
	// ("credential_limit", "organization_read_only", "invalid_request"), empty
	// otherwise. Branch on it, never on Message.
	Code string
	// Limit and Scope qualify a "credential_limit" refusal: the cap that was
	// reached and what it counts ("bucket" or "project"). Zero and empty when the
	// server did not say.
	Limit int
	Scope string
	// Body is the raw response, truncated. Useful when the server answered
	// something this package could not parse (an HTML error page from a proxy).
	Body string
	// Method and Path locate the call that failed.
	Method string
	Path   string
	// RetryAfter is the delay the server asked for with Retry-After (429 and 503
	// carry one). Zero when absent.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = strings.TrimSpace(e.Body)
	}
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return fmt.Sprintf("nemau: %s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// Retryable reports whether repeating the request could plausibly succeed.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// NotFound reports a 404. Terraform's Read uses it to drop a resource from
// state instead of failing the plan.
func (e *APIError) NotFound() bool { return e.StatusCode == http.StatusNotFound }

// Unauthorized reports a credential problem (401) as opposed to a policy one.
func (e *APIError) Unauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

// Forbidden reports that the credential is valid but not allowed (403). On this
// API that is an IAM policy, a token scope, or a missing organization feature.
func (e *APIError) Forbidden() bool { return e.StatusCode == http.StatusForbidden }

// ReasonSubnetOverlap is the [APIError.Reason] of the 409 refusing a subnet
// whose CIDR overlaps another subnet of the same project. All the subnets of a
// project are routed together, whichever network they belong to, so the
// conflict may sit on another network; the Message names the subnet, its range
// and its network.
const ReasonSubnetOverlap = "subnet_overlap"

// IsSubnetOverlap reports whether err is the API refusing a subnet because its
// range overlaps another subnet of the project.
//
// It matches the refusal of the creation request itself, which is where an
// overlap is caught. A conflicting subnet created by someone else between that
// request and the task running fails the task instead: that surfaces as a
// [TaskFailedError] carrying the same message, without the reason.
func IsSubnetOverlap(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Reason == ReasonSubnetOverlap
}

// IsNotFound reports whether err is a 404 [APIError].
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.NotFound()
}

// IsUnauthorized reports whether err is a 401 [APIError].
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Unauthorized()
}

// IsForbidden reports whether err is a 403 [APIError].
func IsForbidden(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Forbidden()
}

// Stable error codes carried by [APIError.Code].
const (
	// ErrorCodeCredentialLimit: the container already has the maximum number of
	// access keys (HTTP 429). Revoke an unused key, then retry.
	ErrorCodeCredentialLimit = "credential_limit"
	// ErrorCodeOrganizationReadOnly: the organization is in its read-only phase
	// (HTTP 403) and may only hold read-only access keys.
	ErrorCodeOrganizationReadOnly = "organization_read_only"
	// ErrorCodeInvalidRequest: the request body or a parameter was refused (HTTP 400).
	ErrorCodeInvalidRequest = "invalid_request"
)

func asAPIError(err error, target **APIError) bool { return errors.As(err, target) }

func hasAPICode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// IsCredentialLimit reports whether err is a refusal because the container has
// reached its access key limit. Revoking an unused key is the fix; repeating the
// same call cannot succeed.
func IsCredentialLimit(err error) bool { return hasAPICode(err, ErrorCodeCredentialLimit) }

// CredentialLimit returns the cap carried by a credential-limit refusal: how many
// keys are allowed and what that counts ("bucket" or "project"). ok is false when
// err is not such a refusal; limit is 0 and scope empty when the server did not
// say.
func CredentialLimit(err error) (limit int, scope string, ok bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrorCodeCredentialLimit {
		return 0, "", false
	}
	return apiErr.Limit, apiErr.Scope, true
}

// IsOrganizationReadOnly reports whether err is a refusal because the
// organization is in its read-only phase.
func IsOrganizationReadOnly(err error) bool { return hasAPICode(err, ErrorCodeOrganizationReadOnly) }

// TaskFailedError is returned when a task reaches a terminal non-successful
// status. The submitted work did not happen; the platform's own message is in
// the wrapped task.
//
// The API replaces the provider's error on a failed task with a fixed string
// ("the operation failed; contact support with the task id"). The task id is
// part of the error message so that it can be given to support.
type TaskFailedError struct {
	Task *Task
}

func (e *TaskFailedError) Error() string {
	detail := ""
	if e.Task != nil && e.Task.Error != nil && *e.Task.Error != "" {
		detail = ": " + *e.Task.Error
	}
	id, kind, status := "", "", ""
	if e.Task != nil {
		id, kind, status = e.Task.ID, e.Task.TaskType, e.Task.Status
	}
	return fmt.Sprintf("nemau: task %s (%s) %s%s", id, kind, status, detail)
}

// ResourceInUse reports whether the platform refused the operation because a
// dependant still uses the resource (a volume still attached to an instance
// being deleted, a subnet still on a network). Error carries the platform's own
// wording, which names what to remove first.
func (e *TaskFailedError) ResourceInUse() bool {
	return e != nil && e.Task != nil && e.Task.ErrorCode != nil && *e.Task.ErrorCode == ErrorCodeResourceInUse
}

// IsResourceInUse reports whether err is a [TaskFailedError] for a resource that
// still has dependants. The fix is on the caller's side: detach or delete the
// dependant, then retry. Repeating the same call cannot succeed.
func IsResourceInUse(err error) bool {
	var failed *TaskFailedError
	return errors.As(err, &failed) && failed.ResourceInUse()
}

// maxErrorBody caps what an error keeps from a response body. A misrouted
// request can return a whole HTML page; the start of it is enough to identify
// it.
const maxErrorBody = 4096

// decodeError returns an *APIError for a non-2xx response, or nil. It consumes
// the body only in the error case; the caller keeps ownership otherwise.
func decodeError(resp *http.Response) *APIError {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	out := &APIError{
		StatusCode: resp.StatusCode,
		Body:       string(raw),
		Method:     resp.Request.Method,
		Path:       resp.Request.URL.Path,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
	// The API answers errors as {"error": "...", "reason": "..."}. Some responses
	// use "message" instead, so both are read.
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
		Code    string `json:"code"`
		Limit   int    `json:"limit"`
		Scope   string `json:"scope"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		out.Message = payload.Error
		if out.Message == "" {
			out.Message = payload.Message
		}
		out.Reason = payload.Reason
		out.Code = payload.Code
		if payload.Limit > 0 {
			out.Limit = payload.Limit
		}
		out.Scope = payload.Scope
	}
	return out
}

// notFound builds the 404 this package synthesises when a list-and-filter read
// finds nothing. It is a real *APIError so [IsNotFound] and Terraform's Read
// treat it exactly like a 404 the server sent.
func notFound(kind, id, path string) *APIError {
	return &APIError{
		StatusCode: http.StatusNotFound,
		Message:    fmt.Sprintf("%s %q was not found in this project", kind, id),
		Method:     http.MethodGet,
		Path:       apiPrefix + path,
	}
}

// HasReason reports whether err is an [APIError] carrying the given
// machine-readable reason code, such as [ReasonDNSZoneReserved]. Use it instead
// of matching on the message, whose wording may change.
func HasReason(err error, reason string) bool {
	var apiErr *APIError
	return reason != "" && errors.As(err, &apiErr) && apiErr.Reason == reason
}
