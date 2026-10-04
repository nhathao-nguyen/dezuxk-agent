package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config chỉ chứa các tham số hạ tầng máy chủ Gateway.
// Tuyệt đối không chứa tài khoản, cookie hay mô hình Gemini/Flow.
// CHÚ Ý BẢO MẬT: Bất kỳ secret nào từng được commit lên Git phải được xoay (rotate) ngay lập tức!
type Config struct {
	Environment    string                           `yaml:"environment"` // "development", "staging", "production"
	TestMode       bool                             `yaml:"test_mode"`   // Chế độ kiểm thử (chỉ dùng cho CI/dev test)
	Server         ServerConfig                     `yaml:"server"`
	Operations     Operations                       `yaml:"operations"`
	Profiles       ProfilesConfig                   `yaml:"profiles"`
	Media          MediaConfig                      `yaml:"media"`
	Vision         VisionConfig                     `yaml:"vision"`
	Tokens         TokensConfig                     `yaml:"tokens"`
	Failover       FailoverConfig                   `yaml:"failover"`
	Storage        StorageConfig                    `yaml:"storage"`
	Distributed    DistributedConfig                `yaml:"distributed"`
	Cluster        ClusterConfig                    `yaml:"cluster"`
	GoldenJob      GoldenJobConfig                  `yaml:"golden_job"`
	KeepAlive      KeepAliveConfig                  `yaml:"keep_alive"`
	Security       SecurityConfig                   `yaml:"security"`
	Alerts         AlertsConfig                     `yaml:"alerts"`
	Cache          CacheConfig                      `yaml:"cache"`
	Admin          AdminConfig                      `yaml:"admin"`
	ChatDefaults   ChatDefaultsConfig               `yaml:"chat_defaults"`
	MCPServers     map[string]MCPServerConfig       `yaml:"mcp_servers"`
	Rpcs           map[string]RpcOverrideConfig     `yaml:"rpcs"`
	RuntimeCatalog RuntimeCatalogConfig             `yaml:"runtime_catalog"`
	ModelSelection ModelSelectionConfig             `yaml:"model_selection"`
	ModelAliases   map[string]ModelAliasEntryConfig `yaml:"model_aliases"`
	Agent          AgentConfig                      `yaml:"agent"`
}

type RuntimeCatalogConfig struct {
	Enabled              *bool         `yaml:"enabled"`
	RefreshInterval      time.Duration `yaml:"refresh_interval"`
	StaleAfter           time.Duration `yaml:"stale_after"`
	DeprecateAfter       time.Duration `yaml:"deprecate_after"`
	DiscoveryConcurrency int           `yaml:"discovery_concurrency"`
	PerAccountTimeout    time.Duration `yaml:"per_account_timeout"`
	GlobalTimeout        time.Duration `yaml:"global_timeout"`
}

func (r RuntimeCatalogConfig) IsEnabled() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

func (r RuntimeCatalogConfig) GetRefreshInterval() time.Duration {
	if r.RefreshInterval > 0 {
		return r.RefreshInterval
	}
	return 15 * time.Minute
}

func (r RuntimeCatalogConfig) GetStaleAfter() time.Duration {
	if r.StaleAfter > 0 {
		return r.StaleAfter
	}
	return 1 * time.Hour
}

func (r RuntimeCatalogConfig) GetDeprecateAfter() time.Duration {
	if r.DeprecateAfter > 0 {
		return r.DeprecateAfter
	}
	return 24 * time.Hour
}

func (r RuntimeCatalogConfig) GetDiscoveryConcurrency() int {
	if r.DiscoveryConcurrency > 0 {
		return r.DiscoveryConcurrency
	}
	return 5
}

func (r RuntimeCatalogConfig) GetPerAccountTimeout() time.Duration {
	if r.PerAccountTimeout > 0 {
		return r.PerAccountTimeout
	}
	return 20 * time.Second
}

func (r RuntimeCatalogConfig) GetGlobalTimeout() time.Duration {
	if r.GlobalTimeout > 0 {
		return r.GlobalTimeout
	}
	return 2 * time.Minute
}

