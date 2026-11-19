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
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/go-cmp/cmp"
	gax "github.com/googleapis/gax-go/v2"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var fastTestBackoff = gax.Backoff{
	Initial:    time.Millisecond,
	Max:        2 * time.Millisecond,
	Multiplier: 1.5,
}

type recordedAdminCall struct {
	method string
	md     metadata.MD
}

type fakeFunctionAdminServer struct {
	btapb.UnimplementedBigtableInstanceAdminServer
	longrunningpb.UnimplementedOperationsServer

	mu         sync.Mutex
	calls      []recordedAdminCall
	createReqs []*btapb.CreateFunctionRequest
	updateReqs []*btapb.UpdateFunctionRequest
	getReqs    []*btapb.GetFunctionRequest
	listReqs   []*btapb.ListFunctionsRequest
	deleteReqs []*btapb.DeleteFunctionRequest
	getOpReqs  []*longrunningpb.GetOperationRequest

	createFn func(context.Context, *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error)
	updateFn func(context.Context, *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error)
	getFn    func(context.Context, *btapb.GetFunctionRequest) (*btapb.Function, error)
	listFn   func(context.Context, *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error)
	deleteFn func(context.Context, *btapb.DeleteFunctionRequest) (*emptypb.Empty, error)
	getOpFn  func(context.Context, *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error)
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

func (s *fakeFunctionAdminServer) CreateFunction(ctx context.Context, req *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
	s.record(ctx, "CreateFunction")
	cloned := proto.Clone(req).(*btapb.CreateFunctionRequest)
	s.mu.Lock()
	s.createReqs = append(s.createReqs, cloned)
	fn := s.createFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected CreateFunction")
}

func (s *fakeFunctionAdminServer) UpdateFunction(ctx context.Context, req *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
	s.record(ctx, "UpdateFunction")
	cloned := proto.Clone(req).(*btapb.UpdateFunctionRequest)
	s.mu.Lock()
	s.updateReqs = append(s.updateReqs, cloned)
	fn := s.updateFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected UpdateFunction")
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

func (s *fakeFunctionAdminServer) GetOperation(ctx context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
	s.record(ctx, "GetOperation")
	cloned := proto.Clone(req).(*longrunningpb.GetOperationRequest)
	s.mu.Lock()
	s.getOpReqs = append(s.getOpReqs, cloned)
	fn := s.getOpFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected GetOperation")
}

func startFakeFunctionAdminGRPC(t *testing.T, srv *fakeFunctionAdminServer) *grpcFunctionAdminClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	grpcSrv := grpc.NewServer()
	btapb.RegisterBigtableInstanceAdminServer(grpcSrv, srv)
	longrunningpb.RegisterOperationsServer(grpcSrv, srv)
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
	return newGRPCFunctionAdminClient(conn, fastTestBackoff)
}

func writeTempWASMFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "module.wasm")
	if err := os.WriteFile(p, content, 0600); err != nil {
		t.Fatalf("writing temp wasm file failed: %v", err)
	}
	return p
}

func packOperationResponse(t *testing.T, name string, msg proto.Message) *longrunningpb.Operation {
	t.Helper()
	packed, err := anypb.New(msg)
	if err != nil {
		t.Fatalf("anypb.New failed: %v", err)
	}
	return &longrunningpb.Operation{
		Name:   name,
		Done:   true,
		Result: &longrunningpb.Operation_Response{Response: packed},
	}
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

func TestReadWASMSource_ExactMaxBoundary(t *testing.T) {
	exactMax := bytes.Repeat([]byte{0x61}, maxWASMSourceBytes)
	wasmPath := writeTempWASMFile(t, exactMax)
	got, err := readWASMSource(wasmPath)
	if err != nil {
		t.Fatalf("readWASMSource(10 MiB) unexpected error: %v", err)
	}
	if len(got) != maxWASMSourceBytes {
		t.Errorf("len(readWASMSource) = %d, want %d", len(got), maxWASMSourceBytes)
	}
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

func TestParseWASMFunctionArgs_Success(t *testing.T) {
	got, err := parseWASMFunctionArgs("payload:bytes, keyword:STRING, _window_1:ARRAY<INT32>")
	if err != nil {
		t.Fatalf("parseWASMFunctionArgs unexpected error: %v", err)
	}
	want := []*btapb.Function_Argument{
		{
			Name:     "payload",
			DataType: &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}},
		},
		{
			Name:     "keyword",
			DataType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}},
		},
		{
			Name: "_window_1",
			DataType: &btapb.Type{
				Kind: &btapb.Type_ArrayType{
					ArrayType: &btapb.Type_Array{
						ElementType: &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}},
					},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("parseWASMFunctionArgs mismatch (-want +got):\n%s", diff)
	}
}

