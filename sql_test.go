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
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/bigtable"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"
	datepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var castagnoliCRC32 = crc32.MakeTable(crc32.Castagnoli)

type fakeSQLDataServer struct {
	btpb.UnimplementedBigtableServer

	prepareFn func(context.Context, *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error)
	executeFn func(*btpb.ExecuteQueryRequest, btpb.Bigtable_ExecuteQueryServer) error
}

func (s *fakeSQLDataServer) PrepareQuery(ctx context.Context, req *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
	if s.prepareFn != nil {
		return s.prepareFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "unexpected PrepareQuery")
}

func (s *fakeSQLDataServer) ExecuteQuery(req *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
	if s.executeFn != nil {
		return s.executeFn(req, stream)
	}
	return status.Error(codes.Unimplemented, "unexpected ExecuteQuery")
}

func startFakeSQLBigtableClient(t *testing.T, srv *fakeSQLDataServer) *bigtable.Client {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	grpcSrv := grpc.NewServer()
	btpb.RegisterBigtableServer(grpcSrv, srv)
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

	btClient, err := bigtable.NewClient(t.Context(), "proj", "inst", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("bigtable.NewClient failed: %v", err)
	}
	t.Cleanup(func() {
		_ = btClient.Close()
	})
	return btClient
}

func makeProtoRowsBatchResponse(t *testing.T, resumeToken string, values ...*btpb.Value) *btpb.ExecuteQueryResponse {
	t.Helper()
	raw, err := proto.Marshal(&btpb.ProtoRows{Values: values})
	if err != nil {
		t.Fatalf("proto.Marshal(ProtoRows) failed: %v", err)
	}
	csum := crc32.Checksum(raw, castagnoliCRC32)
	return &btpb.ExecuteQueryResponse{
		Response: &btpb.ExecuteQueryResponse_Results{
			Results: &btpb.PartialResultSet{
				PartialRows: &btpb.PartialResultSet_ProtoRowsBatch{
					ProtoRowsBatch: &btpb.ProtoRowsBatch{BatchData: raw},
				},
				BatchChecksum: &csum,
				ResumeToken:   []byte(resumeToken),
			},
		},
	}
}

func makePrepareQueryResponse(cols ...*btpb.ColumnMetadata) *btpb.PrepareQueryResponse {
	return &btpb.PrepareQueryResponse{
		PreparedQuery: []byte("compiled-plan"),
		ValidUntil:    timestamppb.New(time.Now().Add(time.Hour)),
		Metadata: &btpb.ResultSetMetadata{
			Schema: &btpb.ResultSetMetadata_ProtoSchema{
				ProtoSchema: &btpb.ProtoSchema{Columns: cols},
			},
		},
	}
}

