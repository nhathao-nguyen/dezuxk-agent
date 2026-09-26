package google

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type FlowClientAdapter struct {
	transport   ports.UpstreamGoogleTransport
	rpcRegistry *domain.RpcRegistry
	marshaller  *Marshaller
	metrics     *domain.ContractMetrics
}

func NewFlowClientAdapter(transport ports.UpstreamGoogleTransport, rpcRegistry *domain.RpcRegistry, metrics *domain.ContractMetrics) ports.FlowClient {
	return &FlowClientAdapter{
		transport:   transport,
		rpcRegistry: rpcRegistry,
		marshaller:  NewMarshaller(),
		metrics:     metrics,
	}
}

// GetCreditsBalance đọc số dư Flow qua nzlxg. Không suy tier từ các phần tử chưa đặt tên.
// upstream_unavailable được gọi lại đúng một lần, sau một khoảng jitter.
func (c *FlowClientAdapter) GetCreditsBalance(ctx context.Context, account *domain.ManagedAccount) (domain.FlowCreditBalance, error) {
	balance, err := c.readCredits(ctx, account)
	if !upstreamUnavailable(err) || ctx.Err() != nil {
		return balance, err
	}
	if waitErr := waitCreditRetry(ctx); waitErr != nil {
		return balance, err
	}
	return c.readCredits(ctx, account)
}

func (c *FlowClientAdapter) readCredits(ctx context.Context, account *domain.ManagedAccount) (domain.FlowCreditBalance, error) {
	if account == nil || account.GetAtToken(domain.ServiceFlow) == "" {
		return domain.FlowCreditBalance{}, domain.CodecExpired(domain.OriginNzlxg, domain.ServiceFlow, "phiên chưa có bí mật dẫn xuất")
	}
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()
	metrics := c.metrics.Bind(domain.OpFlowGetCredits)

	rpc, err := c.rpcRegistry.MustFind("nzlxg")
	if err != nil {
		return domain.FlowCreditBalance{}, domain.CodecRejected(domain.OriginNzlxg, domain.ServiceFlow, "không có đường dẫn số dư")
	}

	postBody, err := c.marshaller.EncodeBatchexecute("nzlxg", []any{}, account.GetAtToken(domain.ServiceFlow))
	if err != nil {
		return domain.FlowCreditBalance{}, domain.CodecRejected(domain.OriginNzlxg, domain.ServiceFlow, "không đóng gói được yêu cầu số dư")
	}

	reqPath := rpc.PathPattern
	if rpc.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = rpc.TargetHost + reqPath
	}

	resp, err := c.transport.DoRequest(
		ctx,
		account,
		domain.ServiceFlow,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return domain.FlowCreditBalance{}, domain.CodecTransport(domain.OriginNzlxg, domain.ServiceFlow, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.FlowCreditBalance{}, StatusError(resp, domain.OriginNzlxg, true, domain.ServiceFlow)
	}

	payloadStr, err := extractBatchexecutePayload(resp.Body, "nzlxg")
	if err != nil {
		metrics.AddSchema()
		return domain.FlowCreditBalance{}, domain.CodecSchema(domain.OriginNzlxg, domain.ServiceFlow, "phản hồi số dư không đúng hợp đồng")
	}

	balance, err := ParseNzlxgBalance(payloadStr, metrics)
	if err != nil {
		return domain.FlowCreditBalance{}, err
	}
	account.CreditsBalance = balance.Amount
	return balance, nil
}

// RegisterSessionLock đăng ký khóa phiên RPC csbIsb tránh trừ âm credit (CRITICAL_INTEGRATION_GUIDE.md mục 5.1)
func (c *FlowClientAdapter) RegisterSessionLock(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	atToken := account.GetAtToken(domain.ServiceFlow)
	if atToken == "" {
		return domain.CodecExpired("csbIsb", domain.ServiceFlow, "phiên chưa có bí mật dẫn xuất")
	}

	rpc, err := c.rpcRegistry.MustFind("csbIsb")
	if err != nil {
		return domain.CodecRejected("csbIsb", domain.ServiceFlow, "không có đường dẫn khóa phiên")
	}

	// Docs-2 quy định payload csbIsb bắt buộc phải là ["projects/<PROJECT_UUID>"]
	param := fmt.Sprintf("projects/%s", projectUUID)
	postBody, err := c.marshaller.EncodeBatchexecute("csbIsb", []interface{}{param}, atToken)
	if err != nil {
		return domain.CodecRejected("csbIsb", domain.ServiceFlow, "không đóng gói được khóa phiên")
	}

	reqPath := rpc.PathPattern
	if rpc.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = rpc.TargetHost + reqPath
	}

	resp, err := c.transport.DoRequest(
		ctx,
		account,
		domain.ServiceFlow,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return domain.CodecTransport("csbIsb", domain.ServiceFlow, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return StatusError(resp, "csbIsb", false, domain.ServiceFlow)
	}

	metrics := c.metrics.Bind("flow.session_lock")
	payloadStr, err := extractBatchexecutePayload(resp.Body, "csbIsb")
	if err != nil {
		metrics.AddSchema()
		return domain.CodecSchema("csbIsb", domain.ServiceFlow, "phản hồi khóa phiên không đúng hợp đồng")
	}

	return domain.ParseSessionLockResponse(payloadStr, metrics)
}

