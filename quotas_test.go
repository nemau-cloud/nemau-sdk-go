package nemau

import (
	"context"
	"net/http"
	"testing"
)

const quotaBody = `{"quotas":[
	{"scope":"tenant","dimensions":[
		{"name":"cores","unit":"count","used":95,"reserved":0,"limit":100},
		{"name":"ram","unit":"MB","used":0,"reserved":0,"limit":51200}
	]},
	{"scope":"project","project_id":"p-1","dimensions":[
		{"name":"cores","unit":"count","used":8,"reserved":4,"limit":32},
		{"name":"objectStorage","unit":"bytes","used":4096,"reserved":0,"limit":107374182400}
	]}
]}`

func TestQuotasGet(t *testing.T) {
	var path string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(quotaBody))
	}))

	quotas, err := c.Quotas.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if path != "/api/v1/quotas" {
		t.Errorf("path = %q", path)
	}
	if len(quotas.Quotas) != 2 {
		t.Fatalf("expected both scopes, got %d", len(quotas.Quotas))
	}

	project := quotas.Scope(QuotaScopeProject)
	if project == nil || project.ProjectID != "p-1" {
		t.Fatalf("project scope = %+v", project)
	}
	cores := project.Dimension(QuotaCores)
	if cores == nil || cores.Used != 8 || cores.Reserved != 4 || cores.Limit != 32 {
		t.Fatalf("project cores = %+v", cores)
	}
	// Reservations count: 32 - 8 - 4, not 32 - 8.
	if cores.Available() != 20 {
		t.Errorf("Available() = %d, want 20", cores.Available())
	}
	if quotas.Scope("nowhere") != nil || project.Dimension("nothing") != nil {
		t.Error("an absent scope or dimension must read as nil, not as zero capacity")
	}
}

// A write needs room in both scopes, so the scope with the least headroom
// decides, even when its limit is larger.
func TestQuotasBindingScope(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quotaBody))
	}))
	quotas, err := c.Quotas.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	cores := quotas.Binding(QuotaCores)
	if cores == nil || cores.Limit != 100 {
		t.Fatalf("Binding(cores) = %+v, want the organization scope with 5 left", cores)
	}
	if cores.Available() != 5 {
		t.Errorf("Available() = %d, want 5", cores.Available())
	}
	// Only one scope reports it; that one binds.
	if store := quotas.Binding(QuotaObjectStorage); store == nil || store.Limit != 107374182400 {
		t.Fatalf("Binding(objectStorage) = %+v", store)
	}
	if quotas.Binding("nothing") != nil {
		t.Error("a dimension nobody reports must read as nil")
	}
}

// An exhausted dimension reports zero headroom, not a negative value.
func TestQuotaAvailableNeverGoesNegative(t *testing.T) {
	d := QuotaDimension{Used: 40, Reserved: 5, Limit: 32}
	if d.Available() != 0 {
		t.Errorf("Available() = %d, want 0", d.Available())
	}
}