// UT-11a: Verify runSQL renders complete ASCII table across multi-batch SQL types.
func TestRunSQL_ValidQueryRendersCompleteTableAcrossSQLTypes(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "doc_id", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
				&btpb.ColumnMetadata{Name: "matched", Type: &btpb.Type{Kind: &btpb.Type_BoolType{BoolType: &btpb.Type_Bool{}}}},
				&btpb.ColumnMetadata{Name: "score", Type: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}}},
				&btpb.ColumnMetadata{Name: "payload", Type: &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}}},
				&btpb.ColumnMetadata{
					Name: "tags",
					Type: &btpb.Type{
						Kind: &btpb.Type_ArrayType{
							ArrayType: &btpb.Type_Array{
								ElementType: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}},
							},
						},
					},
				},
				&btpb.ColumnMetadata{Name: "created_on", Type: &btpb.Type{Kind: &btpb.Type_DateType{DateType: &btpb.Type_Date{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			batch1 := makeProtoRowsBatchResponse(t, "tok-1",
				&btpb.Value{Kind: &btpb.Value_StringValue{StringValue: "doc#1 with long unwrapped text value exceeding thirty chars"}},
				&btpb.Value{Kind: &btpb.Value_BoolValue{BoolValue: true}},
				&btpb.Value{Kind: &btpb.Value_IntValue{IntValue: 42}},
				&btpb.Value{Kind: &btpb.Value_BytesValue{BytesValue: []byte("hello")}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_StringValue{StringValue: "wasm"}},
					{Kind: &btpb.Value_StringValue{StringValue: "udf"}},
				}}}},
				&btpb.Value{Kind: &btpb.Value_DateValue{DateValue: &datepb.Date{Year: 2026, Month: 10, Day: 23}}},
			)
			if err := stream.Send(batch1); err != nil {
				return err
			}
			batch2 := makeProtoRowsBatchResponse(t, "tok-2",
				&btpb.Value{Kind: &btpb.Value_StringValue{StringValue: "doc#2"}},
				&btpb.Value{Kind: &btpb.Value_BoolValue{BoolValue: false}},
				&btpb.Value{Kind: &btpb.Value_IntValue{IntValue: 7}},
				&btpb.Value{Kind: &btpb.Value_BytesValue{BytesValue: []byte("world")}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{}}},
				&btpb.Value{Kind: &btpb.Value_DateValue{DateValue: &datepb.Date{Year: 2026, Month: 10, Day: 24}}},
			)
			return stream.Send(batch2)
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	if err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT * FROM docs"); err != nil {
		t.Fatalf("runSQL unexpected error: %v", err)
	}
	got := out.String()
	wantTable := "" +
		"+-------------------------------------------------------------+---------+-------+---------+------------+------------+\n" +
		"| doc_id                                                      | matched | score | payload | tags       | created_on |\n" +
		"+-------------------------------------------------------------+---------+-------+---------+------------+------------+\n" +
		"| doc#1 with long unwrapped text value exceeding thirty chars | true    | 42    | hello   | [wasm udf] | 2026-10-23 |\n" +
		"| doc#2                                                       | false   | 7     | world   | []         | 2026-10-24 |\n" +
		"+-------------------------------------------------------------+---------+-------+---------+------------+------------+\n"
	if diff := cmp.Diff(wantTable, got); diff != "" {
		t.Errorf("runSQL table output diff (-want +got):\n%s", diff)
	}
}

// UT-12b: Verify runSQL succeeds cleanly and writes no bare borders when a valid query returns zero rows.
func TestRunSQL_ZeroRowQuerySucceedsCleanly(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "id", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, _ btpb.Bigtable_ExecuteQueryServer) error {
			return nil
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	if err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT id FROM empty_tbl"); err != nil {
		t.Fatalf("runSQL zero-row query unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("runSQL zero-row output = %q, want empty", out.String())
	}
}

// UT-11b: Verify PrepareQuery failure returns error with zero partial table output.
func TestRunSQL_PrepareQueryFailureWritesNoOutput(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "unknown function doc_missing")
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT doc_missing(x) FROM t")
	if gotCode := status.Code(err); gotCode != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want InvalidArgument (err: %v)", gotCode, err)
	}
	if out.Len() != 0 {
		t.Errorf("runSQL output on PrepareQuery failure = %q, want empty", out.String())
	}
}

// UT-11c: Verify immediate ExecuteQuery failure returns error with zero partial table output.
func TestRunSQL_ImmediateExecuteQueryFailureWritesNoOutput(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "v", Type: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, _ btpb.Bigtable_ExecuteQueryServer) error {
			return status.Error(codes.InvalidArgument, "WASM trap: unreachable executed")
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT trap_fn(v) FROM t")
	if gotCode := status.Code(err); gotCode != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want InvalidArgument (err: %v)", gotCode, err)
	}
	if out.Len() != 0 {
		t.Errorf("runSQL output on immediate ExecuteQuery failure = %q, want empty", out.String())
	}
}