func TestParseWASMFunctionArgs_Errors(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{name: "empty_spec", spec: "", wantSub: "at least one"},
		{name: "missing_colon", spec: "payload_BYTES", wantSub: "expected <name>:<type>"},
		{name: "empty_arg_name", spec: ":BYTES", wantSub: "invalid argument name"},
		{name: "empty_arg_type", spec: "payload:", wantSub: "must not be empty"},
		{name: "trailing_comma", spec: "payload:BYTES,", wantSub: "empty argument segment"},
		{name: "leading_comma", spec: ",payload:BYTES", wantSub: "empty argument segment"},
		{name: "middle_empty_segment", spec: "a:INT64,,b:STRING", wantSub: "empty argument segment"},
		{name: "duplicate_exact_arg_name", spec: "x:INT64,x:STRING", wantSub: "duplicate argument name"},
		{name: "duplicate_case_variant_arg_name", spec: "x:INT64,X:STRING", wantSub: "invalid argument name"},
		{name: "invalid_arg_identifier_leading_digit", spec: "1arg:INT64", wantSub: "must match [a-z_][a-z0-9_]*"},
		{name: "invalid_arg_identifier_hyphen", spec: "my-arg:INT64", wantSub: "must match [a-z_][a-z0-9_]*"},
		{name: "overlong_arg_identifier", spec: strings.Repeat("a", maxFunctionIdentifierLen+1) + ":INT64", wantSub: "exceeds maximum length"},
		{name: "any_type_rejected_for_wasm", spec: "payload:ANY", wantSub: "unsupported WASM function type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWASMFunctionArgs(tc.spec)
			if err == nil {
				t.Fatalf("parseWASMFunctionArgs(%q) = %v, want error containing %q", tc.spec, got, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseWASMFunctionArgs(%q) error = %q, want substring %q", tc.spec, err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParseSQLFunctionType_And_Args(t *testing.T) {
	dateType := &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}}
	arrayDateType := &btapb.Type{Kind: &btapb.Type_ArrayType{ArrayType: &btapb.Type_Array{ElementType: dateType}}}

	gotDate, err := parseSQLFunctionType("DATE")
	if err != nil {
		t.Fatalf("parseSQLFunctionType(\"DATE\") unexpected error: %v", err)
	}
	if diff := cmp.Diff(dateType, gotDate, protocmp.Transform()); diff != "" {
		t.Errorf("parseSQLFunctionType(\"DATE\") mismatch (-want +got):\n%s", diff)
	}

	gotArrayDate, err := parseSQLFunctionType("ARRAY<DATE>")
	if err != nil {
		t.Fatalf("parseSQLFunctionType(\"ARRAY<DATE>\") unexpected error: %v", err)
	}
	if diff := cmp.Diff(arrayDateType, gotArrayDate, protocmp.Transform()); diff != "" {
		t.Errorf("parseSQLFunctionType(\"ARRAY<DATE>\") mismatch (-want +got):\n%s", diff)
	}

	typeErrTests := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{name: "empty", spec: " ", wantSub: "must not be empty"},
		{name: "unsupported_scalar", spec: "JSON", wantSub: "unsupported SQL function type"},
		{name: "unsupported_array_element", spec: "ARRAY<JSON>", wantSub: "unsupported SQL function type"},
		{name: "unclosed_array", spec: "ARRAY<DATE", wantSub: "missing closing '>'"},
		{name: "nested_array", spec: "ARRAY<ARRAY<DATE>>", wantSub: "nested array type"},
	}
	for _, tc := range typeErrTests {
		t.Run("type_err_"+tc.name, func(t *testing.T) {
			if got, err := parseSQLFunctionType(tc.spec); err == nil {
				t.Fatalf("parseSQLFunctionType(%q) = %v, want error containing %q", tc.spec, got, tc.wantSub)
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseSQLFunctionType(%q) error = %q, want substring %q", tc.spec, err.Error(), tc.wantSub)
			}
		})
	}

	gotArgs, err := parseSQLFunctionArgs("d:DATE, arr:ARRAY<DATE>, untyped_arg, any_arg:ANY")
	if err != nil {
		t.Fatalf("parseSQLFunctionArgs unexpected error: %v", err)
	}
	wantArgs := []*btapb.Function_Argument{
		{Name: "d", DataType: dateType},
		{Name: "arr", DataType: arrayDateType},
		{Name: "untyped_arg"},
		{Name: "any_arg"},
	}
	if diff := cmp.Diff(wantArgs, gotArgs, protocmp.Transform()); diff != "" {
		t.Errorf("parseSQLFunctionArgs mismatch (-want +got):\n%s", diff)
	}

	argErrTests := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{name: "empty_spec", spec: "  ", wantSub: "SQL function args must not be empty"},
		{name: "empty_segment", spec: "d:DATE,", wantSub: "empty argument segment"},
		{name: "invalid_untyped_identifier", spec: "BadArg", wantSub: "must match [a-z_][a-z0-9_]*"},
		{name: "invalid_typed_identifier", spec: "BadArg:DATE", wantSub: "must match [a-z_][a-z0-9_]*"},
		{name: "empty_arg_type", spec: "d:", wantSub: "must not be empty"},
		{name: "unsupported_arg_type", spec: "d:JSON", wantSub: "unsupported SQL function type"},
		{name: "duplicate_arg_name", spec: "d:DATE,d:INT64", wantSub: "duplicate argument name"},
		{name: "duplicate_untyped_arg_name", spec: "d,d", wantSub: "duplicate argument name"},
	}
	for _, tc := range argErrTests {
		t.Run("arg_err_"+tc.name, func(t *testing.T) {
			if got, err := parseSQLFunctionArgs(tc.spec); err == nil {
				t.Fatalf("parseSQLFunctionArgs(%q) = %v, want error containing %q", tc.spec, got, tc.wantSub)
			} else if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("parseSQLFunctionArgs(%q) error = %q, want substring %q", tc.spec, err.Error(), tc.wantSub)
			}
		})
	}
}

