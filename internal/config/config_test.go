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

	if !cfg.Operations.Enabled("chat.completions") {
		t.Error("expected chat.completions to be enabled")
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
}

func TestConfig_ProductionSecretsValidation(t *testing.T) {
	// 1. Missing master_key in production must fail
	cfg := &config.Config{
		Environment: "production",
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Profiles: config.ProfilesConfig{
			BaseDir: "./profiles",
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("expected error when master_key is missing in production")
	}

	// 2. Default password in production must fail
	cfg.Security.MasterKey = "a-very-secure-production-master-key-32b"
	cfg.Admin.Password = "dezuxk_admin_secret_pass"
	cfg.Admin.SessionToken = "random-token-123"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when admin password uses default in production")
	}

	// 3. Default session token in production must fail
	cfg.Admin.Password = "secure-production-password-2026"
	cfg.Admin.SessionToken = "dezuxk_admin_token"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when admin session token uses default in production")
	}

	// 4. Missing APIKey in production must fail
	cfg.Admin.SessionToken = "super-secret-random-admin-session-token-998877"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when server.api_key is missing in production")
	}

	// 5. Proper secrets must pass
	cfg.Server.APIKey = "dezuxk-prod-api-key-test"
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid production config to pass, got: %v", err)
	}
}
