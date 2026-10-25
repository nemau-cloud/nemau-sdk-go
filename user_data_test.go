package nemau

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// createBody is a valid create request without user data.
func createBody() CreateInstanceRequest {
	return CreateInstanceRequest{Name: "web", ImageID: "img-1", VCPUs: 2, RAMMB: 2048, DiskGB: 20}
}

func TestCreateInstanceSendsUserDataOnlyWhenSet(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"task-1","status":"queued"}`))
	}))

	doc := "#cloud-config\nruncmd:\n  - echo hi\n"
	req := createBody()
	req.UserData = doc
	if _, err := c.Compute.CreateInstance(context.Background(), req); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if body["user_data"] != doc {
		t.Errorf("user_data = %v, want the document as given", body["user_data"])
	}

	body = nil
	if _, err := c.Compute.CreateInstance(context.Background(), createBody()); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, present := body["user_data"]; present {
		t.Errorf("user_data sent without a document: %v", body)
	}
}

func TestCreateInstanceRefusesBadUserDataBeforeSending(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))

	cases := map[string]string{
		"oversize":        "#cloud-config\n" + strings.Repeat("a", MaxUserDataBytes),
		"NUL byte":        "#cloud-config\nx\x00y",
		"invalid UTF-8":   "#cloud-config\n\xff\xfe",
		"blank":           "   \n\t",
		"no header":       "hostname: web\n",
		"header + word":   "#cloud-config x\n",
		"BOM then no hdr": "\uFEFFhostname: web\n",
	}
	for name, doc := range cases {
		req := createBody()
		req.UserData = doc
		if _, err := c.Compute.CreateInstance(context.Background(), req); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "hostname") || strings.Contains(err.Error(), "echo") {
			t.Errorf("%s: error carries the document: %v", name, err)
		}
	}
	if calls != 0 {
		t.Errorf("%d requests sent for refused documents", calls)
	}
}

func TestCreateInstanceAcceptsUserDataAtTheLimitAndKeepsTheBOM(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"task_id":"t","status":"queued"}`))
	}))

	// The BOM is not counted against the limit: the platform strips it first.
	atLimit := "\uFEFF#cloud-config\n" + strings.Repeat("a", MaxUserDataBytes-len("#cloud-config\n"))
	req := createBody()
	req.UserData = atLimit
	if _, err := c.Compute.CreateInstance(context.Background(), req); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if body["user_data"] != atLimit {
		t.Errorf("the document was altered before sending")
	}

	over := "#cloud-config\n" + strings.Repeat("a", MaxUserDataBytes-len("#cloud-config\n")+1)
	req.UserData = over
	if _, err := c.Compute.CreateInstance(context.Background(), req); err == nil {
		t.Error("one byte over the limit accepted")
	}
}

func TestValidateUserDataHeaders(t *testing.T) {
	ok := []string{
		"#cloud-config\n",
		"#cloud-config  \r\nruncmd: []\n",
		"#!/bin/sh\necho hi\n",
		"Content-Type: multipart/mixed; boundary=\"B\"\n\n",
		"content-type: MULTIPART/mixed; boundary=\"B\"\n\n",
		"\uFEFF#cloud-config\n",
	}
	for _, doc := range ok {
		if err := validateUserData(doc); err != nil {
			t.Errorf("validateUserData(%q) = %v", doc, err)
		}
	}
}

func TestGetInstanceDecodesUserDataSHA256(t *testing.T) {
	cap := &capture{resp: `{"instance":{"id":"i-1","name":"web","status":"running",
		"tenant_id":"t","user_data_sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}`}
	c, _ := newTestClient(t, cap.handler())

	inst, err := c.Compute.GetInstance(context.Background(), "i-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.UserDataSHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("UserDataSHA256 = %q", inst.UserDataSHA256)
	}

	cap.resp = `{"instance":{"id":"i-2","name":"db","status":"running","tenant_id":"t"}}`
	inst, err = c.Compute.GetInstance(context.Background(), "i-2")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.UserDataSHA256 != "" {
		t.Errorf("UserDataSHA256 = %q for an instance without user data", inst.UserDataSHA256)
	}
}
