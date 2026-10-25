package nemau

import (
	"context"
	"net/http"
	"testing"
)

func TestProjectHeaderIsSentOnlyWhenSelected(t *testing.T) {
	var seen string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(ProjectScopeHeader)
		_, _ = w.Write([]byte(`{"instances":[]}`))
	})

	// No project: the credential's own scope must apply, so no header at all.
	// Sending an empty one would be read by the API as a blank selector.
	c, _ := newTestClient(t, handler)
	if _, err := c.Compute.ListInstances(context.Background()); err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if seen != "" {
		t.Errorf("header sent without a project selected: %q", seen)
	}

	scoped := c.ForProject("proj-42")
	if _, err := scoped.Compute.ListInstances(context.Background()); err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if seen != "proj-42" {
		t.Errorf("%s = %q, want proj-42", ProjectScopeHeader, seen)
	}
}

func TestForProjectDoesNotMutateTheOriginal(t *testing.T) {
	// A client may be shared between goroutines, so ForProject must return a
	// copy and leave the original unchanged.
	var seen []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(ProjectScopeHeader))
		_, _ = w.Write([]byte(`{"networks":[]}`))
	}))

	staging := c.ForProject("proj-staging")
	prod := c.ForProject("proj-prod")

	for _, client := range []*Client{c, staging, prod, c} {
		if _, err := client.Network.ListNetworks(context.Background()); err != nil {
			t.Fatalf("ListNetworks: %v", err)
		}
	}

	want := []string{"", "proj-staging", "proj-prod", ""}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("call %d sent %q, want %q", i, seen[i], want[i])
		}
	}
	if c.Project() != "" {
		t.Errorf("the original client was mutated: Project() = %q", c.Project())
	}
}

func TestForProjectRebindsServiceHandles(t *testing.T) {
	// The copy's service handles must point at the copy; otherwise calls through
	// scoped.Compute would use the original client's project.
	var seen string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(ProjectScopeHeader)
		_, _ = w.Write([]byte(`{"volumes":[]}`))
	}))

	scoped := c.ForProject("proj-7")
	if _, err := scoped.Storage.ListVolumes(context.Background()); err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if seen != "proj-7" {
		t.Errorf("service handle not rebound: header = %q", seen)
	}
}

func TestProjectFromEnvironment(t *testing.T) {
	t.Setenv(EnvProject, "proj-from-env")
	t.Setenv(EnvAccessKeyID, "id")
	t.Setenv(EnvAccessKeySecret, "secret")

	c, err := NewClient(WithBaseURL("https://example.test"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Project() != "proj-from-env" {
		t.Errorf("Project() = %q", c.Project())
	}

	// An explicit option must beat the environment.
	c, err = NewClient(WithBaseURL("https://example.test"), WithProject("proj-explicit"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.Project() != "proj-explicit" {
		t.Errorf("Project() = %q, want the option to win over the environment", c.Project())
	}
}

func TestProjectsListAndLookup(t *testing.T) {
	body := `{"projects":[
		{"id":"p-1","name":"staging","slug":"staging","region":"fr-occitanie-1",
		 "is_active":true,"provisioning_status":"active","current":false},
		{"id":"p-2","name":"prod","slug":"prod","region":"fr-occitanie-1",
		 "is_active":true,"provisioning_status":"pending","current":true}
	],"count":2,"current_project_id":"p-2"}`

	var path string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	ctx := context.Background()

	list, err := c.Projects.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if path != "/api/v1/projects" {
		t.Errorf("path = %q", path)
	}
	if list.CurrentProjectID != "p-2" || len(list.Projects) != 2 {
		t.Fatalf("list = %+v", list)
	}

	// A pending project is listed but refuses every write; Ready reports that.
	if !list.Projects[0].Ready() {
		t.Error("staging should be ready")
	}
	if list.Projects[1].Ready() {
		t.Error("a project still provisioning is not ready")
	}

	byID, err := c.Projects.Get(ctx, "p-1")
	if err != nil || byID.Name != "staging" {
		t.Fatalf("Get by id = %+v, %v", byID, err)
	}
	byName, err := c.Projects.Get(ctx, "prod")
	if err != nil || byName.ID != "p-2" {
		t.Fatalf("Get by name = %+v, %v", byName, err)
	}
	if _, err := c.Projects.Get(ctx, "absent"); !IsNotFound(err) {
		t.Errorf("Get on an unknown project = %v, want a 404", err)
	}

	current, err := c.Projects.Current(ctx)
	if err != nil || current == nil || current.ID != "p-2" {
		t.Fatalf("Current = %+v, %v", current, err)
	}
}

func TestCurrentIsNilWhenNoProjectResolves(t *testing.T) {
	// An organization-wide service account with no project selected gets nil
	// and no error.
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p-1","name":"a","current":false}],"count":1,"current_project_id":""}`))
	}))
	current, err := c.Projects.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current != nil {
		t.Errorf("Current = %+v, want nil", current)
	}
}
