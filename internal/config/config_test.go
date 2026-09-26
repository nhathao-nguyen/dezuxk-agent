package config_test

import (
	"path/filepath"
	"testing"

	"dezuxk-gateway/internal/config"
)

func TestLoadConfig_YamlWithOperations(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.yaml")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("failed to load configs/config.yaml: %v", err)
	}

	if !cfg.Operations.Enabled("flow.get_credits") {
		t.Error("expected flow.get_credits to be enabled")
	}
	if !cfg.Operations.Enabled("chat.completions") {
		t.Error("expected chat.completions to be enabled")
	}
	if cfg.Operations.Enabled("images.generations") {
		t.Error("expected images.generations to be disabled")
	}
	if cfg.Operations.Enabled("videos.generations") {
		t.Error("expected videos.generations to be disabled")
	}

	// Test Vision config
	if !cfg.Vision.IsEnabled() {
		t.Error("expected vision to be enabled")
	}
	if cfg.Vision.GetMaxImageSizeBytes() <= 0 {
		t.Errorf("expected max_image_size_bytes > 0, got %d", cfg.Vision.GetMaxImageSizeBytes())
	}
	if len(cfg.Vision.GetAllowedMimeTypes()) == 0 {
		t.Error("expected allowed_mime_types to be non-empty")
	}
	if cfg.Vision.GetUploadMethod() != "scotty" {
		t.Errorf("expected upload_method to be scotty, got %s", cfg.Vision.GetUploadMethod())
	}

	// Test Tokens config
	if cfg.Tokens.GetEncoding() != "cl100k_base" {
		t.Errorf("expected encoding cl100k_base, got %s", cfg.Tokens.GetEncoding())
	}
	if cfg.Tokens.GetPromptRatio() <= 0 {
		t.Errorf("expected prompt_token_ratio > 0, got %f", cfg.Tokens.GetPromptRatio())
	}
	if cfg.Tokens.GetImageTokensPerTile() <= 0 {
		t.Errorf("expected image_tokens_per_tile > 0, got %d", cfg.Tokens.GetImageTokensPerTile())
	}

	// Test Failover config
	if cfg.Failover.GetMaxAttempts() < 1 {
		t.Errorf("expected max_attempts >= 1, got %d", cfg.Failover.GetMaxAttempts())
	}
	if cfg.Failover.GetCoolingDuration() <= 0 {
		t.Errorf("expected cooling_duration > 0, got %v", cfg.Failover.GetCoolingDuration())
	}

	// Test Cache config (Phase 3)
	if !cfg.Cache.IsEnabled() {
		t.Error("expected cache to be enabled")
	}
	if cfg.Cache.GetMaxEntries() <= 0 {
		t.Errorf("expected cache max_entries > 0, got %d", cfg.Cache.GetMaxEntries())
	}
	if cfg.Cache.GetTTL() <= 0 {
		t.Errorf("expected cache ttl > 0, got %v", cfg.Cache.GetTTL())
	}
	if !cfg.Cache.SupportsMethod("chat") {
		t.Error("expected cache to support method 'chat'")
	}
	if !cfg.Cache.SupportsMethod("credits") {
		t.Error("expected cache to support method 'credits'")
	}
	if cfg.Cache.SupportsMethod("unsupported_method") {
		t.Error("expected cache not to support 'unsupported_method'")
	}

	// Test Admin config (Phase 3)
	if !cfg.Admin.IsEnabled() {
		t.Error("expected admin to be enabled")
	}
	if cfg.Admin.GetUsername() == "" {
		t.Error("expected admin username to be non-empty")
	}
	if cfg.Admin.GetPassword() == "" {
		t.Error("expected admin password to be non-empty")
	}
	if cfg.Admin.GetSessionToken() == "" {
		t.Error("expected admin session_token to be non-empty")
	}
}

