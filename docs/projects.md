# Organizations and projects

The organization comes from the credential, and a call cannot leave it. The
project a request acts on is, in this order:

1. the project set with `nemau.WithProject`, or with `ForProject` on an
   existing client;
2. the value of `NEMAU_PROJECT_ID`;
3. the credential's own project: the active project of the access key's user,
   or the pinned project of a service account.

When a project is selected, the client sends its id in the `X-Project-Id`
header (`nemau.ProjectScopeHeader`). The selection is not stored on the
platform, so two clients built from the same credential can target two projects
at the same time. A service account pinned to a project cannot leave it: the
API answers `403`.

## Selecting a project

```go
client, err := nemau.NewClient(nemau.WithProject(projectID))
if err != nil {
	return err
}
```

`ForProject` returns a copy of a client scoped to another project. The original
client is left unchanged, and calls already running on it keep their project.
The copy shares the HTTP client and the credential.

```go
staging := client.ForProject(stagingID)
prod := client.ForProject(prodID)
```

`client.Project()` returns the selected project id, or an empty string when the
credential's own project applies. `ForProject("")` returns a client that uses
the credential's project.

## Listing projects

`client.Projects.List` returns the projects of the credential's organization:

```go
list, err := client.Projects.List(ctx)
if err != nil {
	return err
}
for _, p := range list.Projects {
	fmt.Println(p.ID, p.Name, p.Slug, p.Region, p.Ready())
}
```

- `Project.Ready()` is true when the project can receive resources. A project
  that is inactive or still provisioning is listed but refuses every write.
- `Project.Current` marks the project the listing request was scoped to.
- `ProjectList.CurrentProjectID` is the project the request resolved to. It can
  name a project absent from `Projects`, for example when the credential points
  at a deleted project.

`client.Projects.Get` accepts an id, a name or a slug. When a value matches
both an id and a name, the id wins. An unknown value returns a `404`
`*nemau.APIError`.

```go
p, err := client.Projects.Get(ctx, "staging")
if err != nil {
	return err
}
staging := client.ForProject(p.ID)
```

`client.Projects.Current` returns the project the credential resolves to, or
`nil` when it resolves to none, as for an organization-wide service account
with no project selected.

Projects are created and deleted in the web console. The SDK only lists them.