// UT-2: Client-side createfunction boundary rejections must fail fast with zero outbound gRPC calls.
func TestRunCreateFunction_ClientSideBoundaryRejections_ZeroRPCs(t *testing.T) {
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)

	validWASM := writeTempWASMFile(t, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	emptyWASM := writeTempWASMFile(t, nil)
	oversizedWASM := writeTempWASMFile(t, bytes.Repeat([]byte{0x01}, maxWASMSourceBytes+1))
	missingWASM := filepath.Join(t.TempDir(), "does_not_exist.wasm")

	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{
			name:    "create_date_arg_rejected",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=d:DATE", "return-type=BOOL"},
			wantSub: "unsupported WASM function type",
		},
		{
			name:    "create_array_date_return_rejected",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=x:INT64", "return-type=ARRAY<DATE>"},
			wantSub: "unsupported WASM function type",
		},
		{
			name:    "create_no_options_one_token",
			args:    []string{"my_fn"},
			wantSub: "usage: cbt createfunction",
		},
		{
			name:    "create_both_wasm_file_and_body_rejected",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "body=1 + 1"},
			wantSub: "cannot specify both \"wasm-file\" and \"body\"",
		},
		{
			name:    "create_missing_both_wasm_file_and_body_rejected",
			args:    []string{"my_fn", "args=x:INT64", "return-type=INT64"},
			wantSub: "missing required option \"wasm-file\" or \"body\"",
		},
		{
			name:    "create_missing_args_option_three_tokens",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "return-type=BOOL"},
			wantSub: "missing required option \"args\"",
		},
		{
			name:    "create_missing_args_option_four_tokens",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "return-type=BOOL", "deletion-protection=true"},
			wantSub: "missing required option \"args\"",
		},
		{
			name:    "create_missing_return_type_option_rejected",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=x:INT64"},
			wantSub: "missing required option \"return-type\"",
		},
		{
			name:    "create_empty_args_value",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=", "return-type=BOOL"},
			wantSub: "option \"args\" must not be empty",
		},
		{
			name:    "create_duplicate_arg_names_exact",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=x:INT64,x:STRING", "return-type=BOOL"},
			wantSub: "duplicate argument name",
		},
		{
			name:    "create_duplicate_arg_names_case_insensitive",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=x:INT64,X:STRING", "return-type=BOOL"},
			wantSub: "must match [a-z_][a-z0-9_]*",
		},
		{
			name:    "create_invalid_function_id_uppercase",
			args:    []string{"BadFunc", "wasm-file=" + validWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "invalid function-id",
		},
		{
			name:    "create_invalid_function_id_hyphen",
			args:    []string{"bad-func", "wasm-file=" + validWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "invalid function-id",
		},
		{
			name:    "create_overlong_function_id",
			args:    []string{strings.Repeat("f", maxFunctionIdentifierLen+1), "wasm-file=" + validWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "exceeds maximum length",
		},
		{
			name:    "create_empty_wasm_file",
			args:    []string{"my_fn", "wasm-file=" + emptyWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "must not be empty",
		},
		{
			name:    "create_missing_wasm_file",
			args:    []string{"my_fn", "wasm-file=" + missingWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "reading wasm-file",
		},
		{
			name:    "create_oversized_wasm_file",
			args:    []string{"my_fn", "wasm-file=" + oversizedWASM, "args=x:INT64", "return-type=BOOL"},
			wantSub: "exceeds maximum allowed size",
		},
		{
			name:    "create_invalid_deletion_protection_bool",
			args:    []string{"my_fn", "wasm-file=" + validWASM, "args=x:INT64", "return-type=BOOL", "deletion-protection=not-a-bool"},
			wantSub: "parsing deletion-protection",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			beforeCalls := srv.totalCalls()
			var out bytes.Buffer
			err := runCreateFunction(t.Context(), api, "proj", "inst", &out, tc.args...)
			if err == nil {
				t.Fatalf("runCreateFunction(%v) succeeded with output %q, want error containing %q", tc.args, out.String(), tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("runCreateFunction(%v) error = %q, want substring %q", tc.args, err.Error(), tc.wantSub)
			}
			if out.Len() != 0 {
				t.Errorf("runCreateFunction(%v) output = %q, want empty output on error", tc.args, out.String())
			}
			if gotCalls := srv.totalCalls() - beforeCalls; gotCalls != 0 {
				t.Errorf("runCreateFunction(%v) outbound gRPC calls = %d, want 0", tc.args, gotCalls)
			}
		})
	}
}

// UT-3: Verify createfunction request fields, routing headers, auth token preservation,
// multi-poll GetOperation LRO loop, and unpacked Function confirmation output.
func TestRunCreateFunction_SuccessWithMultiPollLRO(t *testing.T) {
	wasmPayload := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0xaa, 0xbb}
	wasmPath := writeTempWASMFile(t, wasmPayload)
	opName := "operations/projects/my-proj/instances/my-inst/functions/doc_has_keyword/locations/us-central1-a/operations/op-42"

	createdFn := &btapb.Function{
		Name:               "projects/my-proj/instances/my-inst/functions/doc_has_keyword",
		Etag:               "W/\"v1\"",
		DeletionProtection: true,
		FunctionType: &btapb.Function_WasmFunction_{
			WasmFunction: &btapb.Function_WasmFunction{
				Arguments: []*btapb.Function_Argument{
					{Name: "payload", DataType: &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}},
					{Name: "keyword", DataType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}},
				},
				ReturnType:   &btapb.Type{Kind: &btapb.Type_BoolType{BoolType: &btapb.Type_Bool{}}},
				SourceSha256: []byte{0xde, 0xad, 0xbe, 0xef},
			},
		},
	}

	var pollAttempt int
	srv := &fakeFunctionAdminServer{
		createFn: func(_ context.Context, _ *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
			return &longrunningpb.Operation{Name: opName, Done: false}, nil
		},
		getOpFn: func(_ context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
			pollAttempt++
			if pollAttempt < 3 {
				return &longrunningpb.Operation{Name: req.GetName(), Done: false}, nil
			}
			return packOperationResponse(t, req.GetName(), createdFn), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-goog-iam-authorization-token", "iam-token-xyz")
	var out bytes.Buffer
	err := runCreateFunction(
		ctx,
		api,
		"my-proj",
		"my-inst",
		&out,
		"doc_has_keyword",
		"wasm-file="+wasmPath,
		"args=payload:BYTES,keyword:STRING",
		"return-type=BOOL",
		"deletion-protection=true",
	)
	if err != nil {
		t.Fatalf("runCreateFunction unexpected error: %v", err)
	}

	if len(srv.createReqs) != 1 {
		t.Fatalf("CreateFunction call count = %d, want 1", len(srv.createReqs))
	}
	wantReq := &btapb.CreateFunctionRequest{
		Parent:     "projects/my-proj/instances/my-inst",
		FunctionId: "doc_has_keyword",
		Function: &btapb.Function{
			DeletionProtection: true,
			FunctionType: &btapb.Function_WasmFunction_{
				WasmFunction: &btapb.Function_WasmFunction{
					Arguments:  createdFn.GetWasmFunction().GetArguments(),
					ReturnType: createdFn.GetWasmFunction().GetReturnType(),
					Source:     wasmPayload,
				},
			},
		},
	}
	if diff := cmp.Diff(wantReq, srv.createReqs[0], protocmp.Transform()); diff != "" {
		t.Errorf("CreateFunctionRequest mismatch (-want +got):\n%s", diff)
	}

	createCalls := srv.callsFor("CreateFunction")
	wantCreateParams := []string{"parent=" + url.QueryEscape("projects/my-proj/instances/my-inst")}
	if diff := cmp.Diff(wantCreateParams, createCalls[0].md.Get(xGoogRequestParamsHeader)); diff != "" {
		t.Errorf("CreateFunction x-goog-request-params mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"projects/my-proj"}, createCalls[0].md.Get(googleCloudResourcePrefixHeader)); diff != "" {
		t.Errorf("CreateFunction google-cloud-resource-prefix mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"iam-token-xyz"}, createCalls[0].md.Get("x-goog-iam-authorization-token")); diff != "" {
		t.Errorf("CreateFunction x-goog-iam-authorization-token mismatch (-want +got):\n%s", diff)
	}

	if gotPolls := len(srv.getOpReqs); gotPolls != 3 {
		t.Fatalf("GetOperation poll count = %d, want 3", gotPolls)
	}
	for i, call := range srv.callsFor("GetOperation") {
		wantPollParams := []string{"name=" + url.QueryEscape(opName)}
		if diff := cmp.Diff(wantPollParams, call.md.Get(xGoogRequestParamsHeader)); diff != "" {
			t.Errorf("GetOperation[%d] x-goog-request-params mismatch (-want +got):\n%s", i, diff)
		}
		if diff := cmp.Diff([]string{"projects/my-proj"}, call.md.Get(googleCloudResourcePrefixHeader)); diff != "" {
			t.Errorf("GetOperation[%d] google-cloud-resource-prefix mismatch (-want +got):\n%s", i, diff)
		}
		if diff := cmp.Diff([]string{"iam-token-xyz"}, call.md.Get("x-goog-iam-authorization-token")); diff != "" {
			t.Errorf("GetOperation[%d] x-goog-iam-authorization-token mismatch (-want +got):\n%s", i, diff)
		}
	}

	wantOut := "Name: projects/my-proj/instances/my-inst/functions/doc_has_keyword\nSignature: (payload:BYTES, keyword:STRING) -> BOOL\n"
	if out.String() != wantOut {
		t.Errorf("runCreateFunction output = %q, want %q", out.String(), wantOut)
	}
}

func TestRunCreateFunction_SQLScalarFunction(t *testing.T) {
	fnName := "projects/my-proj/instances/my-inst/functions/add_days"
	createdFn := &btapb.Function{
		Name: fnName,
		FunctionType: &btapb.Function_SqlScalarFunction_{
			SqlScalarFunction: &btapb.Function_SqlScalarFunction{
				Arguments: []*btapb.Function_Argument{
					{Name: "d", DataType: &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}}},
					{Name: "delta", DataType: nil},
				},
				ReturnType:   &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}},
				FunctionBody: "DATE_ADD(d, INTERVAL delta DAY)",
			},
		},
	}

	srv := &fakeFunctionAdminServer{
		createFn: func(_ context.Context, _ *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, "operations/op-sql-create", createdFn), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var createOut bytes.Buffer
	err := runCreateFunction(
		t.Context(),
		api,
		"my-proj",
		"my-inst",
		&createOut,
		"add_days",
		"body=DATE_ADD(d, INTERVAL delta DAY)",
		"args=d:DATE,delta",
		"return-type=DATE",
	)
	if err != nil {
		t.Fatalf("runCreateFunction(SQL) unexpected error: %v", err)
	}
	wantCreateReq := &btapb.CreateFunctionRequest{
		Parent:     "projects/my-proj/instances/my-inst",
		FunctionId: "add_days",
		Function: &btapb.Function{
			FunctionType: createdFn.GetFunctionType(),
		},
	}
	if diff := cmp.Diff(wantCreateReq, srv.createReqs[0], protocmp.Transform()); diff != "" {
		t.Errorf("CreateFunctionRequest(SQL) mismatch (-want +got):\n%s", diff)
	}
	wantCreateOut := "Name: " + fnName + "\nSignature: (d:DATE, delta:ANY) -> DATE\n"
	if createOut.String() != wantCreateOut {
		t.Errorf("runCreateFunction(SQL) output = %q, want %q", createOut.String(), wantCreateOut)
	}
}

// Verify SQL functions may omit args and return-type, and that return-type=ANY
// leaves the return type unset.
func TestRunCreateFunction_SQLOptionalArgsAndReturnType(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "body_only", args: []string{"body=1 + 1"}},
		{name: "return_type_any", args: []string{"body=1 + 1", "return-type=ANY"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &fakeFunctionAdminServer{
				createFn: func(_ context.Context, req *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
					return packOperationResponse(t, "operations/op-sql-min", req.GetFunction()), nil
				},
			}
			api := startFakeFunctionAdminGRPC(t, srv)
			args := append([]string{"const_two"}, tc.args...)
			if err := runCreateFunction(t.Context(), api, "my-proj", "my-inst", io.Discard, args...); err != nil {
				t.Fatalf("runCreateFunction(%v) unexpected error: %v", args, err)
			}
			wantReq := &btapb.CreateFunctionRequest{
				Parent:     "projects/my-proj/instances/my-inst",
				FunctionId: "const_two",
				Function: &btapb.Function{
					FunctionType: &btapb.Function_SqlScalarFunction_{
						SqlScalarFunction: &btapb.Function_SqlScalarFunction{FunctionBody: "1 + 1"},
					},
				},
			}
			if diff := cmp.Diff(wantReq, srv.createReqs[0], protocmp.Transform()); diff != "" {
				t.Errorf("CreateFunctionRequest(%v) mismatch (-want +got):\n%s", args, diff)
			}
		})
	}
}

