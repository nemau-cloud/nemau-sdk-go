package nemau

import (
	"context"
	"net/http"
)

// Project is a project the credential can act on.
type Project struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Region             string `json:"region"`
	IsActive           bool   `json:"is_active"`
	ProvisioningStatus string `json:"provisioning_status"`
	// Current marks the project the listing request itself was scoped to.
	Current bool `json:"current"`
}

// Ready reports whether the project can receive resources. A project that is
// inactive or still provisioning is listed but refuses every write.
func (p *Project) Ready() bool { return p.IsActive && p.ProvisioningStatus == "active" }

// ProjectList is the answer to a project listing.
type ProjectList struct {
	Projects []Project `json:"projects"`
	Count    int       `json:"count"`
	// CurrentProjectID is the scope the request resolved to. It can name a
	// project absent from Projects, for example when the credential points at a
	// deleted project.
	CurrentProjectID string `json:"current_project_id"`
}

// ProjectsService lists the projects a credential can target.
//
// Projects are created and deleted from the web console, with a console
// session; this service only lists them.
type ProjectsService struct{ client *Client }

// List returns the projects of the credential's organization.
func (s *ProjectsService) List(ctx context.Context) (*ProjectList, error) {
	var out ProjectList
	err := s.client.do(ctx, request{method: http.MethodGet, path: "/orchestration/projects"}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns one project by id or by name, whichever matches.
//
// A name or slug is accepted as well as an id. When a value could match both,
// the id match is returned.
func (s *ProjectsService) Get(ctx context.Context, idOrName string) (*Project, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list.Projects {
		if list.Projects[i].ID == idOrName {
			return &list.Projects[i], nil
		}
	}
	for i := range list.Projects {
		if list.Projects[i].Name == idOrName || list.Projects[i].Slug == idOrName {
			return &list.Projects[i], nil
		}
	}
	return nil, notFound("project", idOrName, "/orchestration/projects")
}

// Current returns the project the credential currently resolves to, or nil when
// it resolves to none, as for an organization-wide service account with no
// project selected.
func (s *ProjectsService) Current(ctx context.Context) (*Project, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list.Projects {
		if list.Projects[i].Current {
			return &list.Projects[i], nil
		}
	}
	return nil, nil
}
