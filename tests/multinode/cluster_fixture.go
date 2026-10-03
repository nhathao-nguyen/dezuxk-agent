package multinode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	redisadapter "dezuxk-gateway/internal/adapters/outbound/distributed/redis"
	"dezuxk-gateway/internal/adapters/outbound/session"
	pgstorage "dezuxk-gateway/internal/adapters/outbound/storage/postgres"
	s3storage "dezuxk-gateway/internal/adapters/outbound/storage/s3"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
	"dezuxk-gateway/internal/core/services/agent"
	"dezuxk-gateway/internal/core/services/leader"
)

// TestCluster biểu diễn cụm Gateway đa node cho kiểm thử tích hợp
type TestCluster struct {
	RedisClient    *redis.Client
	MiniRedis      *miniredis.Miniredis
	PgPool         *pgxpool.Pool
	EventBus       ports.EventBus
	Locker         ports.DistributedLocker
	RateLimiter    ports.SharedRateLimiter
	RunsRepo       ports.AgentRunRepository
	Checkpoints    ports.CheckpointRepository
	MediaStorage   ports.MediaStorage
	NodeA          *agent.JobService
	NodeB          *agent.JobService
	NodeC          *agent.JobService
	CoordA         *leader.Coordinator
	CoordB         *leader.Coordinator
	CoordC         *leader.Coordinator
	IsRealPostgres bool
	IsRealS3       bool
}

// RequireRealPostgres xác nhận cluster đang chạy trên PostgreSQL thật, fail ngay nếu chạy in-memory fake
func RequireRealPostgres(t *testing.T, cluster *TestCluster) {
	t.Helper()
	if !cluster.IsRealPostgres {
		t.Fatalf("integration test required real PostgreSQL, but running with fake/in-memory repo")
	}
}

// Close dọn dẹp toàn bộ cụm sau khi test hoàn tất
func (c *TestCluster) Close() {
	if c.NodeA != nil {
		_ = c.NodeA.Shutdown(context.Background())
	}
	if c.NodeB != nil {
		_ = c.NodeB.Shutdown(context.Background())
	}
	if c.NodeC != nil {
		_ = c.NodeC.Shutdown(context.Background())
	}
	if c.CoordA != nil {
		c.CoordA.Stop()
	}
	if c.CoordB != nil {
		c.CoordB.Stop()
	}
	if c.CoordC != nil {
		c.CoordC.Stop()
	}
	if c.RedisClient != nil {
		_ = c.RedisClient.Close()
	}
	if c.MiniRedis != nil {
		c.MiniRedis.Close()
	}
	if c.PgPool != nil {
		c.PgPool.Close()
	}
}