// UT-4: Verify immediate gRPC errors and terminal LRO Operation_Error propagation on CreateFunction.
func TestRunCreateFunction_ImmediateAndLROErrorPropagation(t *testing.T) {
	wasmPath := writeTempWASMFile(t, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	tests := []struct {
		name      string
		rpcErr    error
		initialOp *longrunningpb.Operation
		polledOp  *longrunningpb.Operation
		wantCode  codes.Code
		wantSub   string
	}{
		{
			name:     "create_immediate_already_exists",
			rpcErr:   status.Error(codes.AlreadyExists, "function already exists"),
			wantCode: codes.AlreadyExists,
			wantSub:  "function already exists",
		},
		{
			name:     "create_immediate_invalid_argument",
			rpcErr:   status.Error(codes.InvalidArgument, "unsupported encoding"),
			wantCode: codes.InvalidArgument,
			wantSub:  "unsupported encoding",
		},
		{
			name:     "create_immediate_permission_denied",
			rpcErr:   status.Error(codes.PermissionDenied, "caller lacks bigtable.functions.create"),
			wantCode: codes.PermissionDenied,
			wantSub:  "caller lacks bigtable.functions.create",
		},
		{
			name:      "create_lro_aot_compile_failure",
			initialOp: &longrunningpb.Operation{Name: "operations/op-aot", Done: false},
			polledOp: &longrunningpb.Operation{
				Name: "operations/op-aot",
				Done: true,
				Result: &longrunningpb.Operation_Error{
					Error: &statuspb.Status{
						Code:    int32(codes.InvalidArgument),
						Message: "WASM AOT compilation failed in us-central1-a",
					},
				},
			},
			wantCode: codes.InvalidArgument,
			wantSub:  "WASM AOT compilation failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &fakeFunctionAdminServer{
				createFn: func(_ context.Context, _ *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
					return tc.initialOp, tc.rpcErr
				},
				getOpFn: func(_ context.Context, _ *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
					return tc.polledOp, nil
				},
			}
			api := startFakeFunctionAdminGRPC(t, srv)

			var out bytes.Buffer
			err := runCreateFunction(t.Context(), api, "proj", "inst", &out,
				"doc_fn", "wasm-file="+wasmPath, "args=x:INT64", "return-type=INT64")
			if err == nil {
				t.Fatalf("got nil error with output %q, want code %v", out.String(), tc.wantCode)
			}
			if gotCode := status.Code(err); gotCode != tc.wantCode {
				t.Errorf("status.Code(err) = %v, want %v (err: %v)", gotCode, tc.wantCode, err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
			if out.Len() != 0 {
				t.Errorf("output = %q, want empty output on error", out.String())
			}
		})
	}
}

// UT-5a: Verify initial Done: true Operation skips GetOperation polling.
func TestWaitFunctionOperation_ImmediateDoneSkipsGetOperation(t *testing.T) {
	fnProto := &btapb.Function{
		Name: "projects/p/instances/i/functions/fast_fn",
		FunctionType: &btapb.Function_WasmFunction_{
			WasmFunction: &btapb.Function_WasmFunction{
				Arguments:  []*btapb.Function_Argument{{Name: "a", DataType: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}}},
				ReturnType: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}},
			},
		},
	}
	srv := &fakeFunctionAdminServer{
		createFn: func(_ context.Context, _ *btapb.CreateFunctionRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, "operations/immediate", fnProto), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)
	api.backoff = gax.Backoff{}

	wasmPath := writeTempWASMFile(t, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	var out bytes.Buffer
	if err := runCreateFunction(t.Context(), api, "p", "i", &out,
		"fast_fn", "wasm-file="+wasmPath, "args=a:INT64", "return-type=INT64"); err != nil {
		t.Fatalf("runCreateFunction unexpected error: %v", err)
	}
	if gotPolls := len(srv.getOpReqs); gotPolls != 0 {
		t.Errorf("GetOperation call count = %d, want 0 for initial Done: true", gotPolls)
	}
}

