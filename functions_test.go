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
	getReqs  []*btapb.GetFunctionRequest
	listReqs []*btapb.ListFunctionsRequest

	getFn  func(context.Context, *btapb.GetFunctionRequest) (*btapb.Function, error)
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

func (s *fakeFunctionAdminServer) GetFunction(ctx context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error) {
	s.record(ctx, "GetFunction")
	cloned := proto.Clone(req).(*btapb.GetFunctionRequest)
	s.mu.Lock()
	s.getReqs = append(s.getReqs, cloned)
	fn := s.getFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected GetFunction")
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

// Verify getfunction argument and identifier rejections fail fast with zero outbound gRPC calls.
func TestRunGetFunction_ValidationErrors_ZeroRPCs(t *testing.T) {
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)

	errCases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{
			name:    "get_no_args_rejected",
			args:    nil,
			wantSub: "usage: cbt getfunction",
		},
		{
			name:    "get_too_many_args_rejected",
			args:    []string{"doc_fn", "extra"},
			wantSub: "usage: cbt getfunction",
		},
		{
			name:    "get_invalid_function_id_rejected",
			args:    []string{"BadFunc"},
			wantSub: "invalid function-id",
		},
		{
			name:    "get_overlong_function_id_rejected",
			args:    []string{strings.Repeat("f", maxFunctionIdentifierLen+1)},
			wantSub: "exceeds maximum length",
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			before := srv.totalCalls()
			err := runGetFunction(t.Context(), api, "p", "i", io.Discard, tc.args...)
			if err == nil {
				t.Fatalf("runGetFunction(%v) = nil, want error containing %q", tc.args, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("runGetFunction(%v) error = %q, want substring %q", tc.args, err.Error(), tc.wantSub)
			}
			if got := srv.totalCalls() - before; got != 0 {
				t.Errorf("runGetFunction(%v) outbound gRPC calls = %d, want 0", tc.args, got)
			}
		})
	}
}

// UT-9 & UT-10a: Verify getfunction WASM metadata rendering with both raw and
// pre-encoded ASCII hex SourceSha256 digests and Etag.
func TestRunGetFunction_WASMWithSHA256AndEtag(t *testing.T) {
	fnPrefix := "projects/my-proj/instances/my-inst/functions/"
	preEncodedHex := "0123456789abcdefABCDEF0123456789abcdefABCDEF0123456789abcdefABCD"

	tests := []struct {
		name         string
		fnID         string
		sourceSHA256 []byte
		wantSHA256   string
	}{
		{
			name:         "raw_bytes",
			fnID:         "doc_extract_snippet",
			sourceSHA256: []byte{0x01, 0x23, 0xab, 0xcd},
			wantSHA256:   "0123abcd",
		},
		{
			name:         "raw_32_byte_digest_of_ascii_hex_bytes",
			fnID:         "doc_raw_digest",
			sourceSHA256: bytes.Repeat([]byte("a"), 32),
			wantSHA256:   strings.Repeat("61", 32),
		},
		{
			name:         "pre_encoded_64_byte_ascii_hex",
			fnID:         "doc_preencoded_digest",
			sourceSHA256: []byte(preEncodedHex),
			wantSHA256:   "0123456789abcdefabcdef0123456789abcdefabcdef0123456789abcdefabcd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &fakeFunctionAdminServer{
				getFn: func(_ context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error) {
					return &btapb.Function{
						Name:               req.GetName(),
						Etag:               `W/"v2"`,
						DeletionProtection: true,
						FunctionType: &btapb.Function_WasmFunction_{
							WasmFunction: &btapb.Function_WasmFunction{
								Arguments:    []*btapb.Function_Argument{{Name: "doc", DataType: &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}}},
								ReturnType:   &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}},
								SourceSha256: tc.sourceSHA256,
							},
						},
					}, nil
				},
			}
			api := startFakeFunctionAdminGRPC(t, srv)

			var out bytes.Buffer
			if err := runGetFunction(t.Context(), api, "my-proj", "my-inst", &out, tc.fnID); err != nil {
				t.Fatalf("runGetFunction(%q) unexpected error: %v", tc.fnID, err)
			}
			wantName := fnPrefix + tc.fnID
			if gotName := srv.getReqs[0].GetName(); gotName != wantName {
				t.Errorf("GetFunction(%q) Request.Name = %q, want %q", tc.fnID, gotName, wantName)
			}
			getCalls := srv.callsFor("GetFunction")
			if diff := cmp.Diff([]string{"name=" + url.QueryEscape(wantName)}, getCalls[0].md.Get(xGoogRequestParamsHeader)); diff != "" {
				t.Errorf("GetFunction(%q) x-goog-request-params mismatch (-want +got):\n%s", tc.fnID, diff)
			}
			if diff := cmp.Diff([]string{"projects/my-proj"}, getCalls[0].md.Get(googleCloudResourcePrefixHeader)); diff != "" {
				t.Errorf("GetFunction(%q) google-cloud-resource-prefix mismatch (-want +got):\n%s", tc.fnID, diff)
			}
			wantOut := strings.Join([]string{
				"Name: " + wantName,
				"Type: WASM",
				`Etag: W/"v2"`,
				"DeletionProtection: true",
				"Arguments: doc:BYTES",
				"ReturnType: STRING",
				"SourceSha256: " + tc.wantSHA256,
				"",
			}, "\n")
			if diff := cmp.Diff(wantOut, out.String()); diff != "" {
				t.Errorf("runGetFunction(%q) output mismatch (-want +got):\n%s", tc.fnID, diff)
			}
		})
	}
}