// SetupTestCluster khởi tạo cụm gồm 3 Gateway Nodes (A, B, C) dùng chung Redis & Storage
func SetupTestCluster(t *testing.T) *TestCluster {
	t.Helper()

	// 1. Khởi tạo Redis (dùng TEST_REDIS_ADDR nếu có, không thì miniredis)
	var rdb *redis.Client
	var mr *miniredis.Miniredis
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: redisAddr})
		if err := rdb.Ping(context.Background()).Err(); err != nil {
			t.Fatalf("Không thể kết nối đến TEST_REDIS_ADDR %s: %v", redisAddr, err)
		}
	} else {
		var err error
		mr, err = miniredis.Run()
		if err != nil {
			t.Fatalf("Không thể khởi động miniredis: %v", err)
		}
		rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	}

	eventBus := redisadapter.NewRedisEventBus(rdb)
	locker := redisadapter.NewRedisDistributedLocker(rdb)
	rateLimiter := redisadapter.NewRedisSharedRateLimiter(rdb, 120, time.Minute)

	// 2. Khởi tạo Storage (Real Postgres nếu có TEST_POSTGRES_DSN, không được fallback sang fake)
	var runsRepo ports.AgentRunRepository
	var checkRepo ports.CheckpointRepository
	var mediaRepo *pgstorage.PostgresMediaMetadataRepository
	var pgPool *pgxpool.Pool
	isRealPG := false

	pgDSN := os.Getenv("TEST_POSTGRES_DSN")
	if pgDSN != "" {
		pgCfg := config.PostgresConfig{
			DSN:      pgDSN,
			MaxConns: 10,
		}
		pool, err := pgstorage.NewPool(context.Background(), pgCfg)
		if err != nil {
			t.Fatalf("TEST_POSTGRES_DSN được cấu hình nhưng kết nối pgstorage.NewPool thất bại: %v", err)
		}
		if err := pgstorage.RunMigrations(context.Background(), pool); err != nil {
			t.Fatalf("Chạy PostgreSQL migrations thất bại: %v", err)
		}
		arRepo, err1 := pgstorage.NewPostgresAgentRunRepository(pool)
		if err1 != nil {
			t.Fatalf("Khởi tạo PostgresAgentRunRepository thất bại: %v", err1)
		}
		cpRepo, err2 := pgstorage.NewPostgresCheckpointRepository(pool)
		if err2 != nil {
			t.Fatalf("Khởi tạo PostgresCheckpointRepository thất bại: %v", err2)
		}
		mRepo, err3 := pgstorage.NewPostgresMediaMetadataRepository(pool)
		if err3 != nil {
			t.Fatalf("Khởi tạo PostgresMediaMetadataRepository thất bại: %v", err3)
		}
		runsRepo = arRepo
		checkRepo = cpRepo
		mediaRepo = mRepo
		pgPool = pool
		isRealPG = true
	} else {
		clusterDB := NewClusterFencedDB()
		runsRepo = clusterDB
		checkRepo = clusterDB
	}

	// 3. Khởi tạo Media Storage (Real S3/MinIO nếu có TEST_S3_ENDPOINT, không được fallback)
	var sharedMedia ports.MediaStorage
	isRealS3 := false
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if s3Endpoint != "" {
		if !isRealPG {
			t.Fatalf("TEST_S3_ENDPOINT được cấu hình nhưng TEST_POSTGRES_DSN chưa sẵn sàng để lưu metadata")
		}
		s3Bucket := os.Getenv("TEST_S3_BUCKET")
		if s3Bucket == "" {
			s3Bucket = "dezuxk-test"
		}
		s3AccessKey := os.Getenv("TEST_S3_ACCESS_KEY")
		if s3AccessKey == "" {
			s3AccessKey = "minioadmin"
		}
		s3SecretKey := os.Getenv("TEST_S3_SECRET_KEY")
		if s3SecretKey == "" {
			s3SecretKey = "minioadmin"
		}
		s3Cfg := config.S3MediaConfig{
			Bucket:       s3Bucket,
			Endpoint:     s3Endpoint,
			Region:       "us-east-1",
			AccessKey:    s3AccessKey,
			SecretKey:    s3SecretKey,
			UsePathStyle: true,
		}
		s3Adapter, err := s3storage.NewS3StorageAdapter(s3Cfg, "http://localhost:8080", mediaRepo)
		if err != nil {
			t.Fatalf("Khởi tạo S3StorageAdapter với TEST_S3_ENDPOINT %s thất bại: %v", s3Endpoint, err)
		}
		if err := s3Adapter.EnsureBucketExists(context.Background()); err != nil {
			t.Fatalf("EnsureBucketExists cho S3StorageAdapter (%s) thất bại: %v", s3Bucket, err)
		}
		sharedMedia = s3Adapter
		isRealS3 = true
	} else {
		sharedMedia = NewClusterSharedMediaStorage()
	}

	// 4. Khởi tạo 3 Gateway Nodes dùng chung Repository và Redis EventBus
	dummyRunner := &dummyAgentRunner{}

	nodeA := agent.NewJobService(runsRepo, dummyRunner)
	nodeA.SetWorkerID("worker-node-a")
	nodeA.SetEventBus(eventBus)
	nodeA.SetCheckpointRepository(checkRepo)
	_ = nodeA.Start(context.Background())

	nodeB := agent.NewJobService(runsRepo, dummyRunner)
	nodeB.SetWorkerID("worker-node-b")
	nodeB.SetEventBus(eventBus)
	nodeB.SetCheckpointRepository(checkRepo)
	_ = nodeB.Start(context.Background())

	nodeC := agent.NewJobService(runsRepo, dummyRunner)
	nodeC.SetWorkerID("worker-node-c")
	nodeC.SetEventBus(eventBus)
	nodeC.SetCheckpointRepository(checkRepo)
	_ = nodeC.Start(context.Background())

	coordA := leader.NewCoordinator(locker, "node-a", 1*time.Second)
	coordB := leader.NewCoordinator(locker, "node-b", 1*time.Second)
	coordC := leader.NewCoordinator(locker, "node-c", 1*time.Second)

	cluster := &TestCluster{
		RedisClient:    rdb,
		MiniRedis:      mr,
		PgPool:         pgPool,
		EventBus:       eventBus,
		Locker:         locker,
		RateLimiter:    rateLimiter,
		RunsRepo:       runsRepo,
		Checkpoints:    checkRepo,
		MediaStorage:   sharedMedia,
		NodeA:          nodeA,
		NodeB:          nodeB,
		NodeC:          nodeC,
		CoordA:         coordA,
		CoordB:         coordB,
		CoordC:         coordC,
		IsRealPostgres: isRealPG,
		IsRealS3:       isRealS3,
	}

	if os.Getenv("TEST_POSTGRES_DSN") != "" {
		RequireRealPostgres(t, cluster)
	}

	t.Cleanup(func() {
		cluster.Close()
	})

	return cluster
}