// UT-5c: Verify nil, zero-code, and malformed Operation payloads return clean errors without panic.
func TestWaitFunctionOperation_MalformedOperationsReturnCleanErrors(t *testing.T) {
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)
	wrongAny, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		t.Fatalf("anypb.New failed: %v", err)
	}

	cases := []struct {
		name    string
		op      *longrunningpb.Operation
		wantSub string
	}{
		{
			name:    "nil_operation",
			op:      nil,
			wantSub: "nil long-running operation",
		},
		{
			name:    "pending_operation_missing_name",
			op:      &longrunningpb.Operation{Done: false, Name: ""},
			wantSub: "missing name",
		},
		{
			name: "done_with_nil_error_proto",
			op: &longrunningpb.Operation{
				Name:   "operations/op-nil-err",
				Done:   true,
				Result: &longrunningpb.Operation_Error{Error: nil},
			},
			wantSub: "invalid error status",
		},
		{
			name: "done_with_zero_code_error_proto",
			op: &longrunningpb.Operation{
				Name:   "operations/op-zero-code",
				Done:   true,
				Result: &longrunningpb.Operation_Error{Error: &statuspb.Status{Code: 0, Message: "ok"}},
			},
			wantSub: "invalid error status",
		},
		{
			name: "done_with_nil_response_any",
			op: &longrunningpb.Operation{
				Name:   "operations/op-nil-resp",
				Done:   true,
				Result: &longrunningpb.Operation_Response{Response: nil},
			},
			wantSub: "nil response payload",
		},
		{
			name: "done_with_mismatched_any_type",
			op: &longrunningpb.Operation{
				Name:   "operations/op-bad-any",
				Done:   true,
				Result: &longrunningpb.Operation_Response{Response: wrongAny},
			},
			wantSub: "unpacking operation",
		},
		{
			name: "done_without_result_oneof",
			op: &longrunningpb.Operation{
				Name: "operations/op-empty-done",
				Done: true,
			},
			wantSub: "without error or response result",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := api.WaitFunctionOperation(t.Context(), tc.op)
			if err == nil {
				t.Fatalf("WaitFunctionOperation(%v) = %v, want error containing %q", tc.op, got, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("WaitFunctionOperation(%v) error = %q, want substring %q", tc.op, err.Error(), tc.wantSub)
			}
		})
	}
}

