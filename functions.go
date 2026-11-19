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
	"crypto/sha256"
	hexpkg "encoding/hex"
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
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	maxFunctionIdentifierLen        = 128
	googleCloudResourcePrefixHeader = "google-cloud-resource-prefix"
	xGoogRequestParamsHeader        = "x-goog-request-params"
)

var (
	functionIdentifierRE  = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	emulatorSchemeRE      = regexp.MustCompile(`^(http://|https://|passthrough:///)`)
	functionAdminConnPool gtransport.ConnPool
)

// functionAdminAPI abstracts the BigtableInstanceAdmin Function RPCs so CLI
// commands can be tested hermetically without package-level globals.
type functionAdminAPI interface {
	GetFunction(ctx context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error)
	ListFunctions(ctx context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error)
	DeleteFunction(ctx context.Context, req *btapb.DeleteFunctionRequest) (*emptypb.Empty, error)
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

func (c *grpcFunctionAdminClient) GetFunction(ctx context.Context, req *btapb.GetFunctionRequest) (*btapb.Function, error) {
	return c.admin.GetFunction(withRequestParams(ctx, "name", req.GetName()), req)
}

func (c *grpcFunctionAdminClient) ListFunctions(ctx context.Context, req *btapb.ListFunctionsRequest) (*btapb.ListFunctionsResponse, error) {
	return c.admin.ListFunctions(withRequestParams(ctx, "parent", req.GetParent()), req)
}

func (c *grpcFunctionAdminClient) DeleteFunction(ctx context.Context, req *btapb.DeleteFunctionRequest) (*emptypb.Empty, error) {
	return c.admin.DeleteFunction(withRequestParams(ctx, "name", req.GetName()), req)
}

func validateFunctionIdentifier(id, label string) error {
	if len(id) > maxFunctionIdentifierLen {
		return fmt.Errorf("%s %q exceeds maximum length of %d characters", label, id, maxFunctionIdentifierLen)
	}
	if !functionIdentifierRE.MatchString(id) {
		return fmt.Errorf("invalid %s %q: must match [a-z_][a-z0-9_]*", label, id)
	}
	return nil
}

// parseFunctionScalarType parses a scalar type for WasmFunction (when allowSQL is false)
// or SqlScalarFunction (when allowSQL is true).
// Per google/bigtable/admin/v2/instance.proto, Function types must omit Encoding.
func parseFunctionScalarType(spec string, allowSQL bool) (*btapb.Type, error) {
	upper := strings.ToUpper(strings.TrimSpace(spec))
	if allowSQL && upper == "DATE" {
		return &btapb.Type{Kind: &btapb.Type_DateType{DateType: &btapb.Type_Date{}}}, nil
	}
	switch upper {
	case "BYTES":
		return &btapb.Type{Kind: &btapb.Type_BytesType{BytesType: &btapb.Type_Bytes{}}}, nil
	case "STRING":
		return &btapb.Type{Kind: &btapb.Type_StringType{StringType: &btapb.Type_String{}}}, nil
	case "INT64":
		return &btapb.Type{Kind: &btapb.Type_Int64Type{Int64Type: &btapb.Type_Int64{}}}, nil
	case "INT32":
		return &btapb.Type{Kind: &btapb.Type_Int32Type{Int32Type: &btapb.Type_Int32{}}}, nil
	case "FLOAT64":
		return &btapb.Type{Kind: &btapb.Type_Float64Type{Float64Type: &btapb.Type_Float64{}}}, nil
	case "FLOAT32":
		return &btapb.Type{Kind: &btapb.Type_Float32Type{Float32Type: &btapb.Type_Float32{}}}, nil
	case "BOOL":
		return &btapb.Type{Kind: &btapb.Type_BoolType{BoolType: &btapb.Type_Bool{}}}, nil
	case "TIMESTAMP":
		return &btapb.Type{Kind: &btapb.Type_TimestampType{TimestampType: &btapb.Type_Timestamp{}}}, nil
	default:
		if allowSQL {
			return nil, fmt.Errorf("unsupported SQL function type %q (supported scalar types: BYTES, STRING, INT64, INT32, FLOAT64, FLOAT32, BOOL, TIMESTAMP, DATE)", spec)
		}
		return nil, fmt.Errorf("unsupported WASM function type %q (supported scalar types: BYTES, STRING, INT64, INT32, FLOAT64, FLOAT32, BOOL, TIMESTAMP)", spec)
	}
}

// parseFunctionType parses a scalar or single-level ARRAY<T> type for WasmFunction
// or SqlScalarFunction.
func parseFunctionType(spec string, allowSQL bool) (*btapb.Type, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, errors.New("type specification must not be empty")
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "ARRAY<") {
		if !strings.HasSuffix(trimmed, ">") {
			return nil, fmt.Errorf("malformed array type %q: missing closing '>'", spec)
		}
		inner := strings.TrimSpace(trimmed[len("ARRAY<") : len(trimmed)-1])
		if strings.HasPrefix(strings.ToUpper(inner), "ARRAY<") {
			return nil, fmt.Errorf("nested array type %q is not supported", spec)
		}
		elemType, err := parseFunctionScalarType(inner, allowSQL)
		if err != nil {
			return nil, fmt.Errorf("invalid array element type in %q: %w", spec, err)
		}
		return &btapb.Type{
			Kind: &btapb.Type_ArrayType{
				ArrayType: &btapb.Type_Array{ElementType: elemType},
			},
		}, nil
	}
	return parseFunctionScalarType(trimmed, allowSQL)
}