type dummyAgentRunner struct{}

func (d *dummyAgentRunner) Run(ctx context.Context, goal string, opts domain.AgentRunOptions) (*domain.AgentState, error) {
	return &domain.AgentState{
		TaskID:      "test-run",
		CurrentStep: 1,
		MaxSteps:    10,
		IsCompleted: true,
		StopReason:  domain.StopReasonCompleted,
		FinalAnswer: "Done: " + goal,
	}, nil
}

// ClusterFencedDB mô phỏng chính xác ngữ nghĩa nguyên tử và fencing token của PostgreSQL
type ClusterFencedDB struct {
	mu           sync.Mutex
	runs         map[string]*domain.AgentRun
	events       map[string][]domain.AgentRunEvent
	idempotency  map[string]string // key: tenantID + ":" + idempotencyKey -> runID
	eventCounter int64
	checkpoints  map[string][]*domain.AgentCheckpoint   // taskID -> checkpoints
	toolLedger   map[string]*domain.ToolExecutionRecord // key: tenant:run:tool
}

func NewClusterFencedDB() *ClusterFencedDB {
	return &ClusterFencedDB{
		runs:        make(map[string]*domain.AgentRun),
		events:      make(map[string][]domain.AgentRunEvent),
		idempotency: make(map[string]string),
		checkpoints: make(map[string][]*domain.AgentCheckpoint),
		toolLedger:  make(map[string]*domain.ToolExecutionRecord),
	}
}

// Create chèn AgentRun mới, thực thi ràng buộc UNIQUE (tenant_id, idempotency_key)
func (db *ClusterFencedDB) Create(ctx context.Context, run *domain.AgentRun) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if run == nil || run.ID == "" {
		return errors.New("run không hợp lệ")
	}

	tenantID := run.TenantID
	if tenantID == "" {
		tenantID = "default"
	}

	if run.IdempotencyKey != "" {
		idemKey := tenantID + ":" + run.IdempotencyKey
		if existingID, exists := db.idempotency[idemKey]; exists {
			return fmt.Errorf("duplicate key value violates unique constraint \"idx_agent_runs_tenant_idempotency\" (run: %s)", existingID)
		}
		db.idempotency[idemKey] = run.ID
	}

	cp := *run
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	cp.UpdatedAt = cp.CreatedAt
	db.runs[run.ID] = &cp
	return nil
}

func (db *ClusterFencedDB) Update(ctx context.Context, run *domain.AgentRun) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[run.ID]
	if !exists {
		return session.ErrRunNotFound
	}
	cp := *run
	cp.UpdatedAt = time.Now()
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = cur.CreatedAt
	}
	db.runs[run.ID] = &cp
	return nil
}

func (db *ClusterFencedDB) UpdateWithTransition(ctx context.Context, run *domain.AgentRun, allowed ...domain.AgentRunStatus) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[run.ID]
	if !exists {
		return false, session.ErrRunNotFound
	}
	allowedMap := make(map[domain.AgentRunStatus]bool)
	for _, st := range allowed {
		allowedMap[st] = true
	}
	if !allowedMap[cur.Status] {
		return false, nil
	}
	cp := *run
	cp.UpdatedAt = time.Now()
	db.runs[run.ID] = &cp
	return true, nil
}

func (db *ClusterFencedDB) Get(ctx context.Context, runID string) (*domain.AgentRun, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	r, exists := db.runs[runID]
	if !exists {
		return nil, session.ErrRunNotFound
	}
	cp := *r
	return &cp, nil
}

func (db *ClusterFencedDB) GetForTenant(ctx context.Context, tenantID, runID string) (*domain.AgentRun, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	r, exists := db.runs[runID]
	if !exists || r.TenantID != tenantID {
		return nil, session.ErrRunNotFound
	}
	cp := *r
	return &cp, nil
}