// UT-5d: Verify expiring context deadlines (-timeout) abort LRO backoff sleeps promptly.
func TestWaitFunctionOperation_ContextDeadlineAbortsBackoffSleep(t *testing.T) {
	srv := &fakeFunctionAdminServer{
		getOpFn: func(_ context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
			return &longrunningpb.Operation{Name: req.GetName(), Done: false}, nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)
	api.backoff = gax.Backoff{
		Initial:    time.Hour,
		Max:        time.Hour,
		Multiplier: 2.0,
	}

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Millisecond))
	t.Cleanup(cancel)

	_, err := api.WaitFunctionOperation(ctx, &longrunningpb.Operation{Name: "operations/slow-op", Done: false})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitFunctionOperation error = %v, want context.DeadlineExceeded", err)
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

// UT-2 & UT-7c: Client-side updatefunction boundary rejections, empty options, and empty update masks must fail fast with zero outbound gRPC calls.
func TestRunUpdateFunction_ClientSideBoundaryRejections_ZeroRPCs(t *testing.T) {
	srv := &fakeFunctionAdminServer{}
	api := startFakeFunctionAdminGRPC(t, srv)

	oversizedWASM := writeTempWASMFile(t, bytes.Repeat([]byte{0x01}, maxWASMSourceBytes+1))

	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{
			name:    "update_no_options",
			args:    []string{"doc_fn"},
			wantSub: "usage: cbt updatefunction",
		},
		{
			name:    "update_invalid_function_id",
			args:    []string{"BadFunc", "deletion-protection=true"},
			wantSub: "invalid function-id",
		},
		{
			name:    "update_empty_args_value",
			args:    []string{"my_fn", "args="},
			wantSub: "option \"args\" must not be empty",
		},
		{
			name:    "update_oversized_wasm_file",
			args:    []string{"my_fn", "wasm-file=" + oversizedWASM},
			wantSub: "exceeds maximum allowed size",
		},
		{
			name:    "update_invalid_deletion_protection_bool",
			args:    []string{"my_fn", "deletion-protection=not-a-bool"},
			wantSub: "parsing deletion-protection",
		},
		{
			name:    "update_args_without_wasm_file_rejected",
			args:    []string{"my_fn", "args=x:INT64"},
			wantSub: "requires specifying \"wasm-file\"",
		},
		{
			name:    "update_return_type_without_wasm_file_rejected",
			args:    []string{"my_fn", "return-type=INT64"},
			wantSub: "requires specifying \"wasm-file\"",
		},
		{
			name:    "update_body_option_rejected",
			args:    []string{"doc_fn", "deletion-protection=true", "body=1 + 1"},
			wantSub: "unknown arg key",
		},
		{
			name:    "update_only_etag_has_empty_mask",
			args:    []string{"doc_fn", "etag=W/\"v1\""},
			wantSub: "nothing to update",
		},
		{
			name:    "update_empty_etag_rejected",
			args:    []string{"doc_fn", "deletion-protection=true", "etag="},
			wantSub: "option \"etag\" must not be empty",
		},
		{
			name:    "update_empty_wasm_file_rejected",
			args:    []string{"doc_fn", "wasm-file="},
			wantSub: "option \"wasm-file\" must not be empty",
		},
		{
			name:    "update_empty_return_type_rejected",
			args:    []string{"doc_fn", "return-type="},
			wantSub: "option \"return-type\" must not be empty",
		},
		{
			name:    "update_empty_deletion_protection_rejected",
			args:    []string{"doc_fn", "deletion-protection="},
			wantSub: "option \"deletion-protection\" must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			beforeCalls := srv.totalCalls()
			var out bytes.Buffer
			err := runUpdateFunction(t.Context(), api, "proj", "inst", &out, tc.args...)
			if err == nil {
				t.Fatalf("runUpdateFunction(%v) succeeded with output %q, want error containing %q", tc.args, out.String(), tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("runUpdateFunction(%v) error = %q, want substring %q", tc.args, err.Error(), tc.wantSub)
			}
			if out.Len() != 0 {
				t.Errorf("runUpdateFunction(%v) output = %q, want empty output on error", tc.args, out.String())
			}
			if gotCalls := srv.totalCalls() - beforeCalls; gotCalls != 0 {
				t.Errorf("runUpdateFunction(%v) outbound gRPC calls = %d, want 0", tc.args, gotCalls)
			}
		})
	}
}

