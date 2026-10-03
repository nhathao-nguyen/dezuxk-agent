package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"dezuxk-gateway/internal/config"
)

// getRepoRoot tìm thư mục gốc chứa file go.mod
func getRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root with go.mod starting from %s", dir)
		}
		dir = parent
	}
}

// 1. Kiểm tra cấu hình wiring production và an toàn gitignore
func TestProductionDeploymentWiring_ConfigPathAndSafety(t *testing.T) {
	root := getRepoRoot(t)

	// .gitignore phải chứa configs/config.production.yaml
	gitIgnorePath := filepath.Join(root, ".gitignore")
	gitIgnoreData, err := os.ReadFile(gitIgnorePath)
	if err != nil {
		t.Fatalf("không đọc được .gitignore: %v", err)
	}
	if !strings.Contains(string(gitIgnoreData), "configs/config.production.yaml") {
		t.Errorf(".gitignore phải chứa 'configs/config.production.yaml' để tránh commit secret thật")
	}

	// configs/config.production.example.yaml phải tồn tại
	examplePath := filepath.Join(root, "configs", "config.production.example.yaml")
	if _, err := os.Stat(examplePath); err != nil {
		t.Fatalf("configs/config.production.example.yaml không tồn tại: %v", err)
	}

	// Đọc docker-compose.production.yml
	composePath := filepath.Join(root, "docker-compose.production.yml")
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("không đọc được docker-compose.production.yml: %v", err)
	}
	composeContent := string(composeData)

	// Production compose KHÔNG ĐƯỢC phụ thuộc vào config.example.yaml
	if strings.Contains(composeContent, "config.example.yaml") {
		t.Errorf("docker-compose.production.yml không được phụ thuộc hoặc tham chiếu đến config.example.yaml")
	}

	// Parse YAML compose
	var composeMap struct {
		Services map[string]struct {
			Command interface{} `yaml:"command"`
			Volumes []string    `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeData, &composeMap); err != nil {
		t.Fatalf("lỗi parse docker-compose.production.yml: %v", err)
	}

	gateways := []string{"gateway-a", "gateway-b", "gateway-c"}
	for _, gw := range gateways {
		svc, ok := composeMap.Services[gw]
		if !ok {
			t.Fatalf("service %s thiếu trong docker-compose.production.yml", gw)
		}

		// Kiểm tra command chỉ định rõ ràng -config /app/configs/config.production.yaml
		var cmdJoined string
		switch v := svc.Command.(type) {
		case string:
			cmdJoined = v
		case []interface{}:
			var parts []string
			for _, p := range v {
				if s, ok := p.(string); ok {
					parts = append(parts, s)
				}
			}
			cmdJoined = strings.Join(parts, " ")
		}
		if !strings.Contains(cmdJoined, "-config") || !strings.Contains(cmdJoined, "/app/configs/config.production.yaml") {
			t.Errorf("service %s phải chạy với '-config /app/configs/config.production.yaml', nhận: %v", gw, svc.Command)
		}

		// Kiểm tra volume mount cấu hình production
		hasConfigMount := false
		for _, v := range svc.Volumes {
			if strings.Contains(v, "config.production.yaml") && strings.Contains(v, ":ro") {
				hasConfigMount = true
				break
			}
		}
		if !hasConfigMount {
			t.Errorf("service %s phải mount file config.production.yaml read-only, volumes: %v", gw, svc.Volumes)
		}
	}
}

// 2. Kiểm tra thư mục Chrome Profile riêng biệt cho từng Gateway Node (không chia sẻ user-data-dir)
func TestProductionDeploymentWiring_SeparateProfileDirectories(t *testing.T) {
	root := getRepoRoot(t)
	composePath := filepath.Join(root, "docker-compose.production.yml")
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("không đọc được docker-compose.production.yml: %v", err)
	}

	var composeMap struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeData, &composeMap); err != nil {
		t.Fatalf("lỗi parse docker-compose.production.yml: %v", err)
	}

	nodeProfiles := make(map[string]string)
	gateways := []string{"gateway-a", "gateway-b", "gateway-c"}

	for _, gw := range gateways {
		svc := composeMap.Services[gw]
		var profileMount string
		for _, v := range svc.Volumes {
			if strings.Contains(v, ":/app/profiles") {
				profileMount = v
				break
			}
		}
		if profileMount == "" {
			t.Fatalf("service %s thiếu volume mount cho /app/profiles", gw)
		}
		if profileMount == "./profiles:/app/profiles" {
			t.Errorf("service %s dùng chung './profiles:/app/profiles', phải dùng thư mục riêng biệt như './profiles/%s:/app/profiles'", gw, gw)
		}
		nodeProfiles[gw] = profileMount
	}

	// Đảm bảo cả 3 node có volume riêng biệt, không trùng lặp
	if nodeProfiles["gateway-a"] == nodeProfiles["gateway-b"] ||
		nodeProfiles["gateway-b"] == nodeProfiles["gateway-c"] ||
		nodeProfiles["gateway-a"] == nodeProfiles["gateway-c"] {
		t.Errorf("phát hiện xung đột mount profiles giữa các node: %v", nodeProfiles)
	}
}

// 3. Kiểm tra minio-init: tự động tạo bucket, chỉ định idempotent và private policy cho self-hosted
func TestProductionDeploymentWiring_MinioInit(t *testing.T) {
	root := getRepoRoot(t)
	composePath := filepath.Join(root, "docker-compose.production.yml")
	composeData, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("không đọc được docker-compose.production.yml: %v", err)
	}

	var composeMap struct {
		Services map[string]struct {
			Profiles   []string               `yaml:"profiles"`
			Image      string                 `yaml:"image"`
			Entrypoint string                 `yaml:"entrypoint"`
			DependsOn  map[string]interface{} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeData, &composeMap); err != nil {
		t.Fatalf("lỗi parse docker-compose.production.yml: %v", err)
	}

	initSvc, ok := composeMap.Services["minio-init"]
	if !ok {
		t.Fatalf("service minio-init phải có mặt trong docker-compose.production.yml")
	}

	// Chỉ chạy trong profile self-hosted
	hasSelfHosted := false
	for _, p := range initSvc.Profiles {
		if p == "self-hosted" {
			hasSelfHosted = true
			break
		}
	}
	if !hasSelfHosted {
		t.Errorf("minio-init phải thuộc profile 'self-hosted'")
	}

	// Image dùng minio/mc
	if !strings.Contains(initSvc.Image, "minio/mc") {
		t.Errorf("minio-init phải sử dụng image minio/mc, nhận: %s", initSvc.Image)
	}

	// Phải có mc mb --ignore-existing (idempotent) và mc anonymous set none (private by default)
	ep := initSvc.Entrypoint
	if !strings.Contains(ep, "mb --ignore-existing") {
		t.Errorf("minio-init entrypoint phải chứa 'mb --ignore-existing' để tạo bucket an toàn, không lỗi khi đã tồn tại")
	}
	if !strings.Contains(ep, "anonymous set none") {
		t.Errorf("minio-init entrypoint phải chứa 'anonymous set none' để bảo mật bucket private mặc định")
	}
}

// 4. Kiểm tra Nginx: định tuyến /v1/profiles độc quyền về Gateway Node A và load balancing round-robin các endpoint khác
func TestProductionDeploymentWiring_NginxProfileAffinity(t *testing.T) {
	root := getRepoRoot(t)
	nginxPath := filepath.Join(root, "deployments", "nginx", "nginx.production.conf")
	nginxData, err := os.ReadFile(nginxPath)
	if err != nil {
		t.Fatalf("không đọc được nginx.production.conf: %v", err)
	}
	nginxContent := string(nginxData)

	// Upstream cluster phải có cả 3 node A, B, C
	if !strings.Contains(nginxContent, "server gateway-a:8080") ||
		!strings.Contains(nginxContent, "server gateway-b:8080") ||
		!strings.Contains(nginxContent, "server gateway-c:8080") {
		t.Errorf("upstream dezuxk_cluster phải chứa gateway-a, gateway-b, gateway-c")
	}

	// Phải có location ^~ /v1/profiles chuyển tiếp trực tiếp đến gateway-a:8080
	if !strings.Contains(nginxContent, "location ^~ /v1/profiles") {
		t.Errorf("nginx.production.conf phải có 'location ^~ /v1/profiles'")
	}
	if !strings.Contains(nginxContent, "proxy_pass http://gateway-a:8080;") {
		t.Errorf("nginx.production.conf phải chuyển hướng /v1/profiles về http://gateway-a:8080;")
	}

	// Các traffic khác như /v1/chat/completions, /v1/agent/runs, và / vẫn qua cluster
	if !strings.Contains(nginxContent, "proxy_pass http://dezuxk_cluster;") {
		t.Errorf("nginx.production.conf phải định tuyến API chung và chat về http://dezuxk_cluster;")
	}
}

// 5. Chặn tuyệt đối TEST_MODE=true trong môi trường production
func TestProductionDeploymentWiring_TestModeRejected(t *testing.T) {
	c := &config.Config{
		Environment: "production",
		TestMode:    true,
		Server: config.ServerConfig{
			Host:   "0.0.0.0",
			Port:   8080,
			APIKey: "valid-key-for-test",
		},
		Profiles: config.ProfilesConfig{
			BaseDir: "./profiles",
		},
		Security: config.SecurityConfig{
			MasterKey: "valid-master-key-0123456789abcdef",
		},
		Storage: config.StorageConfig{
			Driver: "postgres",
			Postgres: config.PostgresConfig{
				Host:   "postgres",
				Port:   5432,
				DBName: "dezuxk",
			},
		},
		Distributed: config.DistributedConfig{
			Enabled: true,
			Driver:  "redis",
			Redis: config.RedisConfig{
				Addr: "redis:6379",
			},
		},
		Cluster: config.ClusterConfig{
			Enabled: true,
		},
		Media: config.MediaConfig{
			Driver: "s3",
			S3: config.S3MediaConfig{
				Bucket: "dezuxk-media",
			},
		},
	}

	if err := c.Validate(); err == nil {
		t.Fatal("expected error when TestMode=true in production, but got nil")
	} else if !strings.Contains(err.Error(), "DEZUXK_TEST_MODE") {
		t.Fatalf("expected error mentioning DEZUXK_TEST_MODE, got: %v", err)
	}

	// Thử set biến môi trường DEZUXK_TEST_MODE=true
	c.TestMode = false
	_ = os.Setenv("DEZUXK_TEST_MODE", "true")
	defer os.Unsetenv("DEZUXK_TEST_MODE")

	if err := c.Validate(); err == nil {
		t.Fatal("expected error when DEZUXK_TEST_MODE env is true in production, but got nil")
	}
}