// UT-11d: Verify mid-stream ExecuteQuery failure after ResumeToken batch flushes zero partial rows.
func TestRunSQL_MidStreamExecuteQueryFailureWritesZeroPartialRows(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "doc_id", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			firstBatch := makeProtoRowsBatchResponse(t, "tok-1",
				&btpb.Value{Kind: &btpb.Value_StringValue{StringValue: "partial-row-must-not-flush"}},
			)
			if err := stream.Send(firstBatch); err != nil {
				return err
			}
			return status.Error(codes.InvalidArgument, "WASM trap on second tablet batch")
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT doc_id FROM t")
	if gotCode := status.Code(err); gotCode != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want InvalidArgument (err: %v)", gotCode, err)
	}
	if out.Len() != 0 {
		t.Errorf("runSQL output on mid-stream ExecuteQuery failure = %q, want empty", out.String())
	}
}

// UT-12a: Verify row-callback formatting error propagates even though BoundStatement.Execute returns nil.
func TestRunSQL_RowCallbackFormattingErrorPropagates(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "count_col", Type: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			// First batch succeeds and buffers a row into tablewriter.
			if err := stream.Send(makeProtoRowsBatchResponse(t, "tok-1",
				&btpb.Value{Kind: &btpb.Value_IntValue{IntValue: 100}},
			)); err != nil {
				return err
			}
			// Second batch sends a wire-mismatched StringValue for an Int64Type column,
			// causing row.GetByIndex in the callback to return false while BoundStatement.Execute returns nil.
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-2",
				&btpb.Value{Kind: &btpb.Value_StringValue{StringValue: "wrong-wire-kind"}},
			))
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT count_col FROM t")
	if err == nil {
		t.Fatalf("runSQL succeeded with output %q, want formatting error", out.String())
	}
	if !strings.Contains(err.Error(), `formatting column "count_col"`) {
		t.Errorf("runSQL error = %q, want substring %q", err.Error(), `formatting column "count_col"`)
	}
	if out.Len() != 0 {
		t.Errorf("runSQL output on callback formatting failure = %q, want empty", out.String())
	}
}

// RP-2: Verify STRUCT/MAP formatting and defensive nil row.Metadata guard in runSQL.
func TestRunSQL_StructAndMapColumnsAndNilMetadataGuard(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{
					Name: "meta_struct",
					Type: &btpb.Type{
						Kind: &btpb.Type_StructType{
							StructType: &btpb.Type_Struct{
								Fields: []*btpb.Type_Struct_Field{
									{FieldName: "author", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
								},
							},
						},
					},
				},
				&btpb.ColumnMetadata{
					Name: "kv_map",
					Type: &btpb.Type{
						Kind: &btpb.Type_MapType{
							MapType: &btpb.Type_Map{
								KeyType:   &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}},
								ValueType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
							},
						},
					},
				},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-1",
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_StringValue{StringValue: "alice"}},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
						{Kind: &btpb.Value_BytesValue{BytesValue: []byte("k1")}},
						{Kind: &btpb.Value_IntValue{IntValue: 9}},
					}}}},
				}}}},
			))
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	if err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT meta_struct, kv_map FROM t"); err != nil {
		t.Fatalf("runSQL struct/map unexpected error: %v", err)
	}
	for _, wantSub := range []string{"author:alice", "k1:9"} {
		if !strings.Contains(out.String(), wantSub) {
			t.Errorf("runSQL struct/map output = %q, missing %q", out.String(), wantSub)
		}
	}

	var errOut bytes.Buffer
	nilMetaErr := runSQL(t.Context(), stubSQLRowExecutor{
		fn: func(_ context.Context, _ string, onRow func(bigtable.ResultRow) bool) error {
			_ = onRow(bigtable.ResultRow{Metadata: nil})
			return nil
		},
	}, &errOut, "SELECT 1")
	if nilMetaErr == nil || !strings.Contains(nilMetaErr.Error(), "missing column metadata") {
		t.Errorf("runSQL(nil Metadata) = %v, want missing column metadata error", nilMetaErr)
	}
	if errOut.Len() != 0 {
		t.Errorf("runSQL(nil Metadata) output = %q, want empty", errOut.String())
	}
}

type stubSQLRowExecutor struct {
	fn func(ctx context.Context, query string, onRow func(bigtable.ResultRow) bool) error
}