// UT-4: Verify immediate gRPC errors and terminal LRO Operation_Error propagation on UpdateFunction.
func TestRunUpdateFunction_ImmediateAndLROErrorPropagation(t *testing.T) {
	tests := []struct {
		name      string
		rpcErr    error
		initialOp *longrunningpb.Operation
		polledOp  *longrunningpb.Operation
		wantCode  codes.Code
		wantSub   string
	}{
		{
			name:     "update_immediate_permission_denied",
			rpcErr:   status.Error(codes.PermissionDenied, "caller lacks bigtable.functions.update"),
			wantCode: codes.PermissionDenied,
			wantSub:  "caller lacks bigtable.functions.update",
		},
		{
			name:      "update_lro_rollback_failure",
			initialOp: &longrunningpb.Operation{Name: "operations/op-upd", Done: false},
			polledOp: &longrunningpb.Operation{
				Name: "operations/op-upd",
				Done: true,
				Result: &longrunningpb.Operation_Error{
					Error: &statuspb.Status{
						Code:    int32(codes.Internal),
						Message: "zonal module rollback failed",
					},
				},
			},
			wantCode: codes.Internal,
			wantSub:  "zonal module rollback failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &fakeFunctionAdminServer{
				updateFn: func(_ context.Context, _ *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
					return tc.initialOp, tc.rpcErr
				},
				getOpFn: func(_ context.Context, _ *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
					return tc.polledOp, nil
				},
			}
			api := startFakeFunctionAdminGRPC(t, srv)

			var out bytes.Buffer
			err := runUpdateFunction(t.Context(), api, "proj", "inst", &out, "doc_fn", "deletion-protection=true")
			if err == nil {
				t.Fatalf("got nil error with output %q, want code %v", out.String(), tc.wantCode)
			}
			if gotCode := status.Code(err); gotCode != tc.wantCode {
				t.Errorf("status.Code(err) = %v, want %v (err: %v)", gotCode, tc.wantCode, err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
			if out.Len() != 0 {
				t.Errorf("output = %q, want empty output on error", out.String())
			}
		})
	}
}

// UT-5b: Verify UpdateFunction LRO polling does not leak function.name= into GetOperation metadata.
func TestWaitFunctionOperation_UpdateDoesNotLeakFunctionNameHeader(t *testing.T) {
	fnProto := &btapb.Function{Name: "projects/p/instances/i/functions/upd_fn"}
	opName := "operations/upd-op-1"
	srv := &fakeFunctionAdminServer{
		updateFn: func(_ context.Context, _ *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
			return &longrunningpb.Operation{Name: opName, Done: false}, nil
		},
		getOpFn: func(_ context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, req.GetName(), fnProto), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	if err := runUpdateFunction(t.Context(), api, "p", "i", &out, "upd_fn", "deletion-protection=true"); err != nil {
		t.Fatalf("runUpdateFunction unexpected error: %v", err)
	}
	pollCalls := srv.callsFor("GetOperation")
	if len(pollCalls) != 1 {
		t.Fatalf("GetOperation call count = %d, want 1", len(pollCalls))
	}
	wantParams := []string{"name=" + url.QueryEscape(opName)}
	if diff := cmp.Diff(wantParams, pollCalls[0].md.Get(xGoogRequestParamsHeader)); diff != "" {
		t.Errorf("GetOperation x-goog-request-params mismatch (-want +got):\n%s", diff)
	}
}

// UT-6: Verify updatefunction partial source-only FieldMask, routing headers, and LRO polling.
func TestRunUpdateFunction_PartialSourceOnlyUpdate(t *testing.T) {
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasmPath := writeTempWASMFile(t, wasmBytes)
	fnName := "projects/my-proj/instances/my-inst/functions/doc_extract_snippet"

	srv := &fakeFunctionAdminServer{
		updateFn: func(_ context.Context, _ *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
			return &longrunningpb.Operation{Name: "operations/op-src", Done: false}, nil
		},
		getOpFn: func(_ context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, req.GetName(), &btapb.Function{
				Name: fnName,
				FunctionType: &btapb.Function_WasmFunction_{
					WasmFunction: &btapb.Function_WasmFunction{
						Arguments:  []*btapb.Function_Argument{{Name: "doc", DataType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}}},
						ReturnType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}},
					},
				},
			}), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	err := runUpdateFunction(t.Context(), api, "my-proj", "my-inst", &out,
		"doc_extract_snippet", "wasm-file="+wasmPath)
	if err != nil {
		t.Fatalf("runUpdateFunction unexpected error: %v", err)
	}
	wantReq := &btapb.UpdateFunctionRequest{
		Function: &btapb.Function{
			Name: fnName,
			FunctionType: &btapb.Function_WasmFunction_{
				WasmFunction: &btapb.Function_WasmFunction{Source: wasmBytes},
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"wasm_function.source"}},
	}
	if diff := cmp.Diff(wantReq, srv.updateReqs[0], protocmp.Transform()); diff != "" {
		t.Errorf("UpdateFunctionRequest mismatch (-want +got):\n%s", diff)
	}
	updCalls := srv.callsFor("UpdateFunction")
	wantParams := []string{"function.name=" + url.QueryEscape(fnName)}
	if diff := cmp.Diff(wantParams, updCalls[0].md.Get(xGoogRequestParamsHeader)); diff != "" {
		t.Errorf("UpdateFunction x-goog-request-params mismatch (-want +got):\n%s", diff)
	}
	wantOut := "Name: " + fnName + "\nSignature: (doc:STRING) -> STRING\n"
	if out.String() != wantOut {
		t.Errorf("runUpdateFunction output = %q, want %q", out.String(), wantOut)
	}
}

