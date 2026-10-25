package nemau

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// TaskStatus values a task can report.
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusRetrying  = "retrying"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
)

// TaskRef is the 202 answer to a write. It identifies the background task; the
// resource does not exist yet when this returns.
type TaskRef struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Queue   string `json:"queue,omitempty"`
	Message string `json:"message,omitempty"`
	// Replayed is true when the platform recognised the write's Idempotency-Key
	// and returned the task of the earlier identical call instead of queueing
	// another. TaskID is then the original one.
	Replayed bool `json:"-"`
}

// Task is a background operation as the API reports it.
//
// It does not include the task's parameters, retry counters, idempotency key,
// the user who started it, or the provider's raw error. Result carries the
// operation's outputs, and ResourceID names the resource it acted on once one
// exists.
type Task struct {
	ID           string         `json:"id"`
	TaskType     string         `json:"taskType"`
	Status       string         `json:"status"`
	Progress     int            `json:"progress"`
	ResourceID   *string        `json:"resourceId,omitempty"`
	ResourceType *string        `json:"resourceType,omitempty"`
	Result       map[string]any `json:"result,omitempty"`
	Error        *string        `json:"error,omitempty"`
	// ErrorCode classifies a failure the API chose to explain, so a caller can
	// react without reading Error. The only code defined so far is
	// [ErrorCodeResourceInUse]. Nil for any other failure, and always nil on a
	// task that did not fail.
	ErrorCode   *string    `json:"errorCode,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// ErrorCodeResourceInUse marks a delete the platform refused because something
// still depends on the resource: a volume attached to an instance, a subnet on
// a network. Remove the dependant first, then retry.
const ErrorCodeResourceInUse = "resource_in_use"

// Terminal reports whether the task will not change state again.
func (t *Task) Terminal() bool {
	switch t.Status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		return true
	}
	return false
}

// Succeeded reports whether the task completed successfully.
func (t *Task) Succeeded() bool { return t.Status == TaskStatusCompleted }

// ResultString reads a string out of the task result, trying each key in order.
// Task results are untyped maps whose keys depend on the operation, so the
// caller names the keys that can carry the value.
func (t *Task) ResultString(keys ...string) string {
	if t == nil || t.Result == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := t.Result[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// ResultInt reads a number out of the task result, trying each key in order.
// JSON numbers decode as float64; ResultInt converts them to int.
func (t *Task) ResultInt(keys ...string) (int, bool) {
	if t == nil || t.Result == nil {
		return 0, false
	}
	for _, k := range keys {
		switch v := t.Result[k].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

// TasksService reads background tasks.
//
// The API reads one task by id, or the tasks of one resource. It has no listing
// of all tasks and no way to cancel one.
type TasksService struct{ client *Client }

// Get returns one task by id.
func (s *TasksService) Get(ctx context.Context, id string) (*Task, error) {
	var out Task
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/tasks/" + url.PathEscape(id)}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListForResource returns the operations recorded against one resource.
// resourceID is required.
func (s *TasksService) ListForResource(ctx context.Context, resourceID string) ([]Task, error) {
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	err := s.client.do(ctx, request{
		method: http.MethodGet,
		path:   "/tasks",
		query:  url.Values{"resource_id": {resourceID}},
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Tasks, nil
}

// WaitForTask polls a task until it reaches a terminal status.
//
// It returns the terminal task and a nil error for a completed one, and a
// [TaskFailedError] for a failed or cancelled one. The task is returned in both
// cases, and the error carries it too.
//
// Cancellation and deadlines come from ctx; there is no separate timeout
// option.
func (c *Client) WaitForTask(ctx context.Context, taskID string) (*Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("nemau: WaitForTask: empty task id")
	}
	for {
		task, err := c.Tasks.Get(ctx, taskID)
		if err != nil {
			// Right after a write, the task may not be readable yet: the 202 can
			// arrive before every replica has the task. Keep polling instead of
			// failing.
			if IsNotFound(err) {
				if waitErr := sleepCtx(ctx, c.pollInterval); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}
		if task.Terminal() {
			if !task.Succeeded() {
				return task, &TaskFailedError{Task: task}
			}
			return task, nil
		}
		if err := sleepCtx(ctx, c.pollInterval); err != nil {
			return nil, err
		}
	}
}

// WaitFor is the same as [Client.WaitForTask] for a reference returned by a
// write. A nil ref (a synchronous endpoint answered instead) returns nil, nil.
func (c *Client) WaitFor(ctx context.Context, ref *TaskRef) (*Task, error) {
	if ref == nil || ref.TaskID == "" {
		return nil, nil
	}
	return c.WaitForTask(ctx, ref.TaskID)
}

// waitForResourceID runs a write to completion and returns the id of the
// resource it produced.
//
// It reads resourceId first, which the platform sets on any task linked to a
// resource, whatever its type. For operations that do not set it, the id is
// read from the result map, under the keys the caller passes ("instance_id",
// "network_id", ...).
func (c *Client) waitForResourceID(ctx context.Context, ref *TaskRef, keys ...string) (string, *Task, error) {
	task, err := c.WaitFor(ctx, ref)
	if err != nil {
		return "", task, err
	}
	if task == nil {
		return "", nil, fmt.Errorf("nemau: write did not return a task reference")
	}
	if task.ResourceID != nil && *task.ResourceID != "" {
		return *task.ResourceID, task, nil
	}
	if id := task.ResultString(keys...); id != "" {
		return id, task, nil
	}
	return "", task, fmt.Errorf("nemau: task %s completed without a resource id (looked for %v)", task.ID, keys)
}
