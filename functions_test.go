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
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type recordedAdminCall struct {
	method string
	md     metadata.MD
}

type fakeFunctionAdminServer struct {
	btapb.UnimplementedBigtableInstanceAdminServer

	mu       sync.Mutex
	calls    []recordedAdminCall
	listReqs []*btapb.ListFunctionsRequest

	listFn func(context.Context, *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error)
}

func (s *fakeFunctionAdminServer) record(ctx context.Context, method string) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, recordedAdminCall{method: method, md: md})
}

func (s *fakeFunctionAdminServer) totalCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *fakeFunctionAdminServer) callsFor(method string) []recordedAdminCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedAdminCall
	for _, c := range s.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (s *fakeFunctionAdminServer) ListFunctions(ctx context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error) {
	s.record(ctx, "ListFunctions")
	cloned := proto.Clone(req).(*btapb.ListFunctionsRequest)
	s.mu.Lock()
	s.listReqs = append(s.listReqs, cloned)
	fn := s.listFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected ListFunctions")
}

func startFakeFunctionAdminGRPC(t *testing.T, srv *fakeFunctionAdminServer) *grpcFunctionAdminClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	grpcSrv := grpc.NewServer()
	btapb.RegisterBigtableInstanceAdminServer(grpcSrv, srv)
	go func() {
		_ = grpcSrv.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcSrv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient failed: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return newGRPCFunctionAdminClient(conn)
}

func TestFormatAdminType_AllSQLVariants(t *testing.T) {
	tests := []struct {
		name string
		typ  *btapb.Type
		want string
	}{
		{name: "nil_type", typ: nil, want: "ANY"},
		{name: "unset_kind", typ: &btapb.Type{}, want: "ANY"},
		{name: "bytes", typ: &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}, want: "BYTES"},
		{name: "string", typ: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}, want: "STRING"},
		{name: "int64", typ: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}, want: "INT64"},
		{name: "int32", typ: &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}}, want: "INT32"},
		{name: "float64", typ: &btapb.Type{Kind: &btapb.Type_Float64Type{Float64Type: &btapb.Type_Float64{}}}, want: "FLOAT64"},
		{name: "float32", typ: &btapb.Type{Kind: &btapb.Type_Float32Type{Float32Type: &btapb.Type_Float32{}}}, want: "FLOAT32"},
		{name: "bool", typ: &btapb.Type{Kind: &btapb.Type_BoolType{BoolType: &btapb.Type_Bool{}}}, want: "BOOL"},
		{name: "timestamp", typ: &btapb.Type{Kind: &btapb.Type_TimestampType{TimestampType: &btapb.Type_Timestamp{}}}, want: "TIMESTAMP"},
		{name: "date", typ: &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}}, want: "DATE"},
		{name: "array_of_string", typ: &btapb.Type{Kind: &btapb.Type_ArrayType{ArrayType: &btapb.Type_Array{ElementType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}}}}, want: "ARRAY<STRING>"},
		{name: "geography", typ: &btapb.Type{Kind: &btapb.Type_GeographyType{GeographyType: &btapb.Type_Geography{}}}, want: "GEOGRAPHY"},
		{
			name: "proto_type",
			typ: &btapb.Type{
				Kind: &btapb.Type_ProtoType{
					ProtoType: &btapb.Type_Proto{SchemaBundleId: "bundle1", MessageName: "pkg.MyMsg"},
				},
			},
			want: "PROTO<bundle1.pkg.MyMsg>",
		},
		{
			name: "enum_type",
			typ: &btapb.Type{
				Kind: &btapb.Type_EnumType{
					EnumType: &btapb.Type_Enum{SchemaBundleId: "bundle1", EnumName: "pkg.MyEnum"},
				},
			},
			want: "ENUM<bundle1.pkg.MyEnum>",
		},
		{
			name: "aggregate_type",
			typ: &btapb.Type{
				Kind: &btapb.Type_AggregateType{
					AggregateType: &btapb.Type_Aggregate{
						InputType: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}},
					},
				},
			},
			want: "AGGREGATE<INT64>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAdminType(tc.typ); got != tc.want {
				t.Errorf("formatAdminType(%v) = %q, want %q", tc.typ, got, tc.want)
			}
		})
	}
}