func TestFormatWASMSourceSHA256(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "nil_slice",
			raw:  nil,
			want: "",
		},
		{
			name: "empty_slice",
			raw:  []byte{},
			want: "",
		},
		{
			name: "raw_32_byte_sha256",
			raw:  bytes.Repeat([]byte("f"), 32),
			want: strings.Repeat("66", 32),
		},
		{
			name: "all_hex_63_bytes_not_treated_as_preencoded",
			raw:  bytes.Repeat([]byte("a"), 63),
			want: strings.Repeat("61", 63),
		},
		{
			name: "all_hex_65_bytes_not_treated_as_preencoded",
			raw:  bytes.Repeat([]byte("a"), 65),
			want: strings.Repeat("61", 65),
		},
		{
			name: "preencoded_64_byte_mixed_case_boundaries",
			raw:  []byte("09afAF0123456789abcdefABCDEF0123456789abcdefABCDEF0123456789abcd"),
			want: "09afaf0123456789abcdefabcdef0123456789abcdefabcdef0123456789abcd",
		},
		{
			name: "non_hex_64_bytes_below_zero",
			raw:  append([]byte{'/'}, bytes.Repeat([]byte("0"), 63)...),
			want: "2f" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_above_nine",
			raw:  append([]byte{':'}, bytes.Repeat([]byte("0"), 63)...),
			want: "3a" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_below_upper_a",
			raw:  append([]byte{'@'}, bytes.Repeat([]byte("0"), 63)...),
			want: "40" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_above_upper_f",
			raw:  append([]byte{'G'}, bytes.Repeat([]byte("0"), 63)...),
			want: "47" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_below_lower_a",
			raw:  append([]byte{'`'}, bytes.Repeat([]byte("0"), 63)...),
			want: "60" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_above_lower_f",
			raw:  append([]byte{'g'}, bytes.Repeat([]byte("0"), 63)...),
			want: "67" + strings.Repeat("30", 63),
		},
		{
			name: "non_hex_64_bytes_at_tail_index_63",
			raw:  append(bytes.Repeat([]byte("0"), 63), 'g'),
			want: strings.Repeat("30", 63) + "67",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWASMSourceSHA256(tc.raw); got != tc.want {
				t.Errorf("formatWASMSourceSHA256(%x) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// UT-9b: Verify getfunction omits optional Etag and SourceSha256 lines when empty.
func TestRunGetFunction_WASMWithoutSHA256OrEtag(t *testing.T) {
	srv := &fakeFunctionAdminServer{
		getFn: func(_ context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error) {
			return &btapb.Function{
				Name: req.GetName(),
				FunctionType: &btapb.Function_WasmFunction_{
					WasmFunction: &btapb.Function_WasmFunction{
						Arguments:  []*btapb.Function_Argument{{Name: "x", DataType: &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}}}},
						ReturnType: &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}},
					},
				},
			}, nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	if err := runGetFunction(t.Context(), api, "my-proj", "my-inst", &out, "no_sha"); err != nil {
		t.Fatalf("runGetFunction unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "SourceSha256:") || strings.Contains(out.String(), "Etag:") {
		t.Errorf("runGetFunction output = %q, should omit empty SourceSha256 and Etag", out.String())
	}
}

// UT-9c: Verify getfunction formats SqlScalarFunction definitions with ANY and DATE types.
func TestRunGetFunction_SQLScalarFunction(t *testing.T) {
	fnPrefix := "projects/my-proj/instances/my-inst/functions/"
	srv := &fakeFunctionAdminServer{
		getFn: func(_ context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error) {
			return &btapb.Function{
				Name: req.GetName(),
				FunctionType: &btapb.Function_SqlScalarFunction_{
					SqlScalarFunction: &btapb.Function_SqlScalarFunction{
						Arguments:    []*btapb.Function_Argument{{Name: "x", DataType: nil}},
						ReturnType:   &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}},
						FunctionBody: "DATE_FROM_UNIX_DATE(x)",
					},
				},
			}, nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	if err := runGetFunction(t.Context(), api, "my-proj", "my-inst", &out, "sql_fn"); err != nil {
		t.Fatalf("runGetFunction unexpected error: %v", err)
	}
	wantOut := strings.Join([]string{
		"Name: " + fnPrefix + "sql_fn",
		"Type: SQL",
		"DeletionProtection: false",
		"Arguments: x:ANY",
		"ReturnType: DATE",
		"FunctionBody: DATE_FROM_UNIX_DATE(x)",
		"",
	}, "\n")
	if diff := cmp.Diff(wantOut, out.String()); diff != "" {
		t.Errorf("runGetFunction SQL output mismatch (-want +got):\n%s", diff)
	}
}

// UT-10c: Verify NotFound gRPC error propagation on GetFunction.
func TestRunGetFunction_StatusErrorPropagation(t *testing.T) {
	srv := &fakeFunctionAdminServer{
		getFn: func(_ context.Context, _ *btapb.GetFunctionRequest) (*btapb.Function, error) {
			return nil, status.Error(codes.NotFound, "function not found")
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	err := runGetFunction(t.Context(), api, "my-proj", "my-inst", io.Discard, "fn_err")
	if gotCode := status.Code(err); gotCode != codes.NotFound {
		t.Errorf("status.Code(err) = %v, want %v (err: %v)", gotCode, codes.NotFound, err)
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
	"getfunction",
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