func (s stubSQLRowExecutor) executeSQL(ctx context.Context, query string, onRow func(bigtable.ResultRow) bool) error {
	return s.fn(ctx, query, onRow)
}

// RP-2: Verify doSql preserves os.Stdout file redirection (-o) for valid queries.
func TestDoSQL_PreservesStdoutRedirection(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "greeting", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-1",
				&btpb.Value{Kind: &btpb.Value_StringValue{StringValue: "redirected-ok"}},
			))
		},
	}
	btClient := startFakeSQLBigtableClient(t, srv)

	origClient := client
	origConfig := config
	origStdout := os.Stdout
	outPath := filepath.Join(t.TempDir(), "sql_out.txt")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("os.Create failed: %v", err)
	}
	t.Cleanup(func() {
		_ = f.Close()
		os.Stdout = origStdout
		client = origClient
		config = origConfig
	})

	client = btClient
	config = &Config{Project: "proj", Instance: "inst", Creds: "dummy.json"}
	os.Stdout = f

	doMain(config, []string{"sql", "SELECT 'redirected-ok' AS greeting"})
	if err := f.Close(); err != nil {
		t.Fatalf("closing redirected output file failed: %v", err)
	}

	redirectedBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading redirected output file failed: %v", err)
	}
	if !strings.Contains(string(redirectedBytes), "redirected-ok") {
		t.Errorf("redirected SQL output = %q, want substring %q", string(redirectedBytes), "redirected-ok")
	}
}

