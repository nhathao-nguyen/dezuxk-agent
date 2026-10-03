package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3client "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"dezuxk-gateway/internal/config"
)

// ANSI colors
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"
)

type CheckResult struct {
	Name    string
	Passed  bool
	Message string
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "\ufeff")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			// Do not overwrite existing environment variables
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
	return scanner.Err()
}

func main() {
	envFile := flag.String("env", ".env.production", "Đường dẫn file .env.production")
	configFile := flag.String("config", "configs/config.production.example.yaml", "Đường dẫn file cấu hình YAML")
	skipInfra := flag.Bool("skip-infra", false, "Chỉ kiểm tra biến môi trường và config (bỏ qua ping mạng)")
	flag.Parse()

	// Load env file if present
	if *envFile != "" {
		_ = loadEnvFile(*envFile)
	}

	var results []CheckResult
	allPassed := true

	// Helper to record check
	record := func(name string, passed bool, msg string) {
		results = append(results, CheckResult{
			Name:    name,
			Passed:  passed,
			Message: msg,
		})
		if !passed {
			allPassed = false
		}
	}

	// 1. Environment
	envVal := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_ENV")))
	if envVal == "" {
		envVal = strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	}
	if envVal == "production" {
		record("Environment", true, "")
	} else {
		record("Environment", false, fmt.Sprintf("DEZUXK_ENV phải là 'production', nhận được: %q", envVal))
	}

	// 2. Test mode disabled
	testModeVal := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_TEST_MODE")))
	if testModeVal == "" || testModeVal == "false" || testModeVal == "0" {
		record("Test mode disabled", true, "")
	} else {
		record("Test mode disabled", false, "DEZUXK_TEST_MODE=true bị cấm trong production")
	}

	// 3. API Security
	apiKey := strings.TrimSpace(os.Getenv("DEZUXK_API_KEY"))
	if apiKey != "" && apiKey != "CHANGE_ME" {
		record("API Security", true, "")
	} else {
		record("API Security", false, "DEZUXK_API_KEY bị thiếu hoặc chưa thay đổi placeholder 'CHANGE_ME'")
	}

	// 4. Vault (Master Key)
	masterKey := strings.TrimSpace(os.Getenv("DEZUXK_MASTER_KEY"))
	if masterKey != "" && masterKey != "CHANGE_ME" {
		record("Vault", true, "")
	} else {
		record("Vault", false, "DEZUXK_MASTER_KEY bị thiếu hoặc chưa thay đổi placeholder 'CHANGE_ME'")
	}

	// 5. Admin Security
	adminPass := strings.TrimSpace(os.Getenv("DEZUXK_ADMIN_PASSWORD"))
	adminToken := strings.TrimSpace(os.Getenv("DEZUXK_ADMIN_SESSION_TOKEN"))
	if adminPass != "" && adminPass != "CHANGE_ME" && adminPass != "admin" && adminPass != "dezuxk_admin_secret_pass" &&
		adminToken != "" && adminToken != "CHANGE_ME" && adminToken != "dezuxk_admin_token" {
		record("Admin Security", true, "")
	} else {
		record("Admin Security", false, "DEZUXK_ADMIN_PASSWORD hoặc DEZUXK_ADMIN_SESSION_TOKEN không an toàn hoặc dùng mặc định")
	}

	// 6. Cluster
	clusterEnabled := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_CLUSTER_ENABLED")))
	distEnabled := strings.ToLower(strings.TrimSpace(os.Getenv("DEZUXK_DISTRIBUTED_ENABLED")))
	if (clusterEnabled == "true" || clusterEnabled == "1") && (distEnabled == "true" || distEnabled == "1") {
		record("Cluster", true, "")
	} else {
		record("Cluster", false, "DEZUXK_CLUSTER_ENABLED và DEZUXK_DISTRIBUTED_ENABLED phải được bật (true)")
	}

	// Load & Validate Config
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		record("Config Validation", false, err.Error())
	} else {
		record("Config Validation", true, "")
	}

	// Network / Infrastructure Connectivity checks
	if !*skipInfra && cfg != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// PostgreSQL check
		storageDriver := strings.ToLower(strings.TrimSpace(cfg.Storage.Driver))
		if storageDriver != "postgres" {
			record("PostgreSQL", false, "storage.driver phải là 'postgres'")
		} else {
			dsn := cfg.Storage.Postgres.BuildDSN()
			poolCfg, parseErr := pgxpool.ParseConfig(dsn)
			if parseErr != nil {
				record("PostgreSQL", false, fmt.Sprintf("lỗi phân tích DSN: %v", parseErr))
			} else {
				pool, poolErr := pgxpool.NewWithConfig(ctx, poolCfg)
				if poolErr != nil {
					record("PostgreSQL", false, fmt.Sprintf("kết nối thất bại: %v", poolErr))
				} else {
					defer pool.Close()
					if pingErr := pool.Ping(ctx); pingErr != nil {
						record("PostgreSQL", false, fmt.Sprintf("ping thất bại: %v", pingErr))
					} else {
						record("PostgreSQL", true, "")
					}
				}
			}
		}

		// Redis check
		redisAddrs := cfg.Distributed.Redis.GetAddrs()
		if len(redisAddrs) == 0 {
			record("Redis", false, "thiếu địa chỉ Redis")
		} else {
			rdb := redis.NewClient(&redis.Options{
				Addr:        redisAddrs[0],
				Password:    cfg.Distributed.Redis.Password,
				DB:          cfg.Distributed.Redis.DB,
				DialTimeout: 2 * time.Second,
			})
			defer rdb.Close()
			if pingErr := rdb.Ping(ctx).Err(); pingErr != nil {
				record("Redis", false, fmt.Sprintf("ping thất bại (%s): %v", redisAddrs[0], pingErr))
			} else {
				record("Redis", true, "")
			}
		}

		// S3 check
		mediaDriver := cfg.Media.GetDriver()
		if mediaDriver != "s3" {
			record("S3", false, "media.driver phải là 's3'")
		} else if strings.TrimSpace(cfg.Media.S3.Bucket) == "" {
			record("S3", false, "media.s3.bucket không được để trống")
		} else {
			s3Region := cfg.Media.S3.Region
			if s3Region == "" {
				s3Region = "us-east-1"
			}
			s3Opts := s3client.Options{
				Region:       s3Region,
				UsePathStyle: cfg.Media.S3.UsePathStyle,
			}
			if cfg.Media.S3.Endpoint != "" {
				s3Opts.BaseEndpoint = aws.String(cfg.Media.S3.Endpoint)
			}
			if cfg.Media.S3.AccessKey != "" && cfg.Media.S3.SecretKey != "" {
				s3Opts.Credentials = credentials.NewStaticCredentialsProvider(cfg.Media.S3.AccessKey, cfg.Media.S3.SecretKey, "")
			}
			client := s3client.New(s3Opts)
			_, headErr := client.HeadBucket(ctx, &s3client.HeadBucketInput{
				Bucket: aws.String(cfg.Media.S3.Bucket),
			})
			if headErr != nil {
				var notFound *s3types.NoSuchBucket
				if errors.As(headErr, &notFound) {
					record("S3", false, fmt.Sprintf("bucket %s không tồn tại", cfg.Media.S3.Bucket))
				} else {
					// Also check 404
					record("S3", false, fmt.Sprintf("truy cập bucket %s thất bại: %v", cfg.Media.S3.Bucket, headErr))
				}
			} else {
				record("S3", true, "")
			}
		}
	} else if *skipInfra {
		record("PostgreSQL", true, "(skipped network ping)")
		record("Redis", true, "(skipped network ping)")
		record("S3", true, "(skipped network ping)")
	}

	// Print Results Table
	fmt.Println()
	fmt.Printf("%s=== DEZUXK PRODUCTION PRE-FLIGHT VERIFICATION ===%s\n\n", colorBold, colorReset)

	formatName := func(name string) string {
		const width = 24
		if len(name) < width {
			return name + strings.Repeat(" ", width-len(name))
		}
		return name + " "
	}

	for _, res := range results {
		statusStr := colorGreen + "PASS" + colorReset
		if !res.Passed {
			statusStr = colorRed + "FAIL" + colorReset
		}
		if res.Message != "" && !res.Passed {
			fmt.Printf("%s%s  (%s%s%s)\n", formatName(res.Name), statusStr, colorYellow, res.Message, colorReset)
		} else if res.Message != "" {
			fmt.Printf("%s%s  (%s)\n", formatName(res.Name), statusStr, res.Message)
		} else {
			fmt.Printf("%s%s\n", formatName(res.Name), statusStr)
		}
	}

	fmt.Println()
	if !allPassed {
		fmt.Printf("%sPre-flight verification FAILED! Vui lòng khắc phục các mục FAIL trước khi khởi động production.%s\n\n", colorRed, colorReset)
		os.Exit(1)
	}

	fmt.Printf("%sPre-flight verification PASSED! Hệ thống đã sẵn sàng khởi động trong môi trường Production.%s\n\n", colorGreen, colorReset)
}
