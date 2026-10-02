package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config chỉ chứa các tham số hạ tầng máy chủ Gateway.
// Tuyệt đối không chứa tài khoản, cookie hay mô hình Gemini/Flow.
type Config struct {
	Server     ServerConfig                 `yaml:"server"`
	Operations Operations                   `yaml:"operations"`
	Profiles   ProfilesConfig               `yaml:"profiles"`
	Media      MediaConfig                  `yaml:"media"`
	Vision     VisionConfig                 `yaml:"vision"`
	Tokens     TokensConfig                 `yaml:"tokens"`
	Failover   FailoverConfig               `yaml:"failover"`
	Storage    StorageConfig                `yaml:"storage"`
	GoldenJob  GoldenJobConfig              `yaml:"golden_job"`
	KeepAlive  KeepAliveConfig              `yaml:"keep_alive"`
	Security   SecurityConfig               `yaml:"security"`
	Alerts     AlertsConfig                 `yaml:"alerts"`
	Cache      CacheConfig                  `yaml:"cache"`
	Admin        AdminConfig                  `yaml:"admin"`
	ChatDefaults ChatDefaultsConfig           `yaml:"chat_defaults"`
	MCPServers   map[string]MCPServerConfig   `yaml:"mcp_servers"`
	Rpcs         map[string]RpcOverrideConfig `yaml:"rpcs"`
}

type ChatDefaultsConfig struct {
	Thinking        *bool `yaml:"thinking"`
	SearchGrounding *bool `yaml:"search_grounding"`
	CodeInterpreter *bool `yaml:"code_interpreter"`
}

func (c ChatDefaultsConfig) DefaultThinking() bool {
	if c.Thinking != nil {
		return *c.Thinking
	}
	return false
}

func (c ChatDefaultsConfig) DefaultSearchGrounding() bool {
	if c.SearchGrounding != nil {
		return *c.SearchGrounding
	}
	return false
}

func (c ChatDefaultsConfig) DefaultCodeInterpreter() bool {
	if c.CodeInterpreter != nil {
		return *c.CodeInterpreter
	}
	return false
}

type MCPServerConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Operations là công tắc từng operation nội bộ. Khóa thiếu nghĩa là bật.
type Operations struct {
	ChatCompletions *bool `yaml:"chat_completions"`
}

func (o Operations) Enabled(operation string) bool {
	if operation == "chat.completions" {
		if o.ChatCompletions != nil {
			return *o.ChatCompletions
		}
	}
	return true
}

type ServerConfig struct {
	Host                  string          `yaml:"host"`
	Port                  int             `yaml:"port"`
	APIKey                string          `yaml:"api_key"`
	AllowedOrigins        []string        `yaml:"allowed_origins"`
	ReadTimeout           time.Duration   `yaml:"read_timeout"`
	WriteTimeout          time.Duration   `yaml:"write_timeout"`
	UpstreamShortTimeout  time.Duration   `yaml:"upstream_short_timeout"`
	UpstreamStreamTimeout time.Duration   `yaml:"upstream_stream_timeout"`
	MaxHeaderBytes        int             `yaml:"max_header_bytes"`
	EnableRequestLog      bool            `yaml:"enable_request_log"`
	TrustedProxies        []string        `yaml:"trusted_proxies"`
	RateLimit             RateLimitConfig `yaml:"rate_limit"`
}

type RateLimitConfig struct {
	MaxRequests   int `yaml:"max_requests"`
	WindowSeconds int `yaml:"window_seconds"`
}

// ShortTimeout là hạn chờ của nzlxg và handshake. Khóa thiếu thì 20 giây.
func (s ServerConfig) ShortTimeout() time.Duration {
	if s.UpstreamShortTimeout > 0 {
		return s.UpstreamShortTimeout
	}
	return 20 * time.Second
}

// StreamTimeout là hạn chờ dài của StreamGenerate và StreamChat.
// Khóa thiếu thì lấy read_timeout, rồi 300 giây. Caller deadline ngắn hơn vẫn thắng.
func (s ServerConfig) StreamTimeout() time.Duration {
	if s.UpstreamStreamTimeout > 0 {
		return s.UpstreamStreamTimeout
	}
	if s.ReadTimeout > 0 {
		return s.ReadTimeout
	}
	return 300 * time.Second
}

type ProfilesConfig struct {
	BaseDir      string `yaml:"base_dir"`       // Thư mục chứa các profile Chrome riêng biệt (mỗi tài khoản 1 folder)
	ChromeBinary string `yaml:"chrome_binary"`  // Đường dẫn tới file thực thi Chrome
	CDPPortStart int    `yaml:"cdp_port_start"` // Cổng khởi đầu cho remote debugging Chrome (mặc định 9222)
}

type MediaConfig struct {
	StorageDir    string `yaml:"storage_dir"`
	BaseURL       string `yaml:"base_url"`
	MaxDiskGB     int    `yaml:"max_disk_gb"`
	RetentionDays int    `yaml:"retention_days"`
}