type ModelSelectionConfig struct {
	DefaultPolicy string `yaml:"default_policy"`
	AllowFallback *bool  `yaml:"allow_fallback"`
}

func (m ModelSelectionConfig) GetDefaultPolicy() string {
	if m.DefaultPolicy != "" {
		return m.DefaultPolicy
	}
	return "balanced"
}

func (m ModelSelectionConfig) IsAllowFallback() bool {
	if m.AllowFallback == nil {
		return true
	}
	return *m.AllowFallback
}

type ModelAliasEntryConfig struct {
	Capability  string `yaml:"capability"`
	Preference  string `yaml:"preference"`
	TargetModel string `yaml:"target_model"`
}

type AgentConfig struct {
	DefaultModelPolicy string `yaml:"default_model_policy"`
	DefaultModel       string `yaml:"default_model"`
	Persona            string `yaml:"persona"`
	ProjectContext     string `yaml:"project_context"`
}

func (a AgentConfig) GetDefaultModelPolicy() string {
	if a.DefaultModelPolicy != "" {
		return a.DefaultModelPolicy
	}
	return "balanced"
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
	Host                   string          `yaml:"host"`
	Port                   int             `yaml:"port"`
	APIKey                 string          `yaml:"api_key"`
	MetricsToken           string          `yaml:"metrics_token"`
	AllowedOrigins         []string        `yaml:"allowed_origins"`
	ReadTimeout            time.Duration   `yaml:"read_timeout"`
	WriteTimeout           time.Duration   `yaml:"write_timeout"`
	UpstreamShortTimeout   time.Duration   `yaml:"upstream_short_timeout"`
	UpstreamStreamTimeout  time.Duration   `yaml:"upstream_stream_timeout"`
	UpstreamConnectTimeout time.Duration   `yaml:"upstream_connect_timeout"`
	UpstreamIdleTimeout    time.Duration   `yaml:"stream_idle_timeout"`
	UpstreamMaxDuration    time.Duration   `yaml:"stream_max_duration"`
	MaxHeaderBytes         int             `yaml:"max_header_bytes"`
	EnableRequestLog       bool            `yaml:"enable_request_log"`
	TrustedProxies         []string        `yaml:"trusted_proxies"`
	ShutdownTimeout        time.Duration   `yaml:"shutdown_timeout"`
	RateLimit              RateLimitConfig `yaml:"rate_limit"`
}

type RateLimitConfig struct {
	MaxRequests   int `yaml:"max_requests"`
	WindowSeconds int `yaml:"window_seconds"`
}

// GetShutdownTimeout trả về thời gian chờ an toàn khi tắt máy chủ (mặc định 15 giây)
func (s ServerConfig) GetShutdownTimeout() time.Duration {
	if s.ShutdownTimeout > 0 {
		return s.ShutdownTimeout
	}
	return 15 * time.Second
}

// ShortTimeout là hạn chờ của nzlxg và handshake. Khóa thiếu thì 20 giây.
func (s ServerConfig) ShortTimeout() time.Duration {
	if s.UpstreamShortTimeout > 0 {
		return s.UpstreamShortTimeout
	}
	return 20 * time.Second
}

// ConnectTimeout trả về thời gian chờ thiết lập kết nối tới upstream (mặc định 10s)
func (s ServerConfig) ConnectTimeout() time.Duration {
	if s.UpstreamConnectTimeout > 0 {
		return s.UpstreamConnectTimeout
	}
	return 10 * time.Second
}

// StreamIdleTimeout trả về thời gian chờ tối đa giữa 2 lần nhận dữ liệu từ upstream (mặc định 60s)
func (s ServerConfig) StreamIdleTimeout() time.Duration {
	if s.UpstreamIdleTimeout > 0 {
		return s.UpstreamIdleTimeout
	}
	return 60 * time.Second
}

// StreamMaxDuration trả về thời lượng tối đa cho toàn bộ phiên sinh nội dung dài (mặc định 30 phút = 1800s)
func (s ServerConfig) StreamMaxDuration() time.Duration {
	if s.UpstreamMaxDuration > 0 {
		return s.UpstreamMaxDuration
	}
	return 1800 * time.Second
}

