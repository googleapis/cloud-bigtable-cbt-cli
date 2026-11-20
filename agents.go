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
	"os"
	"strings"
)

type aiAgentDef struct {
	envVar          string
	assertedValue   string
	hardcodedResult string
}

// aiAgents lists environment variable checks used to detect AI coding agents,
// matching gcloud and https://github.com/firebase/firebase-tools/blob/main/src/env.ts.
var aiAgents = []aiAgentDef{
	// If assertedValue is empty, any value matches.
	// If hardcodedResult is non-empty, return it; otherwise return the env var value.
	{"AI_AGENT", "", ""}, // Keep first.
	{"ANTIGRAVITY_AGENT", "", "antigravity"},
	{"CLAUDECODE", "", "claude_code"},
	{"CLAUDE_CODE", "", "claude_code"},
	{"CLINE_ACTIVE", "", "cline"},
	{"CODEX_SANDBOX", "", "codex_cli"},
	{"CODEX_CI", "", "codex_cli"},
	{"CODEX_THREAD_ID", "", "codex_cli"},
	{"CURSOR_AGENT", "", "cursor"},
	{"CURSOR_TRACE_ID", "", "cursor"},
	{"CURSOR_EXTENSION_HOST_ROLE", "agent-exec", "cursor"},
	{"GEMINI_CLI", "", "gemini_cli"},
	{"OPENCODE", "", "open_code"},
	{"OPENCODE_CLIENT", "", "open_code"},
	{"ANDROID_STUDIO_AGENT", "", "android_studio_agent"},
	{"KIRO_AGENT_PATH", "", "kiro"},
	{"COPILOT_MODEL", "", "github_copilot"},
	{"COPILOT_ALLOW_ALL", "", "github_copilot"},
	{"COPILOT_GITHUB_TOKEN", "", "github_copilot"},
	{"REPL_ID", "", "replit"},
	{"AUGMENT_AGENT", "", "augment"},
}

// detectAIAgent detects the AI agent invoking cbt from environment variables.
func detectAIAgent() (string, bool) {
	for _, agent := range aiAgents {
		val, ok := os.LookupEnv(agent.envVar)
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if agent.assertedValue != "" && val != agent.assertedValue {
			continue
		}
		if agent.hardcodedResult != "" {
			return agent.hardcodedResult, true
		}
		if val != "" && isValidAgentToken(val) {
			return val, true
		}
	}
	return "", false
}

// maxAgentTokenLen caps AI_AGENT values to guard against overly long
// user-agent header tokens.
const maxAgentTokenLen = 64

// isValidAgentToken reports whether s is between 1 and maxAgentTokenLen
// characters and consists solely of characters in [0-9A-Za-z._/-], ensuring
// it is safe to embed as a single agent-name/<agent> token in HTTP/2 / gRPC
// user-agent headers. "/" is intentionally allowed to match the
// agent-name/([0-9A-Za-z._-/]+) format used by gcloud and bq.
func isValidAgentToken(s string) bool {
	if s == "" || len(s) > maxAgentTokenLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !isAlnum && c != '.' && c != '_' && c != '-' && c != '/' {
			return false
		}
	}
	return true
}

func hasAgentNameToken(ua string) bool {
	const prefix = "agent-name/"
	for _, field := range strings.Fields(ua) {
		if len(field) > len(prefix) && strings.HasPrefix(strings.ToLower(field), prefix) {
			return true
		}
	}
	return false
}

// buildUserAgent returns the effective user-agent string for cbt RPCs.
// If override is non-empty after trimming whitespace, it replaces base. When an
// AI agent is detected in the environment and the user agent does not already
// carry an agent-name/<agent> token, "agent-name/<agent>" is appended.
func buildUserAgent(base, override string) string {
	ua := base
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		ua = trimmed
	}
	if agent, ok := detectAIAgent(); ok && !hasAgentNameToken(ua) {
		ua = strings.TrimSpace(ua)
		if ua == "" {
			return "agent-name/" + agent
		}
		return ua + " agent-name/" + agent
	}
	return ua
}