// CreateProject tạo dự án mới cấp phát UUID qua RPC jHPbke (docs-2/flow/project_create.md)
func (c *FlowClientAdapter) CreateProject(ctx context.Context, account *domain.ManagedAccount, title string) (string, error) {
	atToken := account.GetAtToken(domain.ServiceFlow)
	if atToken == "" {
		return "", domain.CodecExpired(domain.OriginCreateProject, domain.ServiceFlow, "phiên chưa có bí mật dẫn xuất")
	}

	rpc, err := c.rpcRegistry.MustFind("jHPbke")
	if err != nil {
		return "", domain.CodecRejected(domain.OriginCreateProject, domain.ServiceFlow, "không có đường dẫn tạo dự án")
	}

	postBody, err := c.marshaller.EncodeBatchexecute("jHPbke", domain.BuildCreateProjectInner(title), atToken)
	if err != nil {
		return "", domain.CodecRejected(domain.OriginCreateProject, domain.ServiceFlow, "không đóng gói được yêu cầu tạo dự án")
	}

	reqPath := rpc.PathPattern
	if rpc.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = rpc.TargetHost + reqPath
	}

	resp, err := c.transport.DoRequest(
		ctx,
		account,
		domain.ServiceFlow,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return "", domain.CodecTransport(domain.OriginCreateProject, domain.ServiceFlow, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", StatusError(resp, domain.OriginCreateProject, false, domain.ServiceFlow)
	}

	metrics := c.metrics.Bind(domain.OpFlowCreateProject)
	payload, err := extractBatchexecutePayload(resp.Body, "jHPbke")
	if err != nil {
		metrics.AddSchema()
		return "", domain.CodecSchema(domain.OriginCreateProject, domain.ServiceFlow, "phản hồi tạo dự án không đúng hợp đồng")
	}
	id, unmapped, err := domain.ParseCreatedProject(payload)
	if err != nil {
		metrics.AddSchema()
		return "", err
	}
	metrics.AddUnmapped(unmapped)
	return id, nil
}

func upstreamUnavailable(err error) bool {
	class, _, ok := domain.ClassifiedFailure(err)
	return ok && class == domain.ClassUpstreamUnavailable
}