// UT-12b: Verify top-level SQL NULL scalars (STRING, BYTES, INT64, BOOL) and
// nested NULL elements inside ARRAY, STRUCT, and MAP columns render as "NULL".
func TestRunSQL_FormatsNullScalarAndNestedNullValues(t *testing.T) {
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{Name: "null_str", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
				&btpb.ColumnMetadata{Name: "null_bytes", Type: &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}}},
				&btpb.ColumnMetadata{Name: "null_bool", Type: &btpb.Type{Kind: &btpb.Type_BoolType{BoolType: &btpb.Type_Bool{}}}},
				&btpb.ColumnMetadata{
					Name: "arr_with_null",
					Type: &btpb.Type{
						Kind: &btpb.Type_ArrayType{
							ArrayType: &btpb.Type_Array{
								ElementType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
							},
						},
					},
				},
				&btpb.ColumnMetadata{
					Name: "struct_with_null",
					Type: &btpb.Type{
						Kind: &btpb.Type_StructType{
							StructType: &btpb.Type_Struct{
								Fields: []*btpb.Type_Struct_Field{
									{FieldName: "note", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
								},
							},
						},
					},
				},
				&btpb.ColumnMetadata{
					Name: "map_with_null",
					Type: &btpb.Type{
						Kind: &btpb.Type_MapType{
							MapType: &btpb.Type_Map{
								KeyType:   &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}},
								ValueType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
							},
						},
					},
				},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-nulls",
				&btpb.Value{},
				&btpb.Value{},
				&btpb.Value{},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_IntValue{IntValue: 7}},
					{},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
						{Kind: &btpb.Value_BytesValue{BytesValue: []byte("knull")}},
						{},
					}}}},
				}}}},
			))
		},
	}

	btClient := startFakeSQLBigtableClient(t, srv)
	var out bytes.Buffer
	if err := runSQL(t.Context(), clientSQLRowExecutor{client: btClient}, &out, "SELECT * FROM t"); err != nil {
		t.Fatalf("runSQL with NULLs unexpected error: %v", err)
	}
	got := out.String()
	for _, wantSub := range []string{"[7 NULL]", "map[note:NULL]", "map[knull:NULL]"} {
		if !strings.Contains(got, wantSub) {
			t.Errorf("runSQL NULL output = %q, missing substring %q", got, wantSub)
		}
	}
	if gotNullCount := strings.Count(got, "NULL"); gotNullCount < 6 {
		t.Errorf("runSQL NULL count = %d in %q, want >= 6", gotNullCount, got)
	}

	// Verify composite columns with Go-nillable nested slice/map types (BYTES, ARRAY, MAP).
	srvComposite := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(
				&btpb.ColumnMetadata{
					Name: "bytes_arr",
					Type: &btpb.Type{Kind: &btpb.Type_ArrayType{ArrayType: &btpb.Type_Array{
						ElementType: &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}},
					}}},
				},
				&btpb.ColumnMetadata{
					Name: "bytes_map",
					Type: &btpb.Type{Kind: &btpb.Type_MapType{MapType: &btpb.Type_Map{
						KeyType:   &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}},
						ValueType: &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}},
					}}},
				},
				&btpb.ColumnMetadata{
					Name: "nested_struct",
					Type: &btpb.Type{Kind: &btpb.Type_StructType{StructType: &btpb.Type_Struct{
						Fields: []*btpb.Type_Struct_Field{
							{FieldName: "raw", Type: &btpb.Type{Kind: &btpb.Type_BytesType{BytesType: &btpb.Type_Bytes{}}}},
							{FieldName: "arr", Type: &btpb.Type{Kind: &btpb.Type_ArrayType{ArrayType: &btpb.Type_Array{
								ElementType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
							}}}},
						},
					}}},
				},
				&btpb.ColumnMetadata{
					Name: "arr_of_maps",
					Type: &btpb.Type{Kind: &btpb.Type_ArrayType{ArrayType: &btpb.Type_Array{
						ElementType: &btpb.Type{Kind: &btpb.Type_MapType{MapType: &btpb.Type_Map{
							KeyType:   &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}},
							ValueType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
						}}},
					}}},
				},
				&btpb.ColumnMetadata{
					Name: "map_of_arrs",
					Type: &btpb.Type{Kind: &btpb.Type_MapType{MapType: &btpb.Type_Map{
						KeyType: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}},
						ValueType: &btpb.Type{Kind: &btpb.Type_ArrayType{ArrayType: &btpb.Type_Array{
							ElementType: &btpb.Type{Kind: &btpb.Type_Int64Type{Int64Type: &btpb.Type_Int64{}}},
						}}},
					}}},
				},
			), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-comp-nulls",
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_BytesValue{BytesValue: []byte("alpha")}},
					{},
					{Kind: &btpb.Value_BytesValue{BytesValue: []byte{}}},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
						{Kind: &btpb.Value_StringValue{StringValue: "plain_key"}},
						{},
					}}}},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{},
					{},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{},
				}}}},
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
						{Kind: &btpb.Value_StringValue{StringValue: "k_nil_arr"}},
						{},
					}}}},
				}}}},
			))
		},
	}
	btCompClient := startFakeSQLBigtableClient(t, srvComposite)
	var compOut bytes.Buffer
	if err := runSQL(t.Context(), clientSQLRowExecutor{client: btCompClient}, &compOut, "SELECT * FROM t_comp"); err != nil {
		t.Fatalf("runSQL composite NULLs unexpected error: %v", err)
	}
	gotComp := compOut.String()
	for _, wantSub := range []string{"[alpha NULL ]", "map[plain_key:NULL]", "arr:NULL", "raw:NULL", "[NULL]", "map[k_nil_arr:NULL]"} {
		if !strings.Contains(gotComp, wantSub) {
			t.Errorf("runSQL composite NULL output = %q, missing substring %q", gotComp, wantSub)
		}
	}

	// Verify direct formatNullableSQLValue fallback when a BytesSQLType map key is not base64-encoded.
	gotRawKeyMap, err := formatNullableSQLValue(
		bigtable.MapSQLType{KeyType: bigtable.BytesSQLType{}, ValueType: bigtable.Int64SQLType{}},
		reflect.ValueOf(map[string]int64{"!raw_key!": 5}),
	)
	if err != nil {
		t.Fatalf("formatNullableSQLValue(non-base64 bytes key) unexpected error: %v", err)
	}
	if gotRawKeyMap != "map[!raw_key!:5]" {
		t.Errorf("formatNullableSQLValue(non-base64 bytes key) = %q, want %q", gotRawKeyMap, "map[!raw_key!:5]")
	}
}