type VisionConfig struct {
	Enabled             *bool         `yaml:"enabled"`
	MaxImageSizeBytes   int64         `yaml:"max_image_size_bytes"`
	AllowedMimeTypes    []string      `yaml:"allowed_mime_types"`
	HTTPDownloadTimeout time.Duration `yaml:"http_download_timeout"`
	UploadTimeout       time.Duration `yaml:"upload_timeout"`
	UploadMethod        string        `yaml:"upload_method"`
}

func (v VisionConfig) IsEnabled() bool {
	if v.Enabled == nil {
		return true
	}
	return *v.Enabled
}

func (v VisionConfig) GetMaxImageSizeBytes() int64 {
	if v.MaxImageSizeBytes > 0 {
		return v.MaxImageSizeBytes
	}
	return 10 * 1024 * 1024
}

func (v VisionConfig) GetAllowedMimeTypes() []string {
	if len(v.AllowedMimeTypes) > 0 {
		return v.AllowedMimeTypes
	}
	return []string{"image/jpeg", "image/png", "image/webp", "image/gif"}
}

func (v VisionConfig) GetHTTPDownloadTimeout() time.Duration {
	if v.HTTPDownloadTimeout > 0 {
		return v.HTTPDownloadTimeout
	}
	return 30 * time.Second
}

func (v VisionConfig) GetUploadTimeout() time.Duration {
	if v.UploadTimeout > 0 {
		return v.UploadTimeout
	}
	return 60 * time.Second
}

func (v VisionConfig) GetUploadMethod() string {
	if v.UploadMethod != "" {
		return v.UploadMethod
	}
	return "scotty"
}

type TokensConfig struct {
	Encoding             string  `yaml:"encoding"`
	PromptTokenRatio     float64 `yaml:"prompt_token_ratio"`
	CompletionTokenRatio float64 `yaml:"completion_token_ratio"`
	ImageTokensPerTile   int     `yaml:"image_tokens_per_tile"`
}

func (t TokensConfig) GetEncoding() string {
	if t.Encoding != "" {
		return t.Encoding
	}
	return "cl100k_base"
}

func (t TokensConfig) GetPromptRatio() float64 {
	if t.PromptTokenRatio > 0 {
		return t.PromptTokenRatio
	}
	return 0.25
}

func (t TokensConfig) GetCompletionRatio() float64 {
	if t.CompletionTokenRatio > 0 {
		return t.CompletionTokenRatio
	}
	return 0.25
}

func (t TokensConfig) GetImageTokensPerTile() int {
	if t.ImageTokensPerTile > 0 {
		return t.ImageTokensPerTile
	}
	return 258
}

type FailoverConfig struct {
	MaxAttempts     int           `yaml:"max_attempts"`
	CoolingDuration time.Duration `yaml:"cooling_duration"`
}

func (f FailoverConfig) GetMaxAttempts() int {
	if f.MaxAttempts > 0 {
		return f.MaxAttempts
	}
	return 3
}

func (f FailoverConfig) GetCoolingDuration() time.Duration {
	if f.CoolingDuration > 0 {
		return f.CoolingDuration
	}
	return 60 * time.Second
}

type StorageConfig struct {
	DatabasePath string `yaml:"database_path"`
}

type GoldenJobConfig struct {
	Enabled          *bool         `yaml:"enabled"`
	LabAccountPrefix string        `yaml:"lab_account_prefix"`
	Interval         time.Duration `yaml:"interval"`
}

func (g GoldenJobConfig) IsEnabled() bool {
	if g.Enabled == nil {
		return true
	}
	return *g.Enabled
}

