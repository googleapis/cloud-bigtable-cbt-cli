/*
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/bigtable"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// setupAgentEnv clears all AI-agent detection environment variables, sets
// envVars for the duration of the test, and restores any original values.
func setupAgentEnv(t *testing.T, envVars map[string]string) {
	t.Helper()
	type savedEnv struct {
		val string
		ok  bool
	}
	originals := make(map[string]savedEnv)
	saveAndClear := func(k string) {
		if _, seen := originals[k]; !seen {
			v, ok := os.LookupEnv(k)
			originals[k] = savedEnv{val: v, ok: ok}
		}
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("os.Unsetenv(%q): %v", k, err)
		}
	}
	for _, a := range aiAgents {
		saveAndClear(a.envVar)
	}
	for k := range envVars {
		saveAndClear(k)
	}
	t.Cleanup(func() {
		for k, orig := range originals {
			if orig.ok {
				_ = os.Setenv(k, orig.val)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	})
	for k, v := range envVars {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("os.Setenv(%q, %q): %v", k, v, err)
		}
	}
}

func TestDetectAIAgent(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantAgent string
		wantFound bool
	}{
		{name: "None", env: map[string]string{}, wantAgent: "", wantFound: false},
		{name: "PrioritizeAiAgent", env: map[string]string{"AI_AGENT": " custom-agent ", "CLAUDECODE": "true"}, wantAgent: "custom-agent", wantFound: true},
		{name: "AiAgentAllowedChars", env: map[string]string{"AI_AGENT": "org/agent_v1.2-beta"}, wantAgent: "org/agent_v1.2-beta", wantFound: true},
		{name: "Antigravity", env: map[string]string{"ANTIGRAVITY_AGENT": "true"}, wantAgent: "antigravity", wantFound: true},
		{name: "GeminiCli", env: map[string]string{"GEMINI_CLI": "true"}, wantAgent: "gemini_cli", wantFound: true},
		{name: "AndroidStudioAgent", env: map[string]string{"ANDROID_STUDIO_AGENT": "true"}, wantAgent: "android_studio_agent", wantFound: true},
		{name: "Kiro", env: map[string]string{"KIRO_AGENT_PATH": "/path"}, wantAgent: "kiro", wantFound: true},
		{name: "ClaudeCode_ClaudeCode", env: map[string]string{"CLAUDECODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "ClaudeCode_Claude_Code", env: map[string]string{"CLAUDE_CODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "Cursor_CursorAgent", env: map[string]string{"CURSOR_AGENT": "true"}, wantAgent: "cursor", wantFound: true},
		{name: "Cursor_CursorTraceId", env: map[string]string{"CURSOR_TRACE_ID": "123"}, wantAgent: "cursor", wantFound: true},
		{name: "Cursor_CursorExtensionHostRole", env: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": "agent-exec"}, wantAgent: "cursor", wantFound: true},
		{name: "GithubCopilot_CopilotModel", env: map[string]string{"COPILOT_MODEL": "gpt-4"}, wantAgent: "github_copilot", wantFound: true},
		{name: "GithubCopilot_CopilotAllowAll", env: map[string]string{"COPILOT_ALLOW_ALL": "true"}, wantAgent: "github_copilot", wantFound: true},
		{name: "GithubCopilot_CopilotGithubToken", env: map[string]string{"COPILOT_GITHUB_TOKEN": "token"}, wantAgent: "github_copilot", wantFound: true},
		{name: "Codex_CodexSandbox", env: map[string]string{"CODEX_SANDBOX": "true"}, wantAgent: "codex_cli", wantFound: true},
		{name: "Codex_CodexCi", env: map[string]string{"CODEX_CI": "true"}, wantAgent: "codex_cli", wantFound: true},
		{name: "Codex_CodexThreadId", env: map[string]string{"CODEX_THREAD_ID": "thread"}, wantAgent: "codex_cli", wantFound: true},
		{name: "Cline", env: map[string]string{"CLINE_ACTIVE": "true"}, wantAgent: "cline", wantFound: true},
		{name: "OpenCode_Opencode", env: map[string]string{"OPENCODE": "true"}, wantAgent: "open_code", wantFound: true},
		{name: "OpenCode_OpencodeClient", env: map[string]string{"OPENCODE_CLIENT": "true"}, wantAgent: "open_code", wantFound: true},
		{name: "Replit", env: map[string]string{"REPL_ID": "123"}, wantAgent: "replit", wantFound: true},
		{name: "Augment", env: map[string]string{"AUGMENT_AGENT": "true"}, wantAgent: "augment", wantFound: true},
		{name: "EmptyAiAgent", env: map[string]string{"AI_AGENT": ""}, wantAgent: "", wantFound: false},
		{name: "WhitespaceAiAgent", env: map[string]string{"AI_AGENT": "   "}, wantAgent: "", wantFound: false},
		{name: "EmptyAiAgentFallsBack", env: map[string]string{"AI_AGENT": "", "CLAUDECODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "WhitespaceAiAgentFallsBack", env: map[string]string{"AI_AGENT": "   ", "CLAUDECODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "InvalidAiAgentInternalSpace", env: map[string]string{"AI_AGENT": "my agent"}, wantAgent: "", wantFound: false},
		{name: "InvalidAiAgentInternalTab", env: map[string]string{"AI_AGENT": "my\tagent"}, wantAgent: "", wantFound: false},
		{name: "InvalidAiAgentControlChar", env: map[string]string{"AI_AGENT": "bad\nagent", "CLAUDECODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "InvalidAiAgentCR", env: map[string]string{"AI_AGENT": "bad\ragent"}, wantAgent: "", wantFound: false},
		{name: "InvalidAiAgentDEL", env: map[string]string{"AI_AGENT": "bad\x7fagent"}, wantAgent: "", wantFound: false},
		{name: "InvalidAiAgentNonASCII", env: map[string]string{"AI_AGENT": "agent-é"}, wantAgent: "", wantFound: false},
		{name: "InvalidAiAgentDelimiter", env: map[string]string{"AI_AGENT": "(my-agent);foo", "CLAUDECODE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "EmptyClaudeCode", env: map[string]string{"CLAUDECODE": ""}, wantAgent: "claude_code", wantFound: true},
		{name: "WhitespaceClaudeCode", env: map[string]string{"CLAUDECODE": "   "}, wantAgent: "claude_code", wantFound: true},
		{name: "EmptyCursorExtensionHostRole", env: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": ""}, wantAgent: "", wantFound: false},
		{name: "WhitespaceCursorExtensionHostRole", env: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": "   "}, wantAgent: "", wantFound: false},
		{name: "InvalidCursorExtensionHostRole", env: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": "invalid"}, wantAgent: "", wantFound: false},
		{name: "Precedence_AntigravityOverGemini", env: map[string]string{"ANTIGRAVITY_AGENT": "true", "GEMINI_CLI": "true"}, wantAgent: "antigravity", wantFound: true},
		{name: "Precedence_ClaudeOverCline", env: map[string]string{"CLAUDE_CODE": "true", "CLINE_ACTIVE": "true"}, wantAgent: "claude_code", wantFound: true},
		{name: "Precedence_CopilotOverReplit", env: map[string]string{"COPILOT_ALLOW_ALL": "true", "REPL_ID": "123"}, wantAgent: "github_copilot", wantFound: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupAgentEnv(t, tc.env)
			got, found := detectAIAgent()
			if got != tc.wantAgent || found != tc.wantFound {
				t.Errorf("detectAIAgent() with env %v = (%q, %v), want (%q, %v)", tc.env, got, found, tc.wantAgent, tc.wantFound)
			}
		})
	}
}

func TestBuildUserAgent(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		override string
		env      map[string]string
		want     string
	}{
		{
			name: "DefaultNoAgent",
			base: "cbt-cli-go/1.26.0 abc123 2026-11-17",
			env:  map[string]string{},
			want: "cbt-cli-go/1.26.0 abc123 2026-11-17",
		},
		{
			name: "DefaultWithAgent",
			base: "cbt-cli-go/1.26.0 abc123 2026-11-17",
			env:  map[string]string{"CLAUDECODE": "1"},
			want: "cbt-cli-go/1.26.0 abc123 2026-11-17 agent-name/claude_code",
		},
		{
			name:     "OverrideNoAgent",
			base:     "cbt-cli-go/1.26.0",
			override: "custom-ua/1.0",
			env:      map[string]string{},
			want:     "custom-ua/1.0",
		},
		{
			name:     "OverrideWithAgent",
			base:     "cbt-cli-go/1.26.0",
			override: "custom-ua/1.0",
			env:      map[string]string{"GEMINI_CLI": "1"},
			want:     "custom-ua/1.0 agent-name/gemini_cli",
		},
		{
			name:     "WhitespaceOverrideFallsBackToBase",
			base:     "cbt-cli-go/1.26.0",
			override: "   ",
			env:      map[string]string{"CLAUDECODE": "1"},
			want:     "cbt-cli-go/1.26.0 agent-name/claude_code",
		},
		{
			name:     "OverrideAlreadyHasAgentName",
			base:     "cbt-cli-go/1.26.0",
			override: "custom-ua/1.0 agent-name/existing",
			env:      map[string]string{"CLAUDECODE": "1"},
			want:     "custom-ua/1.0 agent-name/existing",
		},
		{
			name:     "OverrideAlreadyHasAgentNameCaseInsensitive",
			base:     "cbt-cli-go/1.26.0",
			override: "custom-ua/1.0 Agent-Name/existing",
			env:      map[string]string{"CLAUDECODE": "1"},
			want:     "custom-ua/1.0 Agent-Name/existing",
		},
		{
			name:     "BareAgentNamePrefixDoesNotSuppressDetection",
			base:     "cbt-cli-go/1.26.0",
			override: "custom-ua/1.0 agent-name/",
			env:      map[string]string{"CLAUDECODE": "1"},
			want:     "custom-ua/1.0 agent-name/ agent-name/claude_code",
		},
		{
			name: "EmptyBaseWithAgent",
			base: "",
			env:  map[string]string{"CLAUDECODE": "1"},
			want: "agent-name/claude_code",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupAgentEnv(t, tc.env)
			if got := buildUserAgent(tc.base, tc.override); got != tc.want {
				t.Errorf("buildUserAgent(%q, %q) with env %v = %q, want %q", tc.base, tc.override, tc.env, got, tc.want)
			}
		})
	}
}

func startUserAgentCaptureServer(t *testing.T) (addr string, clientTLS *tls.Config, getUA func(method string) string) {
	t.Helper()

	// Borrow a self-signed localhost TLS certificate from httptest.
	ts := httptest.NewTLSServer(nil)
	cert := ts.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	ts.Close()

	var (
		mu       sync.Mutex
		uaByMeth = make(map[string]string)
	)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			method, _ := grpc.MethodFromServerStream(stream)
			if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
				if vals := md.Get("user-agent"); len(vals) > 0 && method != "" {
					mu.Lock()
					uaByMeth[method] = vals[0]
					mu.Unlock()
				}
			}
			// Succeed PingAndWarm so bigtable.NewClientWithConfig finishes priming its pool.
			if method == "/google.bigtable.v2.Bigtable/PingAndWarm" {
				_ = stream.RecvMsg(&emptypb.Empty{})
				return stream.SendMsg(&emptypb.Empty{})
			}
			return status.Error(codes.Unimplemented, "captured user-agent")
		}),
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	return lis.Addr().String(), &tls.Config{RootCAs: pool}, func(method string) string {
		mu.Lock()
		defer mu.Unlock()
		return uaByMeth[method]
	}
}

func TestClientsSendUserAgent(t *testing.T) {
	// Ensure DefaultClientOptions takes the non-emulator dial path so
	// option.WithUserAgent is wired into the gRPC dial options.
	t.Setenv("BIGTABLE_EMULATOR_HOST", "")

	origConfig := config
	origClient := client
	origAdminClient := adminClient
	origInstanceAdminClient := instanceAdminClient
	origCLIUserAgent := cliUserAgent
	t.Cleanup(func() {
		config = origConfig
		client = origClient
		adminClient = origAdminClient
		instanceAdminClient = origInstanceAdminClient
		cliUserAgent = origCLIUserAgent
	})

	resetClients := func() {
		if client != nil {
			_ = client.Close()
			client = nil
		}
		if adminClient != nil {
			_ = adminClient.Close()
			adminClient = nil
		}
		if instanceAdminClient != nil {
			_ = instanceAdminClient.Close()
			instanceAdminClient = nil
		}
	}

	tests := []struct {
		name              string
		env               map[string]string
		userAgentOverride string
		wantPrefix        string
		forbid            string
	}{
		{
			name:       "WithAIAgent",
			env:        map[string]string{"CLAUDECODE": "1"},
			wantPrefix: "cbt-cli-go/1.26.0-test agent-name/claude_code ",
		},
		{
			name:       "WithoutAIAgent",
			env:        map[string]string{},
			wantPrefix: "cbt-cli-go/1.26.0-test ",
			forbid:     "agent-name/",
		},
		{
			name:              "OverrideWithAIAgent",
			env:               map[string]string{"GEMINI_CLI": "1"},
			userAgentOverride: "custom-ua/1.0",
			wantPrefix:        "custom-ua/1.0 agent-name/gemini_cli ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, clientTLS, getUA := startUserAgentCaptureServer(t)
			setupAgentEnv(t, tc.env)
			cliUserAgent = "cbt-cli-go/1.26.0-test"
			config = &Config{
				Project:       "test-project",
				Instance:      "test-instance",
				DataEndpoint:  addr,
				AdminEndpoint: addr,
				UserAgent:     tc.userAgentOverride,
				TokenSource:   oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}),
				TLSCreds:      credentials.NewTLS(clientTLS),
			}
			resetClients()
			t.Cleanup(resetClients)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			clients := []struct {
				name   string
				method string
				invoke func()
			}{
				{
					name:   "getClient",
					method: "/google.bigtable.v2.Bigtable/ReadRows",
					invoke: func() {
						cfg := bigtable.ClientConfig{
							MetricsProvider:     bigtable.NoopMetricsProvider{},
							DisableDirectAccess: true,
							DisableSession:      true,
						}
						_, _ = getClient(cfg).Open("test-table").ReadRow(ctx, "k")
					},
				},
				{
					name:   "getAdminClient",
					method: "/google.bigtable.admin.v2.BigtableTableAdmin/ListTables",
					invoke: func() {
						_, _ = getAdminClient().Tables(ctx)
					},
				},
				{
					name:   "getInstanceAdminClient",
					method: "/google.bigtable.admin.v2.BigtableInstanceAdmin/ListInstances",
					invoke: func() {
						_, _ = getInstanceAdminClient().Instances(ctx)
					},
				},
			}

			for _, c := range clients {
				c.invoke()
				got := getUA(c.method)
				if !strings.HasPrefix(got, tc.wantPrefix) {
					t.Errorf("%s (%s) user-agent = %q, want prefix %q", c.name, c.method, got, tc.wantPrefix)
				}
				if tc.forbid != "" && strings.Contains(got, tc.forbid) {
					t.Errorf("%s (%s) user-agent = %q, must not contain %q", c.name, c.method, got, tc.forbid)
				}
			}
		})
	}
}
