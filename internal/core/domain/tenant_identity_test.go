package domain_test

import (
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/core/domain"
)

func TestResume_WorkspaceIntersectionCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A")},
	}

	// Caller attempts to expand permissions with /projects/B
	callerExpand := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A"), filepath.Clean("/projects/B")},
	}

	intersected, err := domain.IntersectSecurityContext(oldCtx, callerExpand)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be strictly /projects/A
	if len(intersected.AllowedWorkspaceRoots) != 1 {
		t.Fatalf("expected 1 root, got: %v", intersected.AllowedWorkspaceRoots)
	}
	expected := filepath.Clean("/projects/A")
	absExpected, _ := filepath.Abs(expected)
	if intersected.AllowedWorkspaceRoots[0] != absExpected {
		t.Fatalf("expected %s, got %s", absExpected, intersected.AllowedWorkspaceRoots[0])
	}

	// Path containment: old allows /projects, caller allows /projects/A -> result /projects/A
	oldParent := &domain.AgentSecurityContext{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects")},
	}
	callerChild := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A")},
	}
	intersected2, err := domain.IntersectSecurityContext(oldParent, callerChild)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intersected2.AllowedWorkspaceRoots) != 1 || intersected2.AllowedWorkspaceRoots[0] != absExpected {
		t.Fatalf("expected child /projects/A, got %v", intersected2.AllowedWorkspaceRoots)
	}

	// Reverse containment: old allows /projects/A, caller allows /projects -> result /projects/A
	oldChild := &domain.AgentSecurityContext{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects/A")},
	}
	callerParent := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/projects")},
	}
	intersected3, err := domain.IntersectSecurityContext(oldChild, callerParent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intersected3.AllowedWorkspaceRoots) != 1 || intersected3.AllowedWorkspaceRoots[0] != absExpected {
		t.Fatalf("expected child /projects/A, got %v", intersected3.AllowedWorkspaceRoots)
	}

	// Disjoint workspaces -> must fail reauthorization!
	callerDisjoint := domain.TenantIdentity{
		TenantID:              "tenant-1",
		Role:                  "user",
		Scopes:                []string{domain.ScopeAgent},
		AllowedWorkspaceRoots: []string{filepath.Clean("/other/folder")},
	}
	_, errDisjoint := domain.IntersectSecurityContext(oldCtx, callerDisjoint)
	if errDisjoint == nil {
		t.Fatalf("expected error for disjoint workspace roots, got nil")
	}
}

func TestResume_NetworkPolicyCannotExpand(t *testing.T) {
	// 1. One side false -> result false
	oldDenied := &domain.AgentSecurityContext{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  false,
			AllowedDomains: []string{"example.com"},
		},
	}
	callerAllowed := domain.TenantIdentity{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"example.com", "other.com"},
		},
	}
	res, err := domain.IntersectSecurityContext(oldDenied, callerAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.NetworkPolicy.AllowOutbound {
		t.Fatalf("expected AllowOutbound false, got true")
	}
	if len(res.NetworkPolicy.AllowedDomains) != 0 {
		t.Fatalf("expected no AllowedDomains when AllowOutbound is false, got: %v", res.NetworkPolicy.AllowedDomains)
	}
	if res.NetworkPolicy.IsURLAllowed("https://example.com") {
		t.Fatalf("IsURLAllowed must be false when AllowOutbound is false")
	}

	// 2. Disjoint whitelists -> must not bypass
	oldWhitelist := &domain.AgentSecurityContext{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"github.com"},
		},
	}
	callerWhitelist := domain.TenantIdentity{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"google.com"},
		},
	}
	resDisjoint, err := domain.IntersectSecurityContext(oldWhitelist, callerWhitelist)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resDisjoint.NetworkPolicy.AllowOutbound {
		t.Fatalf("expected AllowOutbound false when domain intersection is empty, got true")
	}
	if resDisjoint.NetworkPolicy.IsURLAllowed("https://github.com") || resDisjoint.NetworkPolicy.IsURLAllowed("https://google.com") {
		t.Fatalf("disjoint domains must not allow any outbound URL")
	}

	// 3. Subdomain containment: old allows example.com, caller allows api.example.com -> result api.example.com
	oldDomain := &domain.AgentSecurityContext{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"example.com"},
		},
	}
	callerSubdomain := domain.TenantIdentity{
		TenantID: "tenant-1",
		Role:     "user",
		Scopes:   []string{domain.ScopeAgent},
		NetworkPolicy: domain.NetworkPolicy{
			AllowOutbound:  true,
			AllowedDomains: []string{"api.example.com"},
		},
	}
	resSub, err := domain.IntersectSecurityContext(oldDomain, callerSubdomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resSub.NetworkPolicy.AllowOutbound {
		t.Fatalf("expected AllowOutbound true")
	}
	if !resSub.NetworkPolicy.IsURLAllowed("https://api.example.com/v1") {
		t.Fatalf("expected api.example.com to be allowed")
	}
	if resSub.NetworkPolicy.IsURLAllowed("https://other.example.com") {
		t.Fatalf("expected other.example.com to NOT be allowed")
	}

	// 4. BlockedDomains union
	oldDomain.NetworkPolicy.BlockedDomains = []string{"blocked1.com"}
	callerSubdomain.NetworkPolicy.BlockedDomains = []string{"blocked2.com"}
	resUnion, err := domain.IntersectSecurityContext(oldDomain, callerSubdomain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resUnion.NetworkPolicy.BlockedDomains) != 2 {
		t.Fatalf("expected union of blocked domains (2), got: %v", resUnion.NetworkPolicy.BlockedDomains)
	}
}