func TestFormatNullableSQLValue_Errors(t *testing.T) {
	// Capture a real 1-field bigtable.Struct to test recursive field formatting failure.
	srv := &fakeSQLDataServer{
		prepareFn: func(_ context.Context, _ *btpb.PrepareQueryRequest) (*btpb.PrepareQueryResponse, error) {
			return makePrepareQueryResponse(&btpb.ColumnMetadata{
				Name: "s",
				Type: &btpb.Type{Kind: &btpb.Type_StructType{StructType: &btpb.Type_Struct{
					Fields: []*btpb.Type_Struct_Field{
						{FieldName: "inner", Type: &btpb.Type{Kind: &btpb.Type_StringType{StringType: &btpb.Type_String{}}}},
					},
				}}},
			}), nil
		},
		executeFn: func(_ *btpb.ExecuteQueryRequest, stream btpb.Bigtable_ExecuteQueryServer) error {
			return stream.Send(makeProtoRowsBatchResponse(t, "tok-struct",
				&btpb.Value{Kind: &btpb.Value_ArrayValue{ArrayValue: &btpb.ArrayValue{Values: []*btpb.Value{
					{Kind: &btpb.Value_StringValue{StringValue: "not-a-struct"}},
				}}}},
			))
		},
	}
	btClient := startFakeSQLBigtableClient(t, srv)
	var populatedStruct bigtable.Struct
	if err := (clientSQLRowExecutor{client: btClient}).executeSQL(t.Context(), "SELECT s FROM t", func(row bigtable.ResultRow) bool {
		if err := row.GetByIndex(0, &populatedStruct); err != nil {
			t.Errorf("row.GetByIndex(0, &populatedStruct) failed: %v", err)
		}
		return true
	}); err != nil {
		t.Fatalf("executeSQL unexpected error: %v", err)
	}

	tests := []struct {
		name    string
		sqlType bigtable.SQLType
		val     any
		wantSub string
	}{
		{
			name:    "struct_misaligned_value",
			sqlType: bigtable.StructSQLType{},
			val:     "not-a-struct",
			wantSub: "struct is not aligned with type",
		},
		{
			name: "struct_field_get_by_index_error",
			sqlType: bigtable.StructSQLType{
				Fields: []bigtable.StructSQLField{{Name: "missing", Type: bigtable.StringSQLType{}}},
			},
			val:     bigtable.Struct{},
			wantSub: "out of bounds",
		},
		{
			name: "struct_recursive_field_error",
			sqlType: bigtable.StructSQLType{
				Fields: []bigtable.StructSQLField{{Name: "inner", Type: bigtable.StructSQLType{}}},
			},
			val:     populatedStruct,
			wantSub: "struct is not aligned with type",
		},
		{
			name:    "array_recursive_elem_error",
			sqlType: bigtable.ArraySQLType{ElemType: bigtable.StructSQLType{}},
			val:     []string{"not-a-struct"},
			wantSub: "struct is not aligned with type",
		},
		{
			name:    "map_recursive_key_error",
			sqlType: bigtable.MapSQLType{KeyType: bigtable.StructSQLType{}, ValueType: bigtable.Int64SQLType{}},
			val:     map[string]int64{"k": 1},
			wantSub: "struct is not aligned with type",
		},
		{
			name:    "map_recursive_value_error",
			sqlType: bigtable.MapSQLType{KeyType: bigtable.StringSQLType{}, ValueType: bigtable.StructSQLType{}},
			val:     map[string]string{"k": "not-a-struct"},
			wantSub: "struct is not aligned with type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatNullableSQLValue(tc.sqlType, reflect.ValueOf(tc.val))
			if err == nil {
				t.Fatalf("formatNullableSQLValue(%T, %#v) = %q, want error containing %q", tc.sqlType, tc.val, got, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("formatNullableSQLValue error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}