// StreamTimeout là hạn chờ dài của StreamGenerate và StreamChat.
// Ưu tiên: UpstreamStreamTimeout > UpstreamMaxDuration > ReadTimeout > 1800s.
func (s ServerConfig) StreamTimeout() time.Duration {
	if s.UpstreamStreamTimeout > 0 {
		return s.UpstreamStreamTimeout
	}
	if s.UpstreamMaxDuration > 0 {
		return s.UpstreamMaxDuration
	}
	if s.ReadTimeout > 0 {
		return s.ReadTimeout
	}
	return 1800 * time.Second
}

type ProfilesConfig struct {
	BaseDir             string `yaml:"base_dir"`              // Thư mục chứa các profile Chrome riêng biệt (mỗi tài khoản 1 folder)
	ChromeBinary        string `yaml:"chrome_binary"`         // Đường dẫn tới file thực thi Chrome
	CDPPortStart        int    `yaml:"cdp_port_start"`        // Cổng khởi đầu cho remote debugging Chrome (mặc định 9222)
	ControlPlaneEnabled *bool  `yaml:"control_plane_enabled"` // Node này có quyền điều khiển Chrome profiles cục bộ hay không
}

func (p ProfilesConfig) IsControlPlaneEnabled() bool {
	if p.ControlPlaneEnabled != nil {
		return *p.ControlPlaneEnabled
	}
	return true
}

type MediaConfig struct {
	Driver        string        `yaml:"driver"` // "local" | "s3"
	StorageDir    string        `yaml:"storage_dir"`
	BaseURL       string        `yaml:"base_url"`
	MaxDiskGB     int           `yaml:"max_disk_gb"`
	RetentionDays int           `yaml:"retention_days"`
	S3            S3MediaConfig `yaml:"s3"`
}

func (m MediaConfig) GetDriver() string {
	d := strings.ToLower(strings.TrimSpace(m.Driver))
	if d == "" {
		return "local"
	}
	return d
}