func (db *ClusterFencedDB) List(ctx context.Context, tenantID string, limit, offset int) ([]*domain.AgentRun, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var result []*domain.AgentRun
	for _, r := range db.runs {
		if tenantID == "" || r.TenantID == tenantID {
			cp := *r
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (db *ClusterFencedDB) ListPendingRuns(ctx context.Context) ([]*domain.AgentRun, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	now := time.Now()
	var result []*domain.AgentRun
	for _, r := range db.runs {
		if r.Status == domain.RunStatusQueued ||
			((r.Status == domain.RunStatusRunning || r.Status == domain.RunStatusRecovering) && (r.LeaseUntil == nil || r.LeaseUntil.Before(now))) {
			cp := *r
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (db *ClusterFencedDB) AppendEvent(ctx context.Context, event *domain.AgentRunEvent) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	id := atomic.AddInt64(&db.eventCounter, 1)
	ev := *event
	ev.ID = id
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	db.events[event.RunID] = append(db.events[event.RunID], ev)
	event.ID = id
	return nil
}

func (db *ClusterFencedDB) GetEvents(ctx context.Context, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var result []domain.AgentRunEvent
	for _, ev := range db.events[runID] {
		if ev.ID > afterID {
			result = append(result, ev)
		}
	}
	return result, nil
}

func (db *ClusterFencedDB) GetEventsForTenant(ctx context.Context, tenantID, runID string, afterID int64) ([]domain.AgentRunEvent, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	run, exists := db.runs[runID]
	if !exists || run.TenantID != tenantID {
		return nil, session.ErrRunNotFound
	}
	return db.GetEvents(ctx, runID, afterID)
}

func (db *ClusterFencedDB) Cancel(ctx context.Context, runID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists {
		return session.ErrRunNotFound
	}
	if cur.Status == domain.RunStatusCompleted || cur.Status == domain.RunStatusFailed || cur.Status == domain.RunStatusCancelled {
		return fmt.Errorf("%w: cannot cancel %s", session.ErrInvalidStatusTransition, cur.Status)
	}
	now := time.Now()
	cur.Status = domain.RunStatusCancelled
	cur.StopReason = "cancelled"
	cur.UpdatedAt = now
	cur.FinishedAt = &now
	return nil
}

func (db *ClusterFencedDB) CancelForTenant(ctx context.Context, tenantID, runID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists || cur.TenantID != tenantID {
		return session.ErrRunNotFound
	}
	return db.Cancel(ctx, runID)
}

// ClaimRun mô phỏng chính xác atomic UPDATE ... WHERE RETURNING claim_generation của PostgreSQL
func (db *ClusterFencedDB) ClaimRun(ctx context.Context, runID, workerID string, leaseDuration time.Duration) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists {
		return false, nil
	}

	now := time.Now()
	isQueued := cur.Status == domain.RunStatusQueued
	isExpired := (cur.Status == domain.RunStatusRunning || cur.Status == domain.RunStatusRecovering || cur.Status == domain.RunStatusWaitingForTool) &&
		(cur.LeaseUntil == nil || cur.LeaseUntil.Before(now) || cur.LeaseUntil.Equal(now))

	if !isQueued && !isExpired {
		return false, nil
	}

	cur.Status = domain.RunStatusRunning
	cur.WorkerID = workerID
	cur.ClaimGeneration++
	leaseEnd := now.Add(leaseDuration)
	cur.LeaseUntil = &leaseEnd
	cur.HeartbeatAt = &now
	cur.UpdatedAt = now
	return true, nil
}

func (db *ClusterFencedDB) RenewLease(ctx context.Context, runID, workerID string, claimGeneration int64, leaseDuration time.Duration) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists {
		return false, nil
	}

	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration {
		return false, nil
	}
	if cur.Status != domain.RunStatusRunning && cur.Status != domain.RunStatusRecovering {
		return false, nil
	}

	now := time.Now()
	leaseEnd := now.Add(leaseDuration)
	cur.LeaseUntil = &leaseEnd
	cur.HeartbeatAt = &now
	cur.UpdatedAt = now
	return true, nil
}

// UpdateOwned áp dụng nguyên tử Fencing Token: từ chối zombie worker
func (db *ClusterFencedDB) UpdateOwned(ctx context.Context, run *domain.AgentRun, workerID string, claimGeneration int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[run.ID]
	if !exists {
		return false, nil
	}

	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration || cur.Status == domain.RunStatusCancelled {
		return false, nil
	}

	cur.Status = run.Status
	cur.CurrentStep = run.CurrentStep
	cur.TotalToolCalls = run.TotalToolCalls
	cur.StopReason = run.StopReason
	cur.FinalAnswer = run.FinalAnswer
	cur.Error = run.Error
	cur.GitDiff = run.GitDiff
	cur.UpdatedAt = time.Now()
	cur.FinishedAt = run.FinishedAt
	return true, nil
}

// AppendOwnedEvent áp dụng nguyên tử Fencing Token cho event stream
func (db *ClusterFencedDB) AppendOwnedEvent(ctx context.Context, event *domain.AgentRunEvent, workerID string, claimGeneration int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[event.RunID]
	if !exists {
		return false, nil
	}

	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration {
		return false, nil
	}

	id := atomic.AddInt64(&db.eventCounter, 1)
	ev := *event
	ev.ID = id
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	db.events[event.RunID] = append(db.events[event.RunID], ev)
	event.ID = id
	return true, nil
}

func (db *ClusterFencedDB) FindByTenantAndIdempotencyKey(ctx context.Context, tenantID, key string) (*domain.AgentRun, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if tenantID == "" {
		tenantID = "default"
	}
	idemKey := tenantID + ":" + key
	runID, exists := db.idempotency[idemKey]
	if !exists {
		return nil, nil
	}
	r := db.runs[runID]
	if r == nil {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (db *ClusterFencedDB) ValidateOwnership(ctx context.Context, runID, workerID string, claimGeneration int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists {
		return false, nil
	}
	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration {
		return false, nil
	}
	if cur.Status != domain.RunStatusRunning && cur.Status != domain.RunStatusRecovering && cur.Status != domain.RunStatusWaitingForTool {
		return false, nil
	}
	now := time.Now()
	if cur.LeaseUntil != nil && cur.LeaseUntil.Before(now) {
		return false, nil
	}
	return true, nil
}

func (db *ClusterFencedDB) RecordPlannedOrRunning(ctx context.Context, exec *domain.ToolExecutionRecord) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if exec == nil {
		return errors.New("exec is nil")
	}
	key := fmt.Sprintf("%s:%s:%s", exec.TenantID, exec.RunID, exec.ToolCallID)
	now := time.Now()
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}
	cp := *exec
	db.toolLedger[key] = &cp
	return nil
}

func (db *ClusterFencedDB) RecordFinished(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", tenantID, runID, toolCallID)
	now := time.Now()
	rec, exists := db.toolLedger[key]
	if !exists {
		db.toolLedger[key] = &domain.ToolExecutionRecord{
			TenantID:   tenantID,
			RunID:      runID,
			ToolCallID: toolCallID,
			Status:     status,
			ResultJSON: resultJSON,
			Error:      errStr,
			FinishedAt: &now,
		}
		return nil
	}
	rec.Status = status
	rec.ResultJSON = resultJSON
	rec.Error = errStr
	rec.FinishedAt = &now
	return nil
}

func (db *ClusterFencedDB) GetToolExecution(ctx context.Context, tenantID, runID, toolCallID string) (*domain.ToolExecutionRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	key := fmt.Sprintf("%s:%s:%s", tenantID, runID, toolCallID)
	rec, exists := db.toolLedger[key]
	if !exists {
		return nil, nil
	}
	cp := *rec
	return &cp, nil
}

func (db *ClusterFencedDB) ListToolExecutions(ctx context.Context, tenantID, runID string) ([]*domain.ToolExecutionRecord, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	prefix := fmt.Sprintf("%s:%s:", tenantID, runID)
	var list []*domain.ToolExecutionRecord
	for k, rec := range db.toolLedger {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			cp := *rec
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (db *ClusterFencedDB) RecordPlannedOrRunningOwned(ctx context.Context, exec *domain.ToolExecutionRecord, workerID string, claimGeneration int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if exec == nil {
		return false, errors.New("exec is nil")
	}
	cur, exists := db.runs[exec.RunID]
	if !exists {
		return false, nil
	}
	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration {
		return false, nil
	}
	if cur.Status != domain.RunStatusRunning && cur.Status != domain.RunStatusRecovering && cur.Status != domain.RunStatusWaitingForTool {
		return false, nil
	}
	now := time.Now()
	if cur.LeaseUntil != nil && cur.LeaseUntil.Before(now) {
		return false, nil
	}

	key := fmt.Sprintf("%s:%s:%s", exec.TenantID, exec.RunID, exec.ToolCallID)
	if existing, found := db.toolLedger[key]; found {
		if existing.Status == domain.ToolExecutionSucceeded {
			return false, nil
		}
	}
	if exec.StartedAt == nil {
		exec.StartedAt = &now
	}
	cp := *exec
	cp.WorkerID = workerID
	cp.ClaimGeneration = claimGeneration
	db.toolLedger[key] = &cp
	return true, nil
}

func (db *ClusterFencedDB) RecordFinishedOwned(ctx context.Context, tenantID, runID, toolCallID string, status domain.ToolExecutionStatus, resultJSON, errStr string, workerID string, claimGeneration int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	cur, exists := db.runs[runID]
	if !exists {
		return false, nil
	}
	if cur.WorkerID != workerID || cur.ClaimGeneration != claimGeneration {
		return false, nil
	}
	if cur.Status == domain.RunStatusCancelled {
		return false, nil
	}

	key := fmt.Sprintf("%s:%s:%s", tenantID, runID, toolCallID)
	now := time.Now()
	rec, exists := db.toolLedger[key]
	if !exists {
		db.toolLedger[key] = &domain.ToolExecutionRecord{
			TenantID:        tenantID,
			RunID:           runID,
			ToolCallID:      toolCallID,
			Status:          status,
			ResultJSON:      resultJSON,
			Error:           errStr,
			WorkerID:        workerID,
			ClaimGeneration: claimGeneration,
			FinishedAt:      &now,
		}
		return true, nil
	}
	rec.Status = status
	rec.ResultJSON = resultJSON
	rec.Error = errStr
	rec.FinishedAt = &now
	return true, nil
}

// CheckpointRepository implementation
func (db *ClusterFencedDB) SaveCheckpoint(ctx context.Context, cp *domain.AgentCheckpoint) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	item := *cp
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	db.checkpoints[cp.TaskID] = append(db.checkpoints[cp.TaskID], &item)
	return nil
}

func (db *ClusterFencedDB) GetLatestCheckpoint(ctx context.Context, taskID string) (*domain.AgentCheckpoint, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	list := db.checkpoints[taskID]
	if len(list) == 0 {
		return nil, nil
	}
	return list[len(list)-1], nil
}

func (db *ClusterFencedDB) ListCheckpoints(ctx context.Context, taskID string) ([]*domain.AgentCheckpoint, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	return db.checkpoints[taskID], nil
}

func (db *ClusterFencedDB) DeleteCheckpoints(ctx context.Context, taskID string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	delete(db.checkpoints, taskID)
	return nil
}

// ClusterSharedMediaStorage mô phỏng S3 / MinIO lưu trữ đối tượng nhị phân dùng chung
type ClusterSharedMediaStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	assets  map[string]*domain.MediaAsset
}

var _ ports.MediaStorage = (*ClusterSharedMediaStorage)(nil)

func NewClusterSharedMediaStorage() *ClusterSharedMediaStorage {
	return &ClusterSharedMediaStorage{
		objects: make(map[string][]byte),
		assets:  make(map[string]*domain.MediaAsset),
	}
}

func (s *ClusterSharedMediaStorage) SaveAsset(ctx context.Context, asset *domain.MediaAsset, content io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	cp := *asset
	cp.SizeBytes = int64(len(b))
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.assets[asset.ID] = &cp
	s.objects[asset.ID] = b
	return nil
}

func (s *ClusterSharedMediaStorage) GetAsset(ctx context.Context, assetID string) (*domain.MediaAsset, io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	asset, exists := s.assets[assetID]
	if !exists {
		return nil, nil, os.ErrNotExist
	}
	b, ok := s.objects[assetID]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	cp := *asset
	return &cp, io.NopCloser(bytes.NewReader(b)), nil
}

func (s *ClusterSharedMediaStorage) DeleteAsset(ctx context.Context, assetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.assets, assetID)
	delete(s.objects, assetID)
	return nil
}

func (s *ClusterSharedMediaStorage) ListAssets(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*domain.MediaAsset
	for _, a := range s.assets {
		if kind == "" || a.Kind == kind {
			cp := *a
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *ClusterSharedMediaStorage) DeleteExpired(ctx context.Context, maxAgeDays int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	threshold := time.Now().AddDate(0, 0, -maxAgeDays)
	count := 0
	for id, a := range s.assets {
		if a.CreatedAt.Before(threshold) {
			delete(s.assets, id)
			delete(s.objects, id)
			count++
		}
	}
	return count, nil
}