func waitCreditRetry(ctx context.Context) error {
	delay := 20*time.Millisecond + time.Duration(rand.Int63n(int64(60*time.Millisecond)))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// extractBatchexecutePayload giải mã envelope [[["wrb.fr", rpcID, innerJSON]]]
func extractBatchexecutePayload(body io.Reader, rpcID string) (string, error) {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineStr := strings.TrimSpace(string(line))
			if lineStr == "" || strings.HasPrefix(lineStr, ")]}'") || digitOnlyRegex.MatchString(lineStr) {
				continue
			}
			if strings.HasPrefix(lineStr, "[") && strings.HasSuffix(lineStr, "]") {
				var envelope [][][]interface{}
				if err := json.Unmarshal([]byte(lineStr), &envelope); err == nil {
					for _, outer := range envelope {
						for _, item := range outer {
							if len(item) >= 3 && item[0] == "wrb.fr" && item[1] == rpcID {
								if innerStr, ok := item[2].(string); ok {
									return innerStr, nil
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
	}
	return "", fmt.Errorf("không tìm thấy payload của RPC %s trong phản hồi", rpcID)
}

// ListProjects truy xuất danh sách các dự án qua RPC UpteDb
func (c *FlowClientAdapter) ListProjects(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowProject, error) {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	payload, err := c.executeBatchexecute(ctx, account, "UpteDb", []any{})
	if err != nil {
		return nil, err
	}
	metrics := c.metrics.Bind(domain.OpFlowProjects)
	return domain.ParseProjectsListResponse(payload, metrics)
}

// MoveProjectToTrash chuyển dự án vào thùng rác qua RPC dK3x9
func (c *FlowClientAdapter) MoveProjectToTrash(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	_, err := c.executeBatchexecute(ctx, account, "dK3x9", domain.BuildTrashMovePayload(projectUUID))
	return err
}

// ListTrash lấy danh sách dự án trong thùng rác qua RPC tB6q8
func (c *FlowClientAdapter) ListTrash(ctx context.Context, account *domain.ManagedAccount) ([]domain.FlowTrashProject, error) {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	payload, err := c.executeBatchexecute(ctx, account, "tB6q8", domain.BuildListTrashPayload())
	if err != nil {
		return nil, err
	}
	metrics := c.metrics.Bind(domain.OpFlowTrash)
	return domain.ParseTrashListResponse(payload, metrics)
}

// RestoreProject khôi phục dự án khỏi thùng rác qua RPC rS4y1
func (c *FlowClientAdapter) RestoreProject(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	_, err := c.executeBatchexecute(ctx, account, "rS4y1", domain.BuildTrashRestorePayload(projectUUID))
	return err
}

// DeleteProjectPermanently xóa vĩnh viễn dự án qua RPC mrlkwd
func (c *FlowClientAdapter) DeleteProjectPermanently(ctx context.Context, account *domain.ManagedAccount, projectUUID string) error {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	_, err := c.executeBatchexecute(ctx, account, "mrlkwd", domain.BuildPermanentDeletePayload(projectUUID))
	return err
}

// GetActiveModels đồng bộ ma trận mô hình qua RPC HTrJv và yBhWQ
func (c *FlowClientAdapter) GetActiveModels(ctx context.Context, account *domain.ManagedAccount) (map[string]bool, error) {
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	// 1. Thử gọi RPC HTrJv (Ma trận mô hình phần cứng Veo 3.1 & Abra kèm biểu phí Credit)
	_, _ = c.executeBatchexecute(ctx, account, "HTrJv", []any{})

	// 2. Gọi RPC yBhWQ (Danh sách các mô hình GPU đang hoạt động trực tuyến)
	payload, err := c.executeBatchexecute(ctx, account, "yBhWQ", []any{})
	if err != nil {
		return nil, err
	}
	metrics := c.metrics.Bind("flow.active_models")
	return domain.ParseActiveModelsResponse(payload, metrics)
}

// ListVoicePersonas gọi RPC Zzl0ze để lấy danh sách nhân vật và giọng đọc AI trực tiếp từ Google Flow (docs-2 mục character_and_reference.md)
func (c *FlowClientAdapter) ListVoicePersonas(ctx context.Context, account *domain.ManagedAccount, projectUUID string) ([]domain.VoicePersona, error) {
	if projectUUID == "" && account != nil {
		projectUUID = account.FlowProjectID
	}
	if projectUUID == "" {
		return nil, domain.InvalidRequest("Zzl0ze", "Zzl0ze", domain.ServiceFlow, "thiếu project_uuid để gọi RPC Zzl0ze")
	}
	ctx, cancel := c.transport.BoundShort(ctx)
	defer cancel()

	payload, err := c.executeBatchexecute(ctx, account, "Zzl0ze", domain.BuildGetCharactersPayload(projectUUID))
	if err != nil {
		return nil, err
	}
	metrics := c.metrics.Bind("flow.voices")
	return domain.ParseVoicePersonasResponse(payload, metrics)
}

func (c *FlowClientAdapter) executeBatchexecute(ctx context.Context, account *domain.ManagedAccount, rpcID string, innerPayload any) (string, error) {
	if account == nil {
		return "", domain.Unauthenticated(rpcID, rpcID, domain.ServiceFlow, "tài khoản rỗng")
	}
	atToken := account.GetAtToken(domain.ServiceFlow)
	if atToken == "" {
		return "", domain.CodecExpired(rpcID, domain.ServiceFlow, "phiên chưa có bí mật dẫn xuất")
	}

	rpc, err := c.rpcRegistry.MustFind(rpcID)
	if err != nil {
		return "", domain.CodecRejected(rpcID, domain.ServiceFlow, fmt.Sprintf("không có đường dẫn rpc %s", rpcID))
	}

	postBody, err := c.marshaller.EncodeBatchexecute(rpcID, innerPayload, atToken)
	if err != nil {
		return "", domain.CodecRejected(rpcID, domain.ServiceFlow, fmt.Sprintf("không đóng gói được yêu cầu %s", rpcID))
	}

	reqPath := rpc.PathPattern
	if rpc.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = rpc.TargetHost + reqPath
	}

	resp, err := c.transport.DoRequest(
		ctx,
		account,
		domain.ServiceFlow,
		http.MethodPost,
		reqPath,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return "", domain.CodecTransport(rpcID, domain.ServiceFlow, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", StatusError(resp, rpcID, false, domain.ServiceFlow)
	}

	payload, err := extractBatchexecutePayload(resp.Body, rpcID)
	if err != nil {
		if c.metrics != nil {
			c.metrics.Bind(rpcID).AddSchema()
		}
		return "", domain.CodecSchema(rpcID, domain.ServiceFlow, fmt.Sprintf("phản hồi %s không đúng hợp đồng", rpcID))
	}
	return payload, nil
}
