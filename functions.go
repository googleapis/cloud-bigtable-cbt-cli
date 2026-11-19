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
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"cloud.google.com/go/bigtable"
	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	gtransport "google.golang.org/api/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	googleCloudResourcePrefixHeader = "google-cloud-resource-prefix"
	xGoogRequestParamsHeader        = "x-goog-request-params"
)

var (
	emulatorSchemeRE      = regexp.MustCompile(`^(http://|https://|passthrough:///)`)
	functionAdminConnPool gtransport.ConnPool
)

// functionAdminAPI abstracts the BigtableInstanceAdmin Function RPCs so CLI
// commands can be tested hermetically without package-level globals.
type functionAdminAPI interface {
	ListFunctions(ctx context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error)
}

type grpcFunctionAdminClient struct {
	admin btapb.BigtableInstanceAdminClient
}

func newGRPCFunctionAdminClient(conn grpc.ClientConnInterface) *grpcFunctionAdminClient {
	return &grpcFunctionAdminClient{
		admin: btapb.NewBigtableInstanceAdminClient(conn),
	}
}

func getFunctionAdminConnPool() gtransport.ConnPool {
	if functionAdminConnPool == nil {
		var opts []option.ClientOption
		if addr := os.Getenv("BIGTABLE_EMULATOR_HOST"); addr != "" {
			schemeRemoved := emulatorSchemeRE.ReplaceAllString(addr, "")
			conn, err := grpc.NewClient("passthrough:///"+schemeRemoved, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				log.Fatalf("Making emulator gRPC client: %v", err)
			}
			opts = []option.ClientOption{option.WithGRPCConn(conn)}
		} else {
			opts = []option.ClientOption{
				internaloption.WithDefaultEndpointTemplate("bigtableadmin.UNIVERSE_DOMAIN:443"),
				internaloption.WithDefaultMTLSEndpoint("bigtableadmin.mtls.googleapis.com:443"),
				internaloption.WithDefaultUniverseDomain("googleapis.com"),
				internaloption.WithDefaultAudience("https://bigtableadmin.googleapis.com/"),
				internaloption.WithDefaultScopes(bigtable.InstanceAdminScope),
				internaloption.EnableJwtWithScope(),
				internaloption.EnableNewAuthLibrary(),
				option.WithGRPCDialOption(grpc.WithDefaultCallOptions(
					grpc.MaxCallRecvMsgSize(math.MaxInt32))),
				option.WithUserAgent(cliUserAgent),
			}
			if ep := config.AdminEndpoint; ep != "" {
				opts = append(opts, option.WithEndpoint(ep))
			}
			opts = getCredentialOpts(opts)
		}
		var err error
		functionAdminConnPool, err = gtransport.DialPool(context.Background(), opts...)
		if err != nil {
			log.Fatalf("Dialing BigtableInstanceAdmin connection pool: %v", err)
		}
	}
	return functionAdminConnPool
}

func defaultFunctionAdminAPI() functionAdminAPI {
	return newGRPCFunctionAdminClient(getFunctionAdminConnPool())
}

// withResourcePrefix attaches the project-level routing header while preserving
// any outgoing headers (such as x-goog-iam-authorization-token) from doMain.
func withResourcePrefix(ctx context.Context, project string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, googleCloudResourcePrefixHeader, "projects/"+project)
}

// withRequestParams derives a per-RPC context with URL-escaped x-goog-request-params
// so routing parameters never duplicate across pagination loops.
func withRequestParams(ctx context.Context, key, val string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, xGoogRequestParamsHeader, fmt.Sprintf("%s=%s", key, url.QueryEscape(val)))
}

func (c *grpcFunctionAdminClient) ListFunctions(ctx context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error) {
	return c.admin.ListFunctions(withRequestParams(ctx, "parent", req.GetParent()), req)
}

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

func instanceResourceName(project, instance string) string {
	return fmt.Sprintf("projects/%s/instances/%s", project, instance)
}

func runListFunctions(ctx context.Context, api functionAdminAPI, project, instance string, w io.Writer, args ...string) error {
	if len(args) != 0 {
		return errors.New("usage: cbt listfunctions")
	}
	parent := instanceResourceName(project, instance)
	prefix := parent + "/functions/"
	baseCtx := withResourcePrefix(ctx, project)

	var functions []*btapb.Function
	var pageToken string
	for {
		resp, err := api.ListFunctions(baseCtx, &btapb.ListFunctionsRequest{
			Parent:    parent,
			PageToken: pageToken,
		})
		if err != nil {
			return fmt.Errorf("listing functions: %w", err)
		}
		functions = append(functions, resp.GetFunctions()...)
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	tw := tabwriter.NewWriter(w, 10, 8, 4, '\t', 0)
	fmt.Fprintf(tw, "Function ID\tType\tSignature\n")
	fmt.Fprintf(tw, "-----------\t----\t---------\n")
	for _, fn := range functions {
		id := strings.TrimPrefix(fn.GetName(), prefix)
		kind, sig := describeFunctionKindAndSignature(fn)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", id, kind, sig)
	}
	return tw.Flush()
}

func doListFunctions(ctx context.Context, args ...string) {
	if err := runListFunctions(ctx, defaultFunctionAdminAPI(), config.Project, config.Instance, os.Stdout, args...); err != nil {
		log.Fatal(err)
	}
}
