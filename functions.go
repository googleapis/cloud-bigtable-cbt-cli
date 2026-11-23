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
	"fmt"
	"strings"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
)

// formatAdminType is a total, non-panicking formatter across all Bigtable Admin
// Type variants so listfunctions and getfunction render both WasmFunction and
// SqlScalarFunction definitions cleanly.
func formatAdminType(t *btapb.Type) string {
	if t.GetKind() == nil {
		return "ANY"
	}
	switch k := t.GetKind().(type) {
	case *btapb.Type_BytesType:
		return "BYTES"
	case *btapb.Type_StringType:
		return "STRING"
	case *btapb.Type_Int64Type:
		return "INT64"
	case *btapb.Type_Int32Type:
		return "INT32"
	case *btapb.Type_Float64Type:
		return "FLOAT64"
	case *btapb.Type_Float32Type:
		return "FLOAT32"
	case *btapb.Type_BoolType:
		return "BOOL"
	case *btapb.Type_TimestampType:
		return "TIMESTAMP"
	case *btapb.Type_DateType:
		return "DATE"
	case *btapb.Type_GeographyType:
		return "GEOGRAPHY"
	case *btapb.Type_ArrayType:
		return fmt.Sprintf("ARRAY<%s>", formatAdminType(k.ArrayType.GetElementType()))
	case *btapb.Type_MapType:
		return fmt.Sprintf("MAP<%s, %s>", formatAdminType(k.MapType.GetKeyType()), formatAdminType(k.MapType.GetValueType()))
	case *btapb.Type_StructType:
		var fields []string
		for _, f := range k.StructType.GetFields() {
			if f.GetFieldName() != "" {
				fields = append(fields, fmt.Sprintf("%s %s", f.GetFieldName(), formatAdminType(f.GetType())))
			} else {
				fields = append(fields, formatAdminType(f.GetType()))
			}
		}
		return fmt.Sprintf("STRUCT<%s>", strings.Join(fields, ", "))
	case *btapb.Type_ProtoType:
		return fmt.Sprintf("PROTO<%s.%s>", k.ProtoType.GetSchemaBundleId(), k.ProtoType.GetMessageName())
	case *btapb.Type_EnumType:
		return fmt.Sprintf("ENUM<%s.%s>", k.EnumType.GetSchemaBundleId(), k.EnumType.GetEnumName())
	case *btapb.Type_AggregateType:
		return fmt.Sprintf("AGGREGATE<%s>", formatAdminType(k.AggregateType.GetInputType()))
	default:
		return "UNKNOWN"
	}
}

func formatArgumentList(args []*btapb.Function_Argument) string {
	var formatted []string
	for _, arg := range args {
		formatted = append(formatted, fmt.Sprintf("%s:%s", arg.GetName(), formatAdminType(arg.GetDataType())))
	}
	return strings.Join(formatted, ", ")
}

func describeFunctionKindAndSignature(fn *btapb.Function) (string, string) {
	switch ft := fn.GetFunctionType().(type) {
	case *btapb.Function_WasmFunction_:
		sig := fmt.Sprintf("(%s) -> %s", formatArgumentList(ft.WasmFunction.GetArguments()), formatAdminType(ft.WasmFunction.GetReturnType()))
		return "WASM", sig
	case *btapb.Function_SqlScalarFunction_:
		sig := fmt.Sprintf("(%s) -> %s", formatArgumentList(ft.SqlScalarFunction.GetArguments()), formatAdminType(ft.SqlScalarFunction.GetReturnType()))
		return "SQL", sig
	default:
		return "UNKNOWN", "()"
	}
}
