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
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"

	"cloud.google.com/go/bigtable"
	"github.com/olekukonko/tablewriter"
)

// sqlRowExecutor abstracts PrepareStatement + Bind + Execute for hermetic SQL testing.
type sqlRowExecutor interface {
	executeSQL(ctx context.Context, query string, onRow func(bigtable.ResultRow) bool) error
}

type clientSQLRowExecutor struct {
	client *bigtable.Client
}

func (e clientSQLRowExecutor) executeSQL(ctx context.Context, query string, onRow func(bigtable.ResultRow) bool) error {
	stmt, err := e.client.PrepareStatement(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("while preparing statement: %w", err)
	}
	boundStmt, err := stmt.Bind(nil)
	if err != nil {
		return fmt.Errorf("while binding statement: %w", err)
	}
	if err := boundStmt.Execute(ctx, onRow); err != nil {
		return fmt.Errorf("while executing statement: %w", err)
	}
	return nil
}

// runSQL executes a Bigtable SQL query and renders the ASCII table to w only
// if both query execution and per-column value formatting complete without error.
func runSQL(ctx context.Context, exec sqlRowExecutor, w io.Writer, query string) error {
	tw := tablewriter.NewWriter(w)
	tw.SetAutoFormatHeaders(false)
	tw.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	tw.SetAlignment(tablewriter.ALIGN_LEFT)
	tw.SetAutoWrapText(false)

	// BoundStatement.Execute cancels the stream and returns nil when its row
	// callback returns false, so capture formatting errors explicitly before aborting.
	var callbackErr error
	hasRows := false
	execErr := exec.executeSQL(ctx, query, func(row bigtable.ResultRow) bool {
		if row.Metadata == nil {
			callbackErr = errors.New("result row is missing column metadata")
			return false
		}
		if !hasRows {
			hs := make([]string, len(row.Metadata.Columns))
			for i := 0; i < len(row.Metadata.Columns); i++ {
				hs[i] = row.Metadata.Columns[i].Name
			}
			tw.SetHeader(hs)
			hasRows = true
		}

		vs := make([]string, len(row.Metadata.Columns))
		for i := 0; i < len(row.Metadata.Columns); i++ {
			v, err := formatSQLRowValue(row, i)
			if err != nil {
				callbackErr = fmt.Errorf("formatting column %q: %w", row.Metadata.Columns[i].Name, err)
				return false
			}
			vs[i] = v
		}
		tw.Append(vs)
		return true
	})
	if callbackErr != nil {
		return callbackErr
	}
	if execErr != nil {
		return execErr
	}
	if hasRows {
		tw.Render()
	}
	return nil
}

// formatSQLRowValue formats a SQL result cell, rendering SQL NULL scalars and
// nested NULL array/map/struct elements as "NULL" instead of failing on
// getFormattedValue's non-pointer destination reflection.
func formatSQLRowValue(row bigtable.ResultRow, index int) (string, error) {
	var raw any
	if err := row.GetByIndex(index, &raw); err != nil {
		return "", err
	}
	if raw == nil {
		return "NULL", nil
	}
	return formatNullableSQLValue(row.Metadata.Columns[index].SQLType, reflect.ValueOf(raw))
}

func formatNullableSQLValue(t bigtable.SQLType, v reflect.Value) (string, error) {
	if !v.IsValid() {
		return "NULL", nil
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "NULL", nil
		}
		return formatNullableSQLValue(t, v.Elem())
	}
	if _, isBytes := t.(bigtable.BytesSQLType); isBytes && v.Kind() == reflect.Slice {
		if v.IsNil() {
			return "NULL", nil
		}
		return utf8(string(v.Bytes())), nil
	}
	if st, isStruct := t.(bigtable.StructSQLType); isStruct {
		s, ok := v.Interface().(bigtable.Struct)
		if !ok {
			return "", fmt.Errorf("struct is not aligned with type")
		}
		formattedMap := make(map[string]string, len(st.Fields))
		for i, f := range st.Fields {
			var fieldRaw any
			if err := s.GetByIndex(i, &fieldRaw); err != nil {
				return "", err
			}
			fv, err := formatNullableSQLValue(f.Type, reflect.ValueOf(fieldRaw))
			if err != nil {
				return "", err
			}
			formattedMap[utf8(f.Name)] = fv
		}
		return fmt.Sprintf("%v", formattedMap), nil
	}
	if at, isArray := t.(bigtable.ArraySQLType); isArray && v.Kind() == reflect.Slice {
		if v.IsNil() {
			return "NULL", nil
		}
		formattedSlice := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			fv, err := formatNullableSQLValue(at.ElemType, v.Index(i))
			if err != nil {
				return "", err
			}
			formattedSlice[i] = fv
		}
		return fmt.Sprintf("%v", formattedSlice), nil
	}
	if mt, isMap := t.(bigtable.MapSQLType); isMap && v.Kind() == reflect.Map {
		if v.IsNil() {
			return "NULL", nil
		}
		formattedMap := make(map[string]string, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			var keyStr string
			if _, isBytesKey := mt.KeyType.(bigtable.BytesSQLType); isBytesKey {
				decoded, err := base64.StdEncoding.DecodeString(iter.Key().String())
				if err != nil {
					keyStr = utf8(iter.Key().String())
				} else {
					keyStr = utf8(string(decoded))
				}
			} else {
				kf, err := formatNullableSQLValue(mt.KeyType, iter.Key())
				if err != nil {
					return "", err
				}
				keyStr = kf
			}
			vf, err := formatNullableSQLValue(mt.ValueType, iter.Value())
			if err != nil {
				return "", err
			}
			formattedMap[keyStr] = vf
		}
		return fmt.Sprintf("%v", formattedMap), nil
	}
	return formatDecodedValue(t, v)
}
