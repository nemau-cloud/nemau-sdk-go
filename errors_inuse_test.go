package nemau

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestIsResourceInUse(t *testing.T) {
	msg := "detach volume(s) data before deleting the instance"
	code := ErrorCodeResourceInUse
	other := "something_else"

	inUse := &TaskFailedError{Task: &Task{ID: "task-1", Status: TaskStatusFailed, Error: &msg, ErrorCode: &code}}
	if !IsResourceInUse(inUse) {
		t.Fatal("a task failed with resource_in_use is not reported as such")
	}
	if !IsResourceInUse(fmt.Errorf("delete instance: %w", inUse)) {
		t.Error("a wrapped resource_in_use error is not recognised")
	}
	if !errors.As(inUse, new(*TaskFailedError)) || inUse.Error() == "" {
		t.Error("the error stops being a TaskFailedError")
	}

	for name, err := range map[string]error{
		"nil":                nil,
		"no code":            &TaskFailedError{Task: &Task{Status: TaskStatusFailed, Error: &msg}},
		"another code":       &TaskFailedError{Task: &Task{Status: TaskStatusFailed, ErrorCode: &other}},
		"no task":            &TaskFailedError{},
		"not a task failure": &APIError{StatusCode: 409, Message: msg},
	} {
		if IsResourceInUse(err) {
			t.Errorf("%s: reported as resource in use", name)
		}
	}
}

// The JSON the API returns: errorCode next to error.
func TestTaskDecodesErrorCode(t *testing.T) {
	var task Task
	raw := `{"id":"t1","taskType":"compute:delete","status":"failed","progress":0,` +
		`"error":"detach volume(s) data before deleting the instance","errorCode":"resource_in_use",` +
		`"createdAt":"2026-10-01T10:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &task); err != nil {
		t.Fatal(err)
	}
	if task.ErrorCode == nil || *task.ErrorCode != ErrorCodeResourceInUse {
		t.Fatalf("ErrorCode = %v, want %q", task.ErrorCode, ErrorCodeResourceInUse)
	}
}