func TestResume_ShellCannotExpand(t *testing.T) {
	// old false, current true -> false
	oldNoShell := &domain.AgentSecurityContext{
		TenantID:   "tenant-1",
		Role:       "user",
		Scopes:     []string{domain.ScopeAgent},
		AllowShell: false,
	}
	callerShell := domain.TenantIdentity{
		TenantID:   "tenant-1",
		Role:       "user",
		Scopes:     []string{domain.ScopeAgent},
		AllowShell: true,
	}
	res, err := domain.IntersectSecurityContext(oldNoShell, callerShell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AllowShell {
		t.Fatalf("expected AllowShell false, got true")
	}

	// Ensure run_command is stripped from tools
	oldNoShellWithTool := &domain.AgentSecurityContext{
		TenantID:     "tenant-1",
		Role:         "user",
		Scopes:       []string{domain.ScopeAgent},
		AllowShell:   false,
		AllowedTools: []string{"run_command", "read_file"},
	}
	resTools, err := domain.IntersectSecurityContext(oldNoShellWithTool, callerShell)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tool := range resTools.AllowedTools {
		if tool == "run_command" {
			t.Fatalf("run_command must not be allowed when AllowShell is false")
		}
	}
}

func TestResume_ToolsCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:     "tenant-1",
		Role:         "user",
		Scopes:       []string{domain.ScopeAgent},
		AllowedTools: []string{"read_file"},
	}
	callerExpanded := domain.TenantIdentity{
		TenantID:     "tenant-1",
		Role:         "user",
		Scopes:       []string{domain.ScopeAgent},
		AllowedTools: []string{"read_file", "write_file", "delete_file"},
	}
	res, err := domain.IntersectSecurityContext(oldCtx, callerExpanded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AllowedTools) != 1 || res.AllowedTools[0] != "read_file" {
		t.Fatalf("expected only read_file, got: %v", res.AllowedTools)
	}
}

func TestResume_ModelsCannotExpand(t *testing.T) {
	oldCtx := &domain.AgentSecurityContext{
		TenantID:      "tenant-1",
		Role:          "user",
		Scopes:        []string{domain.ScopeAgent},
		AllowedModels: []string{"gemini-1.5-flash"},
	}
	callerExpanded := domain.TenantIdentity{
		TenantID:      "tenant-1",
		Role:          "user",
		Scopes:        []string{domain.ScopeAgent},
		AllowedModels: []string{"gemini-1.5-flash", "gemini-1.5-pro"},
	}
	res, err := domain.IntersectSecurityContext(oldCtx, callerExpanded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.AllowedModels) != 1 || res.AllowedModels[0] != "gemini-1.5-flash" {
		t.Fatalf("expected only gemini-1.5-flash, got: %v", res.AllowedModels)
	}

	// Disjoint models -> error
	callerDisjoint := domain.TenantIdentity{
		TenantID:      "tenant-1",
		Role:          "user",
		Scopes:        []string{domain.ScopeAgent},
		AllowedModels: []string{"claude-3-opus"},
	}
	_, errDisjoint := domain.IntersectSecurityContext(oldCtx, callerDisjoint)
	if errDisjoint == nil {
		t.Fatalf("expected error for disjoint allowed models, got nil")
	}
}
