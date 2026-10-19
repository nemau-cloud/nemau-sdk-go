package nemau

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The fixtures below are placeholders ("CERT", "KEY"): the SDK relays PEM
// verbatim and the API does the validation.

func TestImportCertificateSendsThePEMAndDecodesTheMetadata(t *testing.T) {
	var method, path string
	var body map[string]string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"crt-1","name":"www","fingerprint_sha256":"AB:CD","subject":"www.example.com",
			"domains":["www.example.com","example.com"],"issuer":"Example CA (R11)","not_before":"2026-09-01T00:00:00Z",
			"not_after":"2026-12-01T00:00:00Z","key_algorithm":"ECDSA-P256","chain_length":1,"used_by":[],
			"created_at":"2026-09-23T10:00:00Z"}`))
	}))
	cert, err := c.LoadBalancer.ImportCertificate(context.Background(), ImportCertificateRequest{
		Name: "www", Certificate: "CERT", PrivateKey: "KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/v1/network/load-balancer-certificates" {
		t.Fatalf("%s %s", method, path)
	}
	if body["name"] != "www" || body["certificate"] != "CERT" || body["private_key"] != "KEY" {
		t.Fatalf("body = %v", body)
	}
	if _, sent := body["chain"]; sent {
		t.Fatal("an empty chain was sent")
	}
	if cert.ID != "crt-1" || len(cert.Domains) != 2 || cert.NotAfter.Month() != 12 || cert.KeyAlgorithm != "ECDSA-P256" {
		t.Fatalf("certificate = %+v", cert)
	}
}

func TestListAndGetCertificates(t *testing.T) {
	cap := &capture{resp: `{"certificates":[{"id":"crt-1","name":"www","used_by":[
		{"listener_id":"lis-1","listener_name":"https","load_balancer_id":"lb-1"}]}],"count":1}`}
	c, _ := newTestClient(t, cap.handler())
	certs, err := c.LoadBalancer.ListCertificates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/v1/network/load-balancer-certificates" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if len(certs) != 1 || certs[0].UsedBy[0].ListenerName != "https" {
		t.Fatalf("certificates = %+v", certs)
	}

	cap.resp = `{"id":"crt-1","name":"www"}`
	if _, err := c.LoadBalancer.GetCertificate(context.Background(), "crt-1"); err != nil {
		t.Fatal(err)
	}
	if cap.path != "/api/v1/network/load-balancer-certificates/crt-1" {
		t.Fatalf("get path = %s", cap.path)
	}
}

func TestDeleteCertificate(t *testing.T) {
	var method, path string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	if err := c.LoadBalancer.DeleteCertificate(context.Background(), "crt-1"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/api/v1/network/load-balancer-certificates/crt-1" {
		t.Fatalf("%s %s", method, path)
	}
}

func TestDeleteCertificateInUseIsRecognizable(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"certificate is used by a listener","reason":"certificate_in_use"}`))
	}))
	err := c.LoadBalancer.DeleteCertificate(context.Background(), "crt-1")
	if !IsCertificateInUse(err) || !strings.Contains(err.Error(), "certificate_in_use") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateListenerCanRepointTheCertificate(t *testing.T) {
	client, seen := captureUpdate(t)
	if _, err := client.LoadBalancer.UpdateListener(context.Background(), "lis-1",
		Update{TLSCertificateRef: Set("crt-2")}); err != nil {
		t.Fatal(err)
	}
	if seen.method != http.MethodPatch || seen.path != "/api/v1/network/listeners/lis-1" || seen.body["tls_certificate_ref"] != "crt-2" {
		t.Fatalf("%s %s %v", seen.method, seen.path, seen.body)
	}
}
