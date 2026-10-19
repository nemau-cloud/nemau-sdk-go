package nemau

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// taskSequence serves a create, then a task that becomes terminal on the nth
// poll, as for every asynchronous write.
type taskSequence struct {
	polls    int32
	untilNth int32
	terminal string
	result   string
	resource string
}

func (s *taskSequence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-1","status":"pending"}`))
		return
	}
	n := atomic.AddInt32(&s.polls, 1)
	if n < s.untilNth {
		_, _ = w.Write([]byte(`{"id":"task-1","taskType":"network:create","status":"running","progress":40}`))
		return
	}
	body := `{"id":"task-1","taskType":"network:create","status":"` + s.terminal + `","progress":100`
	if s.resource != "" {
		body += `,"resourceId":"` + s.resource + `","resourceType":"network"`
	}
	if s.result != "" {
		body += `,"result":` + s.result
	}
	if s.terminal == TaskStatusFailed {
		body += `,"error":"the operation failed; contact support with the task id"`
	}
	_, _ = w.Write([]byte(body + `}`))
}

func TestWaitForTaskPollsUntilTerminal(t *testing.T) {
	seq := &taskSequence{untilNth: 3, terminal: TaskStatusCompleted, resource: "net-42"}
	c, _ := newTestClient(t, seq)

	ref, err := c.Network.CreateNetwork(context.Background(), "prod", "")
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	task, err := c.WaitFor(context.Background(), ref)
	if err != nil {
		t.Fatalf("WaitFor: %v", err)
	}
	if !task.Succeeded() || task.Progress != 100 {
		t.Fatalf("unexpected terminal task: %+v", task)
	}
	if seq.polls != 3 {
		t.Errorf("polls = %d, want 3", seq.polls)
	}
}

func TestWaitForTaskReportsFailure(t *testing.T) {
	c, _ := newTestClient(t, &taskSequence{untilNth: 1, terminal: TaskStatusFailed})

	ref, err := c.Network.CreateNetwork(context.Background(), "prod", "")
	if err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	task, err := c.WaitFor(context.Background(), ref)
	var failed *TaskFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *TaskFailedError", err)
	}
	// The task is returned with the error: support needs its id, since the
	// platform hides the provider's message.
	if task == nil || task.ID != "task-1" {
		t.Fatalf("failed task not returned: %+v", task)
	}
	if !strings.Contains(err.Error(), "task-1") {
		t.Errorf("error text omits the task id: %v", err)
	}
}

func TestWaitForResourceIDPrefersResourceID(t *testing.T) {
	// When both are present and differ, resourceId takes precedence over the
	// id in result.
	seq := &taskSequence{untilNth: 1, terminal: TaskStatusCompleted,
		resource: "net-authoritative", result: `{"network_id":"net-from-result"}`}
	c, _ := newTestClient(t, seq)

	ref, _ := c.Network.CreateNetwork(context.Background(), "prod", "")
	id, _, err := c.waitForResourceID(context.Background(), ref, "network_id")
	if err != nil {
		t.Fatalf("waitForResourceID: %v", err)
	}
	if id != "net-authoritative" {
		t.Errorf("id = %q, want net-authoritative", id)
	}
}

func TestWaitForResourceIDFallsBackToResult(t *testing.T) {
	seq := &taskSequence{untilNth: 1, terminal: TaskStatusCompleted, result: `{"volume_id":"vol-9"}`}
	c, _ := newTestClient(t, seq)

	ref, _ := c.Network.CreateNetwork(context.Background(), "prod", "")
	id, _, err := c.waitForResourceID(context.Background(), ref, "volume_id")
	if err != nil {
		t.Fatalf("waitForResourceID: %v", err)
	}
	if id != "vol-9" {
		t.Errorf("id = %q, want vol-9", id)
	}
}

func TestWaitForTaskToleratesInitial404(t *testing.T) {
	// A 202 can arrive before the task is readable, so the poll loop treats an
	// early 404 as "not yet" instead of failing.
	var polls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"task_id":"t","status":"pending"}`))
			return
		}
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"task not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"t","status":"completed","resourceId":"net-1"}`))
	}))

	ref, _ := c.Network.CreateNetwork(context.Background(), "prod", "")
	if _, err := c.WaitFor(context.Background(), ref); err != nil {
		t.Fatalf("WaitFor: %v", err)
	}
}

func TestWaitForTaskHonoursContext(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"t","status":"running"}`))
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WaitForTask(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestListForResourceRequiresResourceID(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"tasks":[{"id":"t1","status":"completed"}]}`))
	}))
	tasks, err := c.Tasks.ListForResource(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("ListForResource: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v", tasks)
	}
	if gotQuery != "resource_id=inst-1" {
		t.Errorf("query = %q", gotQuery)
	}
}
