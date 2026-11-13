// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

type recordedInstanceAdminCall struct {
	method string
	md     metadata.MD
}

type fakeInstanceAdminServer struct {
	btapb.UnimplementedBigtableInstanceAdminServer

	mu    sync.Mutex
	calls []recordedInstanceAdminCall

	listInstFn  func(context.Context, *btapb.ListInstancesRequest) (*btapb.ListInstancesResponse, error)
	listClustFn func(context.Context, *btapb.ListClustersRequest) (*btapb.ListClustersResponse, error)
}

func (s *fakeInstanceAdminServer) record(ctx context.Context, method string) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.calls = append(s.calls, recordedInstanceAdminCall{method: method, md: md.Copy()})
	s.mu.Unlock()
}

func (s *fakeInstanceAdminServer) callsFor(method string) []recordedInstanceAdminCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedInstanceAdminCall
	for _, c := range s.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (s *fakeInstanceAdminServer) ListInstances(ctx context.Context, req *btapb.ListInstancesRequest) (*btapb.ListInstancesResponse, error) {
	s.record(ctx, "ListInstances")
	if s.listInstFn != nil {
		return s.listInstFn(ctx, req)
	}
	return &btapb.ListInstancesResponse{}, nil
}

func (s *fakeInstanceAdminServer) ListClusters(ctx context.Context, req *btapb.ListClustersRequest) (*btapb.ListClustersResponse, error) {
	s.record(ctx, "ListClusters")
	if s.listClustFn != nil {
		return s.listClustFn(ctx, req)
	}
	return &btapb.ListClustersResponse{}, nil
}

func startFakeInstanceAdminTLSServer(t *testing.T, srv *fakeInstanceAdminServer) (addr string, certFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey failed: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate failed: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("x509.MarshalECPrivateKey failed: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("tls.X509KeyPair failed: %v", err)
	}
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatalf("writing ca.pem failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})))
	btapb.RegisterBigtableInstanceAdminServer(grpcSrv, srv)
	go func() {
		_ = grpcSrv.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcSrv.Stop()
		_ = lis.Close()
	})
	return lis.Addr().String(), certPath
}

func captureTestStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
	})

	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		outCh <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = origStdout
	return <-outCh
}

func TestGetInstanceAdminClient_AppliesUserAgentAndAuthzToken(t *testing.T) {
	t.Setenv("BIGTABLE_EMULATOR_HOST", "")
	srv := &fakeInstanceAdminServer{
		listInstFn: func(_ context.Context, _ *btapb.ListInstancesRequest) (*btapb.ListInstancesResponse, error) {
			return &btapb.ListInstancesResponse{
				Instances: []*btapb.Instance{{Name: "projects/ua-proj/instances/ua-inst", DisplayName: "UA Instance"}},
			}, nil
		},
		listClustFn: func(_ context.Context, _ *btapb.ListClustersRequest) (*btapb.ListClustersResponse, error) {
			return &btapb.ListClustersResponse{
				Clusters: []*btapb.Cluster{{Name: "projects/ua-proj/instances/ua-inst/clusters/c1", Location: "projects/ua-proj/locations/us-central1-a", ServeNodes: 3}},
			}, nil
		},
	}
	tlsAddr, certPath := startFakeInstanceAdminTLSServer(t, srv)

	origConfig := config
	origInstAdmin := instanceAdminClient
	origUserAgent := cliUserAgent
	t.Cleanup(func() {
		if instanceAdminClient != nil && instanceAdminClient != origInstAdmin {
			_ = instanceAdminClient.Close()
		}
		config = origConfig
		instanceAdminClient = origInstAdmin
		cliUserAgent = origUserAgent
	})

	cfg := &Config{
		Project:       "ua-proj",
		Instance:      "ua-inst",
		AdminEndpoint: tlsAddr,
		CertFile:      certPath,
		AccessToken:   "static-test-token",
		AuthToken:     "iam-authz-token",
		UserAgent:     "cbt-instance-admin-test/1.0",
		Timeout:       5 * time.Second,
	}
	config = cfg
	instanceAdminClient = nil

	instOut := captureTestStdout(t, func() {
		doMain(cfg, []string{"listinstances"})
	})
	if !strings.Contains(instOut, "ua-inst") {
		t.Errorf("doMain listinstances output = %q, want ua-inst", instOut)
	}
	clustOut := captureTestStdout(t, func() {
		doMain(cfg, []string{"listclusters"})
	})
	if !strings.Contains(clustOut, "c1") {
		t.Errorf("doMain listclusters output = %q, want c1", clustOut)
	}

	for _, method := range []string{"ListInstances", "ListClusters"} {
		calls := srv.callsFor(method)
		if len(calls) != 1 {
			t.Fatalf("%s call count = %d, want 1", method, len(calls))
		}
		uaVals := strings.Join(calls[0].md.Get("user-agent"), " ")
		if !strings.Contains(uaVals, "cbt-instance-admin-test/1.0") {
			t.Errorf("%s user-agent = %q, want substring %q", method, uaVals, "cbt-instance-admin-test/1.0")
		}
		if diff := cmp.Diff([]string{"iam-authz-token"}, calls[0].md.Get("x-goog-iam-authorization-token")); diff != "" {
			t.Errorf("%s x-goog-iam-authorization-token mismatch (-want +got):\n%s", method, diff)
		}
	}
}