// UT-7a: Verify deletion-protection=false with weak base64 etag updates top-level field without allocating WasmFunction.
func TestRunUpdateFunction_DeletionProtectionFalseWithEtagLeavesWASMNil(t *testing.T) {
	fnName := "projects/my-proj/instances/my-inst/functions/doc_extract_snippet"
	weakEtag := `W/"/v1/etag=YWJjZA=="`
	srv := &fakeFunctionAdminServer{
		updateFn: func(_ context.Context, _ *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, "operations/op-dp", &btapb.Function{
				Name:               fnName,
				DeletionProtection: false,
				FunctionType: &btapb.Function_SqlScalarFunction_{
					SqlScalarFunction: &btapb.Function_SqlScalarFunction{FunctionBody: "1 + 1"},
				},
			}), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	err := runUpdateFunction(t.Context(), api, "my-proj", "my-inst", &out,
		"doc_extract_snippet", "deletion-protection=false", "etag="+weakEtag)
	if err != nil {
		t.Fatalf("runUpdateFunction unexpected error: %v", err)
	}
	gotReq := srv.updateReqs[0]
	if gotReq.GetFunction().GetFunctionType() != nil {
		t.Errorf("UpdateFunctionRequest.Function.FunctionType = %T, want nil when updating only deletion_protection", gotReq.GetFunction().GetFunctionType())
	}
	wantReq := &btapb.UpdateFunctionRequest{
		Function: &btapb.Function{
			Name:               fnName,
			DeletionProtection: false,
			Etag:               weakEtag,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"deletion_protection"}},
	}
	if diff := cmp.Diff(wantReq, gotReq, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateFunctionRequest mismatch (-want +got):\n%s", diff)
	}
}

// UT-6 & UT-7b: Verify full FieldMask path ordering and etag forwarding on updatefunction.
func TestRunUpdateFunction_AllFieldsUpdateMaskOrderAndValues(t *testing.T) {
	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	wasmPath := writeTempWASMFile(t, wasmBytes)

	srv := &fakeFunctionAdminServer{
		updateFn: func(_ context.Context, req *btapb.UpdateFunctionRequest) (*longrunningpb.Operation, error) {
			return packOperationResponse(t, "operations/op-all", req.GetFunction()), nil
		},
	}
	api := startFakeFunctionAdminGRPC(t, srv)

	var out bytes.Buffer
	err := runUpdateFunction(t.Context(), api, "my-proj", "my-inst", &out,
		"doc_extract_snippet",
		"wasm-file="+wasmPath,
		"args=x:INT64",
		"return-type=STRING",
		"deletion-protection=true",
		"etag=YWJjZA==",
	)
	if err != nil {
		t.Fatalf("runUpdateFunction unexpected error: %v", err)
	}
	wantMask := []string{
		"wasm_function.source",
		"wasm_function.arguments",
		"wasm_function.return_type",
		"deletion_protection",
	}
	if diff := cmp.Diff(wantMask, srv.updateReqs[0].GetUpdateMask().GetPaths()); diff != "" {
		t.Errorf("UpdateMask.Paths mismatch (-want +got):\n%s", diff)
	}
	wantFn := &btapb.Function{
		Name:               "projects/my-proj/instances/my-inst/functions/doc_extract_snippet",
		Etag:               "YWJjZA==",
		DeletionProtection: true,
		FunctionType: &btapb.Function_WasmFunction_{
			WasmFunction: &btapb.Function_WasmFunction{
				Source:     wasmBytes,
				Arguments:  []*btapb.Function_Argument{{Name: "x", DataType: &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}}},
				ReturnType: &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}},
			},
		},
	}
	if diff := cmp.Diff(wantFn, srv.updateReqs[0].GetFunction(), protocmp.Transform()); diff != "" {
		t.Errorf("UpdateFunctionRequest.Function mismatch (-want +got):\n%s", diff)
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
	"createfunction",
	"deletefunction",
	"getfunction",
	"listfunctions",
	"updatefunction",
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
