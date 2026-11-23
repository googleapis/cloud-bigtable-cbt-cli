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
	"fmt"
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
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
)

type recordedAdminCall struct {
	method string
	md     metadata.MD
}

type fakeFunctionAdminServer struct {
	btapb.UnimplementedBigtableInstanceAdminServer

	mu         sync.Mutex
	calls      []recordedAdminCall
	getReqs    []*btapb.GetFunctionRequest
	listReqs   []*btapb.ListFunctionsRequest
	deleteReqs []*btapb.DeleteFunctionRequest

	getFn    func(context.Context, *btapb.GetFunctionRequest) (*btapb.Function, error)
	listFn   func(context.Context, *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error)
	deleteFn func(context.Context, *btapb.DeleteFunctionRequest) (*emptypb.Empty, error)
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

func (s *fakeFunctionAdminServer) DeleteFunction(ctx context.Context, req *btapb.DeleteFunctionRequest) (*emptypb.Empty, error) {
	s.record(ctx, "DeleteFunction")
	cloned := proto.Clone(req).(*btapb.DeleteFunctionRequest)
	s.mu.Lock()
	s.deleteReqs = append(s.deleteReqs, cloned)
	fn := s.deleteFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &emptypb.Empty{}, nil
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

// UT-1: Verify WASM scalar and single-level ARRAY<T> type parsing and nil Encoding.
func TestParseWASMFunctionType_Success(t *testing.T) {
	bytesType := &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}
	stringType := &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}
	int64Type := &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}
	int32Type := &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}}
	float64Type := &btapb.Type{Kind: &btapb.Type_Float64Type{Float64Type: &btapb.Type_Float64{}}}
	float32Type := &btapb.Type{Kind: &btapb.Type_Float32Type{Float32Type: &btapb.Type_Float32{}}}
	boolType := &btapb.Type{Kind: &btapb.Type_BoolType{BoolType: &btapb.Type_Bool{}}}
	tsType := &btapb.Type{Kind: &btapb.Type_TimestampType{TimestampType: &btapb.Type_Timestamp{}}}

	tests := []struct {
		name string
		spec string
		want *btapb.Type
	}{
		{name: "scalar_bytes", spec: "BYTES", want: bytesType},
		{name: "scalar_string", spec: "STRING", want: stringType},
		{name: "scalar_int64", spec: "INT64", want: int64Type},
		{name: "scalar_int32", spec: "INT32", want: int32Type},
		{name: "scalar_float64", spec: "FLOAT64", want: float64Type},
		{name: "scalar_float32", spec: "FLOAT32", want: float32Type},
		{name: "scalar_bool", spec: "BOOL", want: boolType},
		{name: "scalar_timestamp", spec: "TIMESTAMP", want: tsType},
		{name: "case_insensitive_scalar", spec: "  string ", want: stringType},
		{
			name: "array_bytes",
			spec: "ARRAY<BYTES>",
			want: &btapb.Type{Kind: &btapb.Type_ArrayType{ArrayType: &btapb.Type_Array{ElementType: bytesType}}},
		},
		{
			name: "array_int64_lowercase_spaced",
			spec: "array< int64 >",
			want: &btapb.Type{Kind: &btapb.Type_ArrayType{ArrayType: &btapb.Type_Array{ElementType: int64Type}}},
		},
		{
			name: "array_timestamp",
			spec: "ARRAY<TIMESTAMP>",
			want: &btapb.Type{Kind: &btapb.Type_ArrayType{ArrayType: &btapb.Type_Array{ElementType: tsType}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWASMFunctionType(tc.spec)
			if err != nil {
				t.Fatalf("parseWASMFunctionType(%q) unexpected error: %v", tc.spec, err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("parseWASMFunctionType(%q) mismatch (-want +got):\n%s", tc.spec, diff)
			}
			if err := checkNilAdminTypeEncoding(got); err != nil {
				t.Errorf("parseWASMFunctionType(%q) encoding invariant failed: %v", tc.spec, err)
			}
		})
	}
}

func checkNilAdminTypeEncoding(typ *btapb.Type) error {
	switch k := typ.GetKind().(type) {
	case *btapb.Type_BytesType:
		if k.BytesType.GetEncoding() != nil {
			return fmt.Errorf("BytesType.Encoding = %v, want nil", k.BytesType.GetEncoding())
		}
	case *btapb.Type_StringType:
		if k.StringType.GetEncoding() != nil {
			return fmt.Errorf("StringType.Encoding = %v, want nil", k.StringType.GetEncoding())
		}
	case *btapb.Type_Int64Type:
		if k.Int64Type.GetEncoding() != nil {
			return fmt.Errorf("Int64Type.Encoding = %v, want nil", k.Int64Type.GetEncoding())
		}
	case *btapb.Type_Int32Type:
		if k.Int32Type.GetEncoding() != nil {
			return fmt.Errorf("Int32Type.Encoding = %v, want nil", k.Int32Type.GetEncoding())
		}
	case *btapb.Type_TimestampType:
		if k.TimestampType.GetEncoding() != nil {
			return fmt.Errorf("TimestampType.Encoding = %v, want nil", k.TimestampType.GetEncoding())
		}
	case *btapb.Type_ArrayType:
		return checkNilAdminTypeEncoding(k.ArrayType.GetElementType())
	}
	return nil
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

func TestParseWASMFunctionType_Errors(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{name: "empty", spec: "   ", wantSub: "must not be empty"},
		{name: "date_rejected", spec: "DATE", wantSub: "unsupported WASM function type"},
		{name: "array_date_rejected", spec: "ARRAY<DATE>", wantSub: "unsupported WASM function type"},
		{name: "nested_array_rejected", spec: "ARRAY<ARRAY<INT64>>", wantSub: "nested array type"},
		{name: "unclosed_array", spec: "ARRAY<INT64", wantSub: "missing closing '>'"},
		{name: "empty_array_element", spec: "ARRAY<>", wantSub: "unsupported WASM function type"},
		{name: "struct_rejected", spec: "STRUCT<a INT64>", wantSub: "unsupported WASM function type"},
		{name: "unknown_type", spec: "JSON", wantSub: "unsupported WASM function type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWASMFunctionType(tc.spec)
			if err == nil {
				t.Fatalf("parseWASMFunctionType(%q) = %v, want error containing %q", tc.spec, got, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseWASMFunctionType(%q) error = %q, want substring %q", tc.spec, err.Error(), tc.wantSub)
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

// UT-9 & UT-10a: Verify getfunction WASM metadata rendering with lowercase hex
// SourceSha256 digest and Etag.
func TestRunGetFunction_WASMWithSHA256AndEtag(t *testing.T) {
	fnPrefix := "projects/my-proj/instances/my-inst/functions/"
	const fnID = "doc_extract_snippet"
	const sourceSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

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
						SourceSha256: sourceSHA256,
					},
				},
			}, nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	if err := runGetFunction(t.Context(), api, "my-proj", "my-inst", &out, fnID); err != nil {
		t.Fatalf("runGetFunction(%q) unexpected error: %v", fnID, err)
	}
	wantName := fnPrefix + fnID
	if gotName := srv.getReqs[0].GetName(); gotName != wantName {
		t.Errorf("GetFunction(%q) Request.Name = %q, want %q", fnID, gotName, wantName)
	}
	getCalls := srv.callsFor("GetFunction")
	if diff := cmp.Diff([]string{"name=" + url.QueryEscape(wantName)}, getCalls[0].md.Get(xGoogRequestParamsHeader)); diff != "" {
		t.Errorf("GetFunction(%q) x-goog-request-params mismatch (-want +got):\n%s", fnID, diff)
	}
	if diff := cmp.Diff([]string{"projects/my-proj"}, getCalls[0].md.Get(googleCloudResourcePrefixHeader)); diff != "" {
		t.Errorf("GetFunction(%q) google-cloud-resource-prefix mismatch (-want +got):\n%s", fnID, diff)
	}
	wantOut := strings.Join([]string{
		"Name: " + wantName,
		"Type: WASM",
		`Etag: W/"v2"`,
		"DeletionProtection: true",
		"Arguments: doc:BYTES",
		"ReturnType: STRING",
		"SourceSha256: " + sourceSHA256,
		"",
	}, "\n")
	if diff := cmp.Diff(wantOut, out.String()); diff != "" {
		t.Errorf("runGetFunction(%q) output mismatch (-want +got):\n%s", fnID, diff)
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

// Verify deletefunction argument, identifier, and option rejections fail fast with zero outbound gRPC calls.
func TestRunDeleteFunction_ValidationErrors_ZeroRPCs(t *testing.T) {
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)

	errCases := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{
			name:    "delete_no_args_rejected",
			args:    nil,
			wantSub: "usage: cbt deletefunction",
		},
		{
			name:    "delete_invalid_function_id_rejected",
			args:    []string{"BadFunc"},
			wantSub: "invalid function-id",
		},
		{
			name:    "delete_overlong_function_id_rejected",
			args:    []string{strings.Repeat("f", maxFunctionIdentifierLen+1)},
			wantSub: "exceeds maximum length",
		},
		{
			name:    "delete_empty_etag_rejected",
			args:    []string{"doc_fn", "etag="},
			wantSub: "option \"etag\" must not be empty",
		},
		{
			name:    "delete_unexpected_option_rejected",
			args:    []string{"doc_fn", "unknown=1"},
			wantSub: "unknown arg key",
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			before := srv.totalCalls()
			err := runDeleteFunction(t.Context(), api, "p", "i", tc.args...)
			if err == nil {
				t.Fatalf("runDeleteFunction(%v) = nil, want error containing %q", tc.args, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("runDeleteFunction(%v) error = %q, want substring %q", tc.args, err.Error(), tc.wantSub)
			}
			if got := srv.totalCalls() - before; got != 0 {
				t.Errorf("runDeleteFunction(%v) outbound gRPC calls = %d, want 0", tc.args, got)
			}
		})
	}
}

// UT-10b: Verify deletefunction resource names, routing headers, and optional etag forwarding.
func TestRunDeleteFunction_WithAndWithoutEtag(t *testing.T) {
	fnPrefix := "projects/my-proj/instances/my-inst/functions/"
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)

	if err := runDeleteFunction(t.Context(), api, "my-proj", "my-inst", "fn_one"); err != nil {
		t.Fatalf("runDeleteFunction without etag unexpected error: %v", err)
	}
	complexEtag := `W/"/v1/hash=abc=="`
	if err := runDeleteFunction(t.Context(), api, "my-proj", "my-inst", "fn_two", "etag="+complexEtag); err != nil {
		t.Fatalf("runDeleteFunction with etag unexpected error: %v", err)
	}

	wantReqs := []*btapb.DeleteFunctionRequest{
		{Name: fnPrefix + "fn_one", Etag: ""},
		{Name: fnPrefix + "fn_two", Etag: complexEtag},
	}
	if diff := cmp.Diff(wantReqs, srv.deleteReqs, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteFunctionRequests mismatch (-want +got):\n%s", diff)
	}
	delCalls := srv.callsFor("DeleteFunction")
	if diff := cmp.Diff([]string{"name=" + url.QueryEscape(fnPrefix+"fn_two")}, delCalls[1].md.Get(xGoogRequestParamsHeader)); diff != "" {
		t.Errorf("DeleteFunction x-goog-request-params mismatch (-want +got):\n%s", diff)
	}
}

// UT-10c: Verify NotFound, Aborted, and FailedPrecondition gRPC error propagation on DeleteFunction.
func TestRunDeleteFunction_StatusErrorPropagation(t *testing.T) {
	errCases := []struct {
		name     string
		srvErr   error
		wantCode codes.Code
	}{
		{
			name:     "delete_not_found",
			srvErr:   status.Error(codes.NotFound, "function not found"),
			wantCode: codes.NotFound,
		},
		{
			name:     "delete_aborted_etag_mismatch",
			srvErr:   status.Error(codes.Aborted, "etag mismatch"),
			wantCode: codes.Aborted,
		},
		{
			name:     "delete_failed_precondition_deletion_protection",
			srvErr:   status.Error(codes.FailedPrecondition, "deletion_protection is enabled"),
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &fakeFunctionAdminServer{
				deleteFn: func(_ context.Context, _ *btapb.DeleteFunctionRequest) (*emptypb.Empty, error) {
					return nil, tc.srvErr
				},
			}
			api := startFakeFunctionAdminGRPC(t, srv)

			err := runDeleteFunction(t.Context(), api, "my-proj", "my-inst", "fn_err", "etag=v1")
			if gotCode := status.Code(err); gotCode != tc.wantCode {
				t.Errorf("status.Code(err) = %v, want %v (err: %v)", gotCode, tc.wantCode, err)
			}
		})
	}
}

var allFunctionCommandNames = []string{
	"deletefunction",
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