type S3MediaConfig struct {
	Endpoint     string `yaml:"endpoint"`
	Region       string `yaml:"region"`
	Bucket       string `yaml:"bucket"`
	AccessKey    string `yaml:"access_key"`
	SecretKey    string `yaml:"secret_key"`
	UsePathStyle bool   `yaml:"use_path_style"`
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

type ClusterConfig struct {
	Enabled bool   `yaml:"enabled"`
	NodeID  string `yaml:"node_id"`
}

func (c ClusterConfig) IsEnabled() bool {
	return c.Enabled
}

func (c ClusterConfig) GetNodeID() string {
	if strings.TrimSpace(c.NodeID) != "" {
		return strings.TrimSpace(c.NodeID)
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "node"
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", hostname, hex.EncodeToString(b))
}

type StorageConfig struct {
	Driver              string         `yaml:"driver"` // "sqlite" (mặc định) | "postgres"
	DatabasePath        string         `yaml:"database_path"`
	Postgres            PostgresConfig `yaml:"postgres"`
	AllowMemoryFallback bool           `yaml:"allow_memory_fallback"`
}

type PostgresConfig struct {
	DSN               string        `yaml:"dsn"`
	Host              string        `yaml:"host"`
	Port              int           `yaml:"port"`
	User              string        `yaml:"user"`
	Password          string        `yaml:"password"`
	DBName            string        `yaml:"dbname"`
	SSLMode           string        `yaml:"sslmode"`
	MaxConns          int32         `yaml:"max_conns"`
	MinConns          int32         `yaml:"min_conns"`
	MaxConnLifetime   time.Duration `yaml:"max_conn_lifetime"`
	MaxConnIdleTime   time.Duration `yaml:"max_conn_idle_time"`
	HealthCheckPeriod time.Duration `yaml:"health_check_period"`
	ConnectTimeout    time.Duration `yaml:"connect_timeout"`
	StatementTimeout  time.Duration `yaml:"statement_timeout"`
}

func (p PostgresConfig) GetPort() int {
	if p.Port > 0 {
		return p.Port
	}
	return 5432
}

func (p PostgresConfig) GetSSLMode() string {
	if p.SSLMode != "" {
		return strings.TrimSpace(p.SSLMode)
	}
	return "disable"
}

func (p PostgresConfig) GetMaxConns() int32 {
	if p.MaxConns > 0 {
		return p.MaxConns
	}
	return 25
}

func (p PostgresConfig) GetMinConns() int32 {
	if p.MinConns > 0 {
		return p.MinConns
	}
	return 5
}

func (p PostgresConfig) GetMaxConnLifetime() time.Duration {
	if p.MaxConnLifetime > 0 {
		return p.MaxConnLifetime
	}
	return time.Hour
}

func (p PostgresConfig) GetMaxConnIdleTime() time.Duration {
	if p.MaxConnIdleTime > 0 {
		return p.MaxConnIdleTime
	}
	return 30 * time.Minute
}

func (p PostgresConfig) GetHealthCheckPeriod() time.Duration {
	if p.HealthCheckPeriod > 0 {
		return p.HealthCheckPeriod
	}
	return time.Minute
}

func (p PostgresConfig) GetConnectTimeout() time.Duration {
	if p.ConnectTimeout > 0 {
		return p.ConnectTimeout
	}
	return 5 * time.Second
}

func (p PostgresConfig) BuildDSN() string {
	if strings.TrimSpace(p.DSN) != "" {
		return strings.TrimSpace(p.DSN)
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", p.Host, p.GetPort()),
		Path:   "/" + strings.TrimPrefix(p.DBName, "/"),
	}
	if p.User != "" || p.Password != "" {
		u.User = url.UserPassword(p.User, p.Password)
	}
	q := u.Query()
	q.Set("sslmode", p.GetSSLMode())
	if p.ConnectTimeout > 0 {
		q.Set("connect_timeout", strconv.Itoa(int(p.ConnectTimeout.Seconds())))
	}
	if p.StatementTimeout > 0 {
		q.Set("statement_timeout", strconv.Itoa(int(p.StatementTimeout.Milliseconds())))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (p PostgresConfig) SanitizedDSN() string {
	raw := p.BuildDSN()
	parsed, err := url.Parse(raw)
	if err != nil {
		return "postgres://[sanitized]"
	}
	return parsed.Redacted()
}

type DistributedConfig struct {
	Enabled bool        `yaml:"enabled"`
	Driver  string      `yaml:"driver"` // "redis"
	Redis   RedisConfig `yaml:"redis"`
}

type RedisConfig struct {
	Mode         string        `yaml:"mode"` // "standalone", "sentinel", "cluster"
	Addr         string        `yaml:"addr"`
	Addrs        []string      `yaml:"addrs"`
	MasterName   string        `yaml:"master_name"`
	Username     string        `yaml:"username"`
	Password     string        `yaml:"password"`
	DB           int           `yaml:"db"`
	PoolSize     int           `yaml:"pool_size"`
	MinIdleConns int           `yaml:"min_idle_conns"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

func (r RedisConfig) GetMode() string {
	m := strings.ToLower(strings.TrimSpace(r.Mode))
	if m == "" {
		return "standalone"
	}
	return m
}

func (r RedisConfig) GetAddrs() []string {
	if len(r.Addrs) > 0 {
		return r.Addrs
	}
	if strings.TrimSpace(r.Addr) != "" {
		return []string{strings.TrimSpace(r.Addr)}
	}
	return nil
}

func (r RedisConfig) GetPoolSize() int {
	if r.PoolSize > 0 {
		return r.PoolSize
	}
	return 50
}

func (r RedisConfig) GetMinIdleConns() int {
	if r.MinIdleConns > 0 {
		return r.MinIdleConns
	}
	return 10
}

func (r RedisConfig) GetDialTimeout() time.Duration {
	if r.DialTimeout > 0 {
		return r.DialTimeout
	}
	return 5 * time.Second
}

func (r RedisConfig) GetReadTimeout() time.Duration {
	if r.ReadTimeout > 0 {
		return r.ReadTimeout
	}
	return 3 * time.Second
}

func (r RedisConfig) GetWriteTimeout() time.Duration {
	if r.WriteTimeout > 0 {
		return r.WriteTimeout
	}
	return 3 * time.Second
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

func (c *Config) IsProduction() bool {
	if strings.EqualFold(strings.TrimSpace(c.Environment), "production") {
		return true
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_ENV")))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	}
	return env == "production"
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
	return a.Password
}

func (a AdminConfig) GetSessionToken() string {
	return a.SessionToken
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
		if os.IsNotExist(err) && (path == "configs/config.yaml" || path == "./configs/config.yaml") {
			examplePath := "configs/config.example.yaml"
			if exData, exErr := os.ReadFile(examplePath); exErr == nil {
				data = exData
				err = nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("không thể đọc file cấu hình tại %s: %w", path, err)
		}
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
	if env := os.Getenv("DEZUXK_ENV"); env != "" {
		cfg.Environment = env
	} else if env := os.Getenv("ENV"); env != "" {
		cfg.Environment = env
	}

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
	if cpe := os.Getenv("DEZUXK_PROFILES_CONTROL_PLANE_ENABLED"); cpe != "" {
		val := (cpe == "true" || cpe == "1")
		cfg.Profiles.ControlPlaneEnabled = &val
	}
	if rcConc := os.Getenv("DEZUXK_RUNTIME_CATALOG_CONCURRENCY"); rcConc != "" {
		if c, err := strconv.Atoi(rcConc); err == nil && c > 0 {
			cfg.RuntimeCatalog.DiscoveryConcurrency = c
		}
	}
	if rcAccTimeout := os.Getenv("DEZUXK_RUNTIME_CATALOG_ACCOUNT_TIMEOUT"); rcAccTimeout != "" {
		if d, err := time.ParseDuration(rcAccTimeout); err == nil && d > 0 {
			cfg.RuntimeCatalog.PerAccountTimeout = d
		}
	}
	if rcGlobalTimeout := os.Getenv("DEZUXK_RUNTIME_CATALOG_GLOBAL_TIMEOUT"); rcGlobalTimeout != "" {
		if d, err := time.ParseDuration(rcGlobalTimeout); err == nil && d > 0 {
			cfg.RuntimeCatalog.GlobalTimeout = d
		}
	}
	if dp := os.Getenv("DEZUXK_DATABASE_PATH"); dp != "" {
		cfg.Storage.DatabasePath = dp
	}
	if sd := os.Getenv("DEZUXK_STORAGE_DRIVER"); sd != "" {
		cfg.Storage.Driver = sd
	}
	if pgDSN := os.Getenv("DEZUXK_POSTGRES_DSN"); pgDSN != "" {
		cfg.Storage.Postgres.DSN = pgDSN
	}
	if pgHost := os.Getenv("DEZUXK_POSTGRES_HOST"); pgHost != "" {
		cfg.Storage.Postgres.Host = pgHost
	}
	if pgPortStr := os.Getenv("DEZUXK_POSTGRES_PORT"); pgPortStr != "" {
		if p, err := strconv.Atoi(pgPortStr); err == nil && p > 0 {
			cfg.Storage.Postgres.Port = p
		}
	}
	if pgUser := os.Getenv("DEZUXK_POSTGRES_USER"); pgUser != "" {
		cfg.Storage.Postgres.User = pgUser
	}
	if pgPass := os.Getenv("DEZUXK_POSTGRES_PASSWORD"); pgPass != "" {
		cfg.Storage.Postgres.Password = pgPass
	}
	if pgDB := os.Getenv("DEZUXK_POSTGRES_DBNAME"); pgDB != "" {
		cfg.Storage.Postgres.DBName = pgDB
	}
	if pgSSL := os.Getenv("DEZUXK_POSTGRES_SSLMODE"); pgSSL != "" {
		cfg.Storage.Postgres.SSLMode = pgSSL
	}
	if pgMaxConnsStr := os.Getenv("DEZUXK_POSTGRES_MAX_CONNS"); pgMaxConnsStr != "" {
		if mc, err := strconv.Atoi(pgMaxConnsStr); err == nil && mc > 0 {
			cfg.Storage.Postgres.MaxConns = int32(mc)
		}
	}
	if pgMinConnsStr := os.Getenv("DEZUXK_POSTGRES_MIN_CONNS"); pgMinConnsStr != "" {
		if mc, err := strconv.Atoi(pgMinConnsStr); err == nil && mc >= 0 {
			cfg.Storage.Postgres.MinConns = int32(mc)
		}
	}

	if de := os.Getenv("DEZUXK_DISTRIBUTED_ENABLED"); de != "" {
		cfg.Distributed.Enabled = (de == "true" || de == "1")
	}
	if ra := os.Getenv("DEZUXK_REDIS_ADDR"); ra != "" {
		cfg.Distributed.Redis.Addr = ra
	}
	if ras := os.Getenv("DEZUXK_REDIS_ADDRS"); ras != "" {
		parts := strings.Split(ras, ",")
		var addrs []string
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				addrs = append(addrs, trimmed)
			}
		}
		if len(addrs) > 0 {
			cfg.Distributed.Redis.Addrs = addrs
		}
	}
	if rp := os.Getenv("DEZUXK_REDIS_PASSWORD"); rp != "" {
		cfg.Distributed.Redis.Password = rp
	}
	if rdbStr := os.Getenv("DEZUXK_REDIS_DB"); rdbStr != "" {
		if rdb, err := strconv.Atoi(rdbStr); err == nil && rdb >= 0 {
			cfg.Distributed.Redis.DB = rdb
		}
	}
	if rm := os.Getenv("DEZUXK_REDIS_MODE"); rm != "" {
		cfg.Distributed.Redis.Mode = rm
	}
	if rmn := os.Getenv("DEZUXK_REDIS_MASTER_NAME"); rmn != "" {
		cfg.Distributed.Redis.MasterName = rmn
	}

	if ce := os.Getenv("DEZUXK_CLUSTER_ENABLED"); ce != "" {
		cfg.Cluster.Enabled = (ce == "true" || ce == "1")
	}
	if cn := os.Getenv("DEZUXK_CLUSTER_NODE_ID"); cn != "" {
		cfg.Cluster.NodeID = cn
	}

	if md := os.Getenv("DEZUXK_MEDIA_DRIVER"); md != "" {
		cfg.Media.Driver = md
	}
	if s3ep := os.Getenv("DEZUXK_S3_ENDPOINT"); s3ep != "" {
		cfg.Media.S3.Endpoint = s3ep
	}
	if s3reg := os.Getenv("DEZUXK_S3_REGION"); s3reg != "" {
		cfg.Media.S3.Region = s3reg
	}
	if s3bkt := os.Getenv("DEZUXK_S3_BUCKET"); s3bkt != "" {
		cfg.Media.S3.Bucket = s3bkt
	}
	if s3ak := os.Getenv("DEZUXK_S3_ACCESS_KEY"); s3ak != "" {
		cfg.Media.S3.AccessKey = s3ak
	}
	if s3sk := os.Getenv("DEZUXK_S3_SECRET_KEY"); s3sk != "" {
		cfg.Media.S3.SecretKey = s3sk
	}
	if s3ps := os.Getenv("DEZUXK_S3_USE_PATH_STYLE"); s3ps != "" {
		cfg.Media.S3.UsePathStyle = (s3ps == "true" || s3ps == "1")
	}

	if u := os.Getenv("DEZUXK_ADMIN_USERNAME"); u != "" {
		cfg.Admin.Username = u
	}
	if pw := os.Getenv("DEZUXK_ADMIN_PASSWORD"); pw != "" {
		cfg.Admin.Password = pw
	}
	if st := os.Getenv("DEZUXK_ADMIN_SESSION_TOKEN"); st != "" {
		cfg.Admin.SessionToken = st
	}
	if mt := os.Getenv("DEZUXK_METRICS_TOKEN"); mt != "" {
		cfg.Server.MetricsToken = mt
	}
	if rlMax := os.Getenv("DEZUXK_RATE_LIMIT_MAX_REQUESTS"); rlMax != "" {
		if val, err := strconv.Atoi(rlMax); err == nil && val > 0 {
			cfg.Server.RateLimit.MaxRequests = val
		}
	}
	if rlWin := os.Getenv("DEZUXK_RATE_LIMIT_WINDOW_SECONDS"); rlWin != "" {
		if val, err := strconv.Atoi(rlWin); err == nil && val > 0 {
			cfg.Server.RateLimit.WindowSeconds = val
		}
	}

	if tm := os.Getenv("DEZUXK_TEST_MODE"); tm != "" {
		cfg.TestMode = (tm == "true" || tm == "1")
	}
	if ao := os.Getenv("DEZUXK_ALLOWED_ORIGINS"); ao != "" {
		parts := strings.Split(ao, ",")
		var origins []string
		for _, o := range parts {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				origins = append(origins, trimmed)
			}
		}
		if len(origins) > 0 {
			cfg.Server.AllowedOrigins = origins
		}
	}
	if tp := os.Getenv("DEZUXK_TRUSTED_PROXIES"); tp != "" {
		parts := strings.Split(tp, ",")
		var proxies []string
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				proxies = append(proxies, trimmed)
			}
		}
		if len(proxies) > 0 {
			cfg.Server.TrustedProxies = proxies
		}
	}

	if rc := os.Getenv("DEZUXK_RUNTIME_CATALOG_ENABLED"); rc != "" {
		enabled := (rc == "true" || rc == "1")
		cfg.RuntimeCatalog.Enabled = &enabled
	}
	if ap := os.Getenv("DEZUXK_AGENT_PERSONA"); ap != "" {
		cfg.Agent.Persona = ap
	}
	if apc := os.Getenv("DEZUXK_AGENT_PROJECT_CONTEXT"); apc != "" {
		cfg.Agent.ProjectContext = apc
	}
	if admp := os.Getenv("DEZUXK_AGENT_DEFAULT_MODEL_POLICY"); admp != "" {
		cfg.Agent.DefaultModelPolicy = admp
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

	// Kiểm tra storage driver
	storageDriver := strings.ToLower(strings.TrimSpace(c.Storage.Driver))
	if storageDriver == "" {
		storageDriver = "sqlite"
	}
	if storageDriver != "sqlite" && storageDriver != "postgres" {
		return fmt.Errorf("storage.driver không được hỗ trợ: %q (chỉ hỗ trợ 'sqlite' hoặc 'postgres')", c.Storage.Driver)
	}
	if storageDriver == "postgres" {
		hasDSN := strings.TrimSpace(c.Storage.Postgres.DSN) != ""
		hasHostDB := strings.TrimSpace(c.Storage.Postgres.Host) != "" && strings.TrimSpace(c.Storage.Postgres.DBName) != ""
		if !hasDSN && !hasHostDB {
			return errors.New("storage.postgres.host và storage.postgres.dbname (hoặc storage.postgres.dsn) là bắt buộc khi chọn driver 'postgres'")
		}
	}

	// Kiểm tra multi-node cluster config (fail-fast: không giả vờ multi-node ready nếu cấu hình chưa đủ)
	if c.Distributed.Enabled || c.Cluster.Enabled {
		distDriver := strings.ToLower(strings.TrimSpace(c.Distributed.Driver))
		if distDriver == "" {
			distDriver = "redis"
		}
		if distDriver != "redis" {
			return fmt.Errorf("distributed.driver không được hỗ trợ: %q (chỉ hỗ trợ 'redis')", c.Distributed.Driver)
		}
		addrs := c.Distributed.Redis.GetAddrs()
		if len(addrs) == 0 || addrs[0] == "" {
			return errors.New("distributed.redis.addr là bắt buộc khi kích hoạt chế độ cụm phân tán (distributed.enabled: true)")
		}
	}

	if c.Cluster.Enabled {
		if storageDriver != "postgres" {
			return errors.New("chế độ cụm phân tán cluster.enabled yêu cầu storage.driver='postgres' (PostgreSQL là source-of-truth cho agent leases)")
		}
		if !c.Distributed.Enabled {
			return errors.New("chế độ cụm phân tán cluster.enabled yêu cầu distributed.enabled=true")
		}
	}

	mediaDriver := c.Media.GetDriver()
	if mediaDriver != "local" && mediaDriver != "s3" {
		return fmt.Errorf("media.driver không được hỗ trợ: %q (chỉ hỗ trợ 'local' hoặc 's3')", c.Media.Driver)
	}
	if mediaDriver == "s3" {
		if strings.TrimSpace(c.Media.S3.Bucket) == "" {
			return errors.New("media.s3.bucket là bắt buộc khi chọn media driver 's3'")
		}
	}

	if c.IsProduction() {
		// 1. Chặn tuyệt đối DEZUXK_TEST_MODE trong production (Requirement 11)
		testModeEnv := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_TEST_MODE")))
		if c.TestMode || testModeEnv == "true" || testModeEnv == "1" {
			return errors.New("DEZUXK_TEST_MODE=true không được phép sử dụng trong môi trường production (bảo đảm không bypass upstream, authentication và session)")
		}

		// 2. Chặn thiếu hoặc placeholder API Key & Master Key (Requirement 10, 12)
		if strings.TrimSpace(c.Server.APIKey) == "" || c.Server.APIKey == "CHANGE_ME" {
			return errors.New("server.api_key bắt buộc phải được cấu hình trong môi trường production")
		}
		if strings.TrimSpace(c.Security.MasterKey) == "" || c.Security.MasterKey == "CHANGE_ME" {
			return errors.New("security.master_key bắt buộc phải được cấu hình trong môi trường production")
		}

		// 3. Chặn Admin credentials mặc định hoặc placeholder (Requirement 12)
		if c.Admin.IsEnabled() {
			adminPass := strings.TrimSpace(c.Admin.Password)
			if adminPass == "" || adminPass == "dezuxk_admin_secret_pass" || adminPass == "admin" || adminPass == "password" || adminPass == "CHANGE_ME" {
				return errors.New("admin.password không được để trống hoặc dùng mật khẩu mặc định trong môi trường production")
			}
			adminToken := strings.TrimSpace(c.Admin.SessionToken)
			if adminToken == "" || adminToken == "dezuxk_admin_token" || adminToken == "dezuxk_secure_admin_session_token_2026" || adminToken == "CHANGE_ME" {
				return errors.New("admin.session_token không được để trống hoặc dùng token mặc định trong môi trường production")
			}
		}

		// 4. Wildcard CORS bị cấm trong production (Requirement 12)
		for _, origin := range c.Server.AllowedOrigins {
			if strings.TrimSpace(origin) == "*" {
				return errors.New("server.allowed_origins không được chứa wildcard '*' khi chạy trong môi trường production")
			}
		}

		// 5. Cụm multi-node cluster trong production bắt buộc dùng Postgres, Redis và S3 (Requirement 5, 7, 8, 12)
		if c.Cluster.Enabled {
			if storageDriver != "postgres" {
				return errors.New("chế độ cụm phân tán cluster.enabled yêu cầu storage.driver='postgres' (PostgreSQL là source-of-truth cho agent leases)")
			}
			if !c.Distributed.Enabled || len(c.Distributed.Redis.GetAddrs()) == 0 || c.Distributed.Redis.GetAddrs()[0] == "" {
				return errors.New("chế độ cụm phân tán cluster.enabled yêu cầu distributed.enabled=true và cấu hình distributed.redis.addr hợp lệ")
			}
			if mediaDriver != "s3" {
				return errors.New("cụm cluster multi-node production không được dùng media driver 'local' (yêu cầu shared media storage: media.driver='s3')")
			}
			if strings.TrimSpace(c.Media.S3.Bucket) == "" {
				return errors.New("media.s3.bucket là bắt buộc khi chọn media driver 's3'")
			}
		}
	}

	return nil
}