// UT-8 & UT-9: Verify listfunctions pagination, single non-duplicated routing headers per page,
// unexpected positional argument rejection, and formatting of WASM and SQL functions.
func TestRunListFunctions_PaginationHeadersAndSQLTypes(t *testing.T) {
	parent := "projects/my-proj/instances/my-inst"
	pages := map[string]*btapb.ListFunctionsResponse{
		"": {
			Functions: []*btapb.Function{
				{
					Name: parent + "/functions/doc_has_keyword",
					FunctionType: &btapb.Function_WasmFunction_{
						WasmFunction: &btapb.Function_WasmFunction{
							Arguments: []*btapb.Function_Argument{
								{Name: "payload", DataType: &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}},
								{Name: "keyword", DataType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}},
							},
							ReturnType: &btapb.Type{Kind: &btapb.Type_BoolType{BoolType: &btapb.Type_Bool{}}},
						},
					},
				},
			},
			NextPageToken: "tok-2",
		},
		"tok-2": {
			Functions: []*btapb.Function{
				{
					Name: parent + "/functions/sql_templated_zero_arg",
					FunctionType: &btapb.Function_SqlScalarFunction_{
						SqlScalarFunction: &btapb.Function_SqlScalarFunction{
							Arguments:    nil,
							ReturnType:   nil, // Unset/inferred ANY return type.
							FunctionBody: "CURRENT_DATE()",
						},
					},
				},
			},
			NextPageToken: "tok-3",
		},
		"tok-3": {
			Functions: []*btapb.Function{
				{
					Name: parent + "/functions/sql_complex_types",
					FunctionType: &btapb.Function_SqlScalarFunction_{
						SqlScalarFunction: &btapb.Function_SqlScalarFunction{
							Arguments: []*btapb.Function_Argument{
								{Name: "any_in", DataType: nil},
								{Name: "dt", DataType: &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}}},
								{
									Name: "rec",
									DataType: &btapb.Type{
										Kind: &btapb.Type_StructType{
											StructType: &btapb.Type_Struct{
												Fields: []*btapb.Type_Struct_Field{
													{FieldName: "id", Type: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}},
													{FieldName: "", Type: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}},
												},
											},
										},
									},
								},
							},
							ReturnType: &btapb.Type{
								Kind: &btapb.Type_MapType{
									MapType: &btapb.Type_Map{
										KeyType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}},
										ValueType: &btapb.Type{
											Kind: &btapb.Type_ArrayType{
												ArrayType: &btapb.Type_Array{
													ElementType: &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}},
												},
											},
										},
									},
								},
							},
							FunctionBody: "MAP {}",
						},
					},
				},
			},
			NextPageToken: "",
		},
	}

	srv := &fakeFunctionAdminServer{
		listFn: func(_ context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error) {
			resp, ok := pages[req.GetPageToken()]
			if !ok {
				return nil, status.Errorf(codes.InvalidArgument, "unexpected page token %q", req.GetPageToken())
			}
			return resp, nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	// Reject unexpected positional arguments before issuing any RPC.
	if err := runListFunctions(t.Context(), api, "my-proj", "my-inst", io.Discard, "extra"); err == nil {
		t.Fatal("runListFunctions with extra arg succeeded, want error")
	}
	if got := srv.totalCalls(); got != 0 {
		t.Fatalf("outbound gRPC calls after extra arg = %d, want 0", got)
	}

	var out bytes.Buffer
	if err := runListFunctions(t.Context(), api, "my-proj", "my-inst", &out); err != nil {
		t.Fatalf("runListFunctions unexpected error: %v", err)
	}

	wantTokens := []string{"", "tok-2", "tok-3"}
	var gotTokens []string
	for _, r := range srv.listReqs {
		gotTokens = append(gotTokens, r.GetPageToken())
	}
	if diff := cmp.Diff(wantTokens, gotTokens); diff != "" {
		t.Errorf("ListFunctions page tokens mismatch (-want +got):\n%s", diff)
	}

	// Verify each page carries exactly one x-goog-request-params and one resource-prefix header.
	wantParams := []string{"parent=" + url.QueryEscape(parent)}
	for i, call := range srv.callsFor("ListFunctions") {
		if diff := cmp.Diff(wantParams, call.md.Get(xGoogRequestParamsHeader)); diff != "" {
			t.Errorf("ListFunctions[%d] x-goog-request-params mismatch (-want +got):\n%s", i, diff)
		}
		if diff := cmp.Diff([]string{"projects/my-proj"}, call.md.Get(googleCloudResourcePrefixHeader)); diff != "" {
			t.Errorf("ListFunctions[%d] google-cloud-resource-prefix mismatch (-want +got):\n%s", i, diff)
		}
	}

	gotOut := out.String()
	for _, wantLine := range []string{
		"Function ID",
		"doc_has_keyword",
		"WASM",
		"(payload:BYTES, keyword:STRING) -> BOOL",
		"sql_templated_zero_arg",
		"SQL",
		"() -> ANY",
		"sql_complex_types",
		"(any_in:ANY, dt:DATE, rec:STRUCT<id INT64, STRING>) -> MAP<STRING, ARRAY<DATE>>",
	} {
		if !strings.Contains(gotOut, wantLine) {
			t.Errorf("runListFunctions output = %q, missing %q", gotOut, wantLine)
		}
	}
}

var allFunctionCommandNames = []string{
	"listfunctions",
}

// UT-13a: Verify ProjectAndInstanceRequired CheckFlags enforcement for Function Admin commands.
func TestCLIFunctionCommands_ProjectAndInstanceRequired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("PATH", "")

	cmdByName := make(map[string]RequiredFlags)
	for _, cmd := range commands {
		cmdByName[cmd.Name] = cmd.Required
	}

	for _, name := range allFunctionCommandNames {
		req, ok := cmdByName[name]
		if !ok {
			t.Fatalf("command %q is not registered in commands[]", name)
		}
		if req != ProjectAndInstanceRequired {
			t.Errorf("command %q Required = %v, want ProjectAndInstanceRequired (%v)", name, req, ProjectAndInstanceRequired)
		}

		missingBoth := (&Config{Creds: "dummy.json"}).CheckFlags(req)
		if missingBoth == nil || !strings.Contains(missingBoth.Error(), "-project") || !strings.Contains(missingBoth.Error(), "-instance") {
			t.Errorf("%s CheckFlags(empty) = %v, want missing -project and -instance", name, missingBoth)
		}
		missingInst := (&Config{Project: "p", Creds: "dummy.json"}).CheckFlags(req)
		if missingInst == nil || !strings.Contains(missingInst.Error(), "-instance") {
			t.Errorf("%s CheckFlags(project only) = %v, want missing -instance", name, missingInst)
		}
		missingProj := (&Config{Instance: "i", Creds: "dummy.json"}).CheckFlags(req)
		if missingProj == nil || !strings.Contains(missingProj.Error(), "-project") {
			t.Errorf("%s CheckFlags(instance only) = %v, want missing -project", name, missingProj)
		}
	}
}

// UT-13b: Verify cbt help <cmd> renders complete command usage for Function Admin commands.
func TestCLIFunctionCommands_HelpRendersUsage(t *testing.T) {
	for _, name := range allFunctionCommandNames {
		got := captureStdout(t, func() {
			doMain(&Config{}, []string{"help", name})
		})
		if !strings.Contains(got, "cbt "+name) {
			t.Errorf("cbt help %s output = %q, want usage containing %q", name, got, "cbt "+name)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
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
