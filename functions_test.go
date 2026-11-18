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
	"testing"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
)

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