type KeepAliveConfig struct {
	Enabled  *bool         `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
}

func (k KeepAliveConfig) IsEnabled() bool {
	if k.Enabled == nil {
		return true
	}
	return *k.Enabled
}

type SecurityConfig struct {
	MasterKey string `yaml:"master_key"`
}

type AlertsConfig struct {
	Webhook WebhookAlertConfig `yaml:"webhook"`
}

type WebhookAlertConfig struct {
	Enabled         *bool         `yaml:"enabled"`
	Provider        string        `yaml:"provider"` // "telegram", "discord", "slack", "generic"
	URL             string        `yaml:"url"`
	Token           string        `yaml:"token"`
	ChatID          string        `yaml:"chat_id"`
	MessageTemplate string        `yaml:"message_template"`
	MaxRetries      int           `yaml:"max_retries"`
	RetryBackoff    time.Duration `yaml:"retry_backoff"`
}

func (w WebhookAlertConfig) IsEnabled() bool {
	if w.Enabled == nil {
		return false
	}
	return *w.Enabled
}

func (w WebhookAlertConfig) GetProvider() string {
	if w.Provider != "" {
		return strings.ToLower(strings.TrimSpace(w.Provider))
	}
	return "generic"
}

func (w WebhookAlertConfig) GetMaxRetries() int {
	if w.MaxRetries > 0 {
		return w.MaxRetries
	}
	return 3
}

func (w WebhookAlertConfig) GetRetryBackoff() time.Duration {
	if w.RetryBackoff > 0 {
		return w.RetryBackoff
	}
	return 1 * time.Second
}

func (w WebhookAlertConfig) GetMessageTemplate() string {
	if w.MessageTemplate != "" {
		return w.MessageTemplate
	}
	return "[DEZUXK ALERT] {error_type} | Tài khoản: {account_id} | Chi tiết: {reason} | Hướng dẫn: Vui lòng mở Chrome để đồng bộ lại CDP qua /v1/profiles/{account_id}/launch hoặc /v1/profiles/{account_id}/sync"
}

type CacheConfig struct {
	Enabled    *bool    `yaml:"enabled"`
	MaxEntries int      `yaml:"max_entries"`
	TTLSeconds int      `yaml:"ttl_seconds"`
	Methods    []string `yaml:"methods"`
}

func (c CacheConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return false
	}
	return *c.Enabled
}

func (c CacheConfig) GetMaxEntries() int {
	if c.MaxEntries > 0 {
		return c.MaxEntries
	}
	return 10000
}

func (c CacheConfig) GetTTL() time.Duration {
	if c.TTLSeconds > 0 {
		return time.Duration(c.TTLSeconds) * time.Second
	}
	return 3600 * time.Second
}

func (c CacheConfig) SupportsMethod(method string) bool {
	if !c.IsEnabled() {
		return false
	}
	method = strings.ToLower(strings.TrimSpace(method))
	for _, m := range c.Methods {
		if strings.ToLower(strings.TrimSpace(m)) == method {
			return true
		}
	}
	return false
}

type AdminConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	SessionToken string `yaml:"session_token"`
}

func (a AdminConfig) IsEnabled() bool {
	if a.Enabled == nil {
		return true
	}
	return *a.Enabled
}

func (a AdminConfig) GetUsername() string {
	if a.Username != "" {
		return a.Username
	}
	return "admin"
}

func (a AdminConfig) GetPassword() string {
	if a.Password != "" {
		return a.Password
	}
	return "dezuxk_admin_secret_pass"
}

func (a AdminConfig) GetSessionToken() string {
	if a.SessionToken != "" {
		return a.SessionToken
	}
	return "dezuxk_admin_token"
}

type RpcOverrideConfig struct {
	PathPattern string `yaml:"path_pattern"`
	TargetHost  string `yaml:"target_host"`
	RequiresAt  *bool  `yaml:"requires_at"`
	Description string `yaml:"description"`
}

// LoadConfig đọc file cấu hình hạ tầng thuần túy. Nếu lỗi là dừng ngay, không fallback.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("đường dẫn file cấu hình không được để trống")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("không thể đọc file cấu hình tại %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("lỗi cú pháp YAML trong file %s: %w", path, err)
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("cấu hình không hợp lệ trong %s: %w", path, err)
	}

	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if h := os.Getenv("DEZUXK_HOST"); h != "" {
		cfg.Server.Host = h
	} else if h := os.Getenv("HOST"); h != "" {
		cfg.Server.Host = h
	}

	if pStr := os.Getenv("DEZUXK_PORT"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			cfg.Server.Port = p
		}
	} else if pStr := os.Getenv("PORT"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			cfg.Server.Port = p
		}
	}

	if k := os.Getenv("DEZUXK_API_KEY"); k != "" {
		cfg.Server.APIKey = k
	}
	if mk := os.Getenv("DEZUXK_MASTER_KEY"); mk != "" {
		cfg.Security.MasterKey = mk
	}
	if cb := os.Getenv("DEZUXK_CHROME_BINARY"); cb != "" {
		cfg.Profiles.ChromeBinary = cb
	}
	if pd := os.Getenv("DEZUXK_PROFILES_DIR"); pd != "" {
		cfg.Profiles.BaseDir = pd
	}
	if dp := os.Getenv("DEZUXK_DATABASE_PATH"); dp != "" {
		cfg.Storage.DatabasePath = dp
	}
	if u := os.Getenv("DEZUXK_ADMIN_USERNAME"); u != "" {
		cfg.Admin.Username = u
	}
	if pw := os.Getenv("DEZUXK_ADMIN_PASSWORD"); pw != "" {
		cfg.Admin.Password = pw
	}
}

// Validate kiểm tra tính hợp lệ của hạ tầng máy chủ
func (c *Config) Validate() error {
	if c.Server.Host == "" {
		return errors.New("server.host là bắt buộc (ví dụ: '127.0.0.1')")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port không hợp lệ: %d (phải từ 1 đến 65535)", c.Server.Port)
	}
	if c.Profiles.BaseDir == "" {
		return errors.New("profiles.base_dir là bắt buộc (thư mục lưu profile Chrome từng tài khoản)")
	}

	return nil
}