func parseWASMFunctionType(spec string) (*btapb.Type, error) {
	return parseFunctionType(spec, false)
}

func parseSQLFunctionType(spec string) (*btapb.Type, error) {
	return parseFunctionType(spec, true)
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

func functionResourceName(project, instance, functionID string) string {
	return fmt.Sprintf("projects/%s/instances/%s/functions/%s", project, instance, functionID)
}

func parseNonEmptyOption(parsed map[string]string, key string) (string, bool, error) {
	val, ok := parsed[key]
	if !ok {
		return "", false, nil
	}
	if val == "" {
		return "", true, fmt.Errorf("option %q must not be empty", key)
	}
	return val, true, nil
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

// formatWASMSourceSHA256 renders WasmFunction.source_sha256 as lowercase hex.
// Some backends populate this bytes field with a 64-byte ASCII hex digest
// rather than a 32-byte binary SHA-256 digest; decoding 64-byte ASCII hex so it
// normalizes to lowercase avoids double-encoding it to 128 hex characters.
func formatWASMSourceSHA256(raw []byte) string {
	var decoded [sha256.Size]byte
	if len(raw) == hexpkg.EncodedLen(len(decoded)) {
		if _, err := hexpkg.Decode(decoded[:], raw); err == nil { // if NO error
			raw = decoded[:]
		}
	}
	return hexpkg.EncodeToString(raw)
}

func runGetFunction(ctx context.Context, api functionAdminAPI, project, instance string, w io.Writer, args ...string) error {
	if len(args) != 1 {
		return errors.New("usage: cbt getfunction <function-id>")
	}
	functionID := args[0]
	if err := validateFunctionIdentifier(functionID, "function-id"); err != nil {
		return err
	}
	baseCtx := withResourcePrefix(ctx, project)
	fn, err := api.GetFunction(baseCtx, &btapb.GetFunctionRequest{
		Name: functionResourceName(project, instance, functionID),
	})
	if err != nil {
		return fmt.Errorf("getting function %q: %w", functionID, err)
	}

	kind, _ := describeFunctionKindAndSignature(fn)
	fmt.Fprintf(w, "Name: %s\n", fn.GetName())
	fmt.Fprintf(w, "Type: %s\n", kind)
	if fn.GetEtag() != "" {
		fmt.Fprintf(w, "Etag: %s\n", fn.GetEtag())
	}
	fmt.Fprintf(w, "DeletionProtection: %t\n", fn.GetDeletionProtection())
	switch ft := fn.GetFunctionType().(type) {
	case *btapb.Function_WasmFunction_:
		fmt.Fprintf(w, "Arguments: %s\n", formatArgumentList(ft.WasmFunction.GetArguments()))
		fmt.Fprintf(w, "ReturnType: %s\n", formatAdminType(ft.WasmFunction.GetReturnType()))
		if len(ft.WasmFunction.GetSourceSha256()) > 0 {
			fmt.Fprintf(w, "SourceSha256: %s\n", formatWASMSourceSHA256(ft.WasmFunction.GetSourceSha256()))
		}
	case *btapb.Function_SqlScalarFunction_:
		fmt.Fprintf(w, "Arguments: %s\n", formatArgumentList(ft.SqlScalarFunction.GetArguments()))
		fmt.Fprintf(w, "ReturnType: %s\n", formatAdminType(ft.SqlScalarFunction.GetReturnType()))
		fmt.Fprintf(w, "FunctionBody: %s\n", ft.SqlScalarFunction.GetFunctionBody())
	}
	return nil
}

func runDeleteFunction(ctx context.Context, api functionAdminAPI, project, instance string, args ...string) error {
	if len(args) < 1 {
		return errors.New("usage: cbt deletefunction <function-id> [etag=<etag>]")
	}
	functionID := args[0]
	if err := validateFunctionIdentifier(functionID, "function-id"); err != nil {
		return err
	}
	parsed, err := parseArgs(args[1:], []string{"etag"})
	if err != nil {
		return err
	}
	var etag string
	if etagVal, hasEtag, err := parseNonEmptyOption(parsed, "etag"); err != nil {
		return err
	} else if hasEtag {
		etag = etagVal
	}

	baseCtx := withResourcePrefix(ctx, project)
	_, err = api.DeleteFunction(baseCtx, &btapb.DeleteFunctionRequest{
		Name: functionResourceName(project, instance, functionID),
		Etag: etag,
	})
	if err != nil {
		return fmt.Errorf("deleting function %q: %w", functionID, err)
	}
	return nil
}

func doListFunctions(ctx context.Context, args ...string) {
	if err := runListFunctions(ctx, defaultFunctionAdminAPI(), config.Project, config.Instance, os.Stdout, args...); err != nil {
		log.Fatal(err)
	}
}

func doGetFunction(ctx context.Context, args ...string) {
	if err := runGetFunction(ctx, defaultFunctionAdminAPI(), config.Project, config.Instance, os.Stdout, args...); err != nil {
		log.Fatal(err)
	}
}

func doDeleteFunction(ctx context.Context, args ...string) {
	if err := runDeleteFunction(ctx, defaultFunctionAdminAPI(), config.Project, config.Instance, args...); err != nil {
		log.Fatal(err)
	}
}
