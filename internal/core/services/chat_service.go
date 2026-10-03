package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/ports"
)

type ChatService struct {
	modelRegistry    *domain.ModelRegistry
	sessionRepo      ports.SessionRepository
	upstream         ports.UpstreamGoogleTransport
	wire             ports.WireCodec
	metrics          *domain.ContractMetrics
	storage          ports.MediaRepository
	visionResolver   *VisionResolver
	tokenCounter     *TokenCounter
	failoverConfig   config.FailoverConfig
	leaseWaitTimeout time.Duration
	rpcRegistry      *domain.RpcRegistry
	keyUseCase       ports.KeyUseCase
	chatDefaults     config.ChatDefaultsConfig
}

func NewChatService(
	mr *domain.ModelRegistry,
	sr ports.SessionRepository,
	up ports.UpstreamGoogleTransport,
	wire ports.WireCodec,
	metrics *domain.ContractMetrics,
) *ChatService {
	return &ChatService{
		modelRegistry: mr,
		sessionRepo:   sr,
		upstream:      up,
		wire:          wire,
		metrics:       metrics,
		rpcRegistry:   domain.DefaultRpcRegistry(),
		tokenCounter:  NewTokenCounter(config.TokensConfig{}),
		failoverConfig: config.FailoverConfig{
			MaxAttempts:     3,
			CoolingDuration: 60 * time.Second,
		},
		leaseWaitTimeout: 3 * time.Second,
	}
}

func (s *ChatService) SetStorage(storage ports.MediaRepository) {
	s.storage = storage
}

func (s *ChatService) SetVisionResolver(vr *VisionResolver) {
	s.visionResolver = vr
}

func (s *ChatService) SetTokenCounter(tc *TokenCounter) {
	s.tokenCounter = tc
}

func (s *ChatService) SetKeyUseCase(k ports.KeyUseCase) {
	s.keyUseCase = k
}

func (s *ChatService) SetFailoverConfig(fc config.FailoverConfig) {
	s.failoverConfig = fc
}

func (s *ChatService) SetLeaseWaitTimeout(d time.Duration) {
	s.leaseWaitTimeout = d
}

func (s *ChatService) SetRpcRegistry(r *domain.RpcRegistry) {
	if r != nil {
		s.rpcRegistry = r
	}
}

func (s *ChatService) SetChatDefaults(cd config.ChatDefaultsConfig) {
	s.chatDefaults = cd
}

func (s *ChatService) ExecuteChatStream(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
	streamWriter io.Writer,
	flusher func(),
) error {
	modelDesc, err := s.prepareModel(req)
	if err != nil {
		return err
	}
	var flushedToClient bool
	return s.runGeminiFailover(ctx, func(account *domain.ManagedAccount) (bool, error) {
		callErr := s.streamRound(ctx, account, modelDesc, req, streamWriter, flusher, &flushedToClient)
		return !flushedToClient, callErr
	})
}

func (s *ChatService) ExecuteChatSync(
	ctx context.Context,
	req *domain.OpenAIChatRequest,
) (*domain.OpenAIChatResponse, error) {
	modelDesc, err := s.prepareModel(req)
	if err != nil {
		return nil, err
	}
	var resp *domain.OpenAIChatResponse
	err = s.runGeminiFailover(ctx, func(account *domain.ManagedAccount) (bool, error) {
		var callErr error
		resp, callErr = s.syncRound(ctx, account, modelDesc, req)
		return true, callErr
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *ChatService) prepareModel(req *domain.OpenAIChatRequest) (*domain.ModelDescriptor, error) {
	if req == nil || len(req.Messages) == 0 {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "thiếu hội thoại")
	}

	desc, ok := s.modelRegistry.ResolveGeminiModel(req.Model)
	if !ok {
		return nil, domain.InvalidRequest(domain.OpChatCompletions, "", domain.ServiceGemini, "không có mô hình Gemini khả dụng")
	}

	req.Model = desc.ID
	return &desc, nil
}

type failoverAction func(account *domain.ManagedAccount) (canRetry bool, err error)

func (s *ChatService) runGeminiFailover(ctx context.Context, action failoverAction) error {
	maxAttempts := s.failoverConfig.GetMaxAttempts()
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	triedAccounts := make(map[string]struct{})
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		account, err := s.sessionRepo.GetAvailable(ctx, domain.ServiceGemini, 0)
		if err != nil {
			if lastErr != nil {
				return domain.EnsureGateway(lastErr, domain.OpChatCompletions, domain.ServiceGemini)
			}
			if _, ok := domain.AsGatewayError(err); ok {
				return err
			}
			return domain.Unauthenticated(domain.OpChatCompletions, "", domain.ServiceGemini, "chưa có phiên Gemini sẵn sàng").WithPublicStatus(http.StatusServiceUnavailable)
		}

		if _, alreadyTried := triedAccounts[account.ID]; alreadyTried {
			s.sessionRepo.Release(account, nil)
			// Kiểm tra xem đã thử hết tất cả tài khoản trong hệ thống chưa
			allAccs := s.sessionRepo.ListAll(ctx)
			allTried := true
			for _, a := range allAccs {
				if _, ok := triedAccounts[a.ID]; !ok {
					allTried = false
					break
				}
			}
			if allTried {
				break
			}
			// Nếu còn tài khoản chưa thử, đợi nhẹ để bộ điều phối giải phóng lượt chọn
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		triedAccounts[account.ID] = struct{}{}

		if !s.sessionRepo.TryWriteLease(account, domain.ServiceGemini) {
			allAccs := s.sessionRepo.ListAll(ctx)
			if len(allAccs) <= 1 {
				// Hàng đợi chờ giải phóng Lease ghi (Wait Queue) cho cấu hình 1 tài khoản
				waitDuration := s.leaseWaitTimeout
				if waitDuration <= 0 {
					waitDuration = 3 * time.Second
				}
				acquired := false
				waitDeadline := time.Now().Add(waitDuration)
				for time.Now().Before(waitDeadline) {
					select {
					case <-ctx.Done():
						s.sessionRepo.Release(account, nil)
						return ctx.Err()
					case <-time.After(50 * time.Millisecond):
					}
					if s.sessionRepo.TryWriteLease(account, domain.ServiceGemini) {
						acquired = true
						break
					}
				}
				if !acquired {
					s.sessionRepo.Release(account, nil)
					return domain.Conflict(domain.OpChatCompletions, domain.OriginStreamGenerate, domain.ServiceGemini, "phiên đang bận một tác vụ ghi")
				}
			} else {
				s.sessionRepo.Release(account, nil)
				lastErr = domain.Conflict(domain.OpChatCompletions, domain.OriginStreamGenerate, domain.ServiceGemini, "phiên đang bận một tác vụ ghi")
				continue
			}
		}

		var canRetry bool
		callErr := session.RetryAfterRefresh(ctx, s.sessionRepo, account, domain.ServiceGemini, func() error {
			var err error
			canRetry, err = action(account)
			return err
		})

		// 1. Client ngắt kết nối (Client Disconnect)
		if ctx.Err() != nil || errors.Is(callErr, context.Canceled) {
			s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
			s.sessionRepo.Release(account, nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return callErr
		}

		// 2. Thành công
		if callErr == nil {
			s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
			s.sessionRepo.Release(account, nil)
			return nil
		}

		// 3. Xử lý lỗi
		lastErr = callErr
		class, _, _ := domain.ClassifiedFailure(callErr)

		coolingDuration := s.failoverConfig.GetCoolingDuration()
		if coolingDuration <= 0 {
			coolingDuration = 60 * time.Second
		}

		if class == domain.ClassRateLimited {
			_ = account.MoveService(domain.ServiceGemini, domain.StateCooling)
			account.CoolService(domain.ServiceGemini, time.Now().Add(coolingDuration), class)
			account.SetCooldown(time.Now().Add(coolingDuration))
		} else if class == domain.ClassUpstreamUnavailable {
			_ = account.MoveService(domain.ServiceGemini, domain.StateCooling)
			account.CoolService(domain.ServiceGemini, time.Now().Add(coolingDuration), class)
			account.SetCooldown(time.Now().Add(coolingDuration))
		} else if class == domain.ClassExpired {
			account.HealthStatus = domain.HealthStatusAuthExpired
		}

		s.sessionRepo.ReleaseWriteLease(account, domain.ServiceGemini)
		s.sessionRepo.Release(account, callErr)

		if !canRetry || !isFailoverCandidate(callErr) {
			return domain.EnsureGateway(callErr, domain.OpChatCompletions, domain.ServiceGemini)
		}
	}

	if lastErr != nil {
		return domain.EnsureGateway(lastErr, domain.OpChatCompletions, domain.ServiceGemini)
	}
	return domain.UpstreamUnavailable(domain.OpChatCompletions, "", domain.ServiceGemini, "hết tài khoản khả dụng để hoàn thành yêu cầu")
}

func isFailoverCandidate(err error) bool {
	if err == nil {
		return false
	}
	class, _, ok := domain.ClassifiedFailure(err)
	if !ok {
		return false
	}
	switch class {
	case domain.ClassRateLimited, domain.ClassUpstreamUnavailable, domain.ClassExpired, domain.ClassUnauthorized:
		return true
	case domain.ClassUpstreamRejected:
		if ge, found := domain.AsGatewayError(err); found {
			if ge.OriginStatus == http.StatusTooManyRequests ||
				ge.OriginStatus == http.StatusServiceUnavailable ||
				ge.OriginStatus == http.StatusGatewayTimeout ||
				ge.OriginStatus == http.StatusBadGateway {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *ChatService) streamRound(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
	streamWriter io.Writer,
	flusher func(),
	flushedToClient *bool,
) error {
	ctx, cancel := boundStream(s.upstream, ctx)
	defer cancel()
	resp, err := s.postGemini(ctx, account, modelDesc, req)
	if err != nil {
		return err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	createdTime := time.Now().Unix()
	var conversationID string
	filter := NewStreamToolFilter(len(req.Tools) > 0, streamWriter, flusher, flushedToClient, createdTime, req.Model, conversationID)
	filter.SetToolsConfig(req.Tools, req.ToolChoice)

	var streamedReasoning bool

	onContent := func(delta, cID string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if cID != "" {
			conversationID = cID
			filter.SetConversationID(cID)
		}
		return filter.OnDelta(delta, cID)
	}

	onReasoning := func(delta, cID string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if delta == "" {
			return nil
		}
		if cID != "" {
			conversationID = cID
			filter.SetConversationID(cID)
		}
		streamedReasoning = true
		reasoningChunk := domain.OpenAIChatResponse{
			ID:             "chatcmpl-" + conversationID,
			Object:         "chat.completion.chunk",
			Created:        createdTime,
			Model:          req.Model,
			ConversationID: conversationID,
			Choices: []domain.OpenAIChoice{{
				Index: 0,
				Delta: domain.OpenAIDelta{
					ReasoningContent: delta,
				},
			}},
		}
		b, err := json.Marshal(reasoningChunk)
		if err != nil {
			return err
		}
		if _, writeErr := fmt.Fprintf(streamWriter, "data: %s\n\n", b); writeErr != nil {
			return writeErr
		}
		if flusher != nil {
			flusher()
		}
		if flushedToClient != nil {
			*flushedToClient = true
		}
		return nil
	}

	reply, err := s.wire.DematerializeChatStream(ctx, resp, s.metrics.Bind(domain.OpChatCompletions), onContent, onReasoning)
	if conversationID == "" {
		conversationID = reply.ConversationID
		filter.SetConversationID(conversationID)
	}
	if flushErr := filter.FlushRemaining(); flushErr != nil && err == nil {
		err = flushErr
	}
	if err != nil {
		return normalizeWireErr(err)
	}

	// Tải và cache media cục bộ cho chunk cuối cùng
	if s.storage != nil && len(reply.MediaURLs) > 0 {
		cookies := ""
		userAgent := ""
		if account != nil {
			if account.Jar != nil {
				cookies = account.Jar.GetCookieHeader(false)
			}
			userAgent = account.UserAgent
		}

		userPrompt := ""
		if len(req.Messages) > 0 {
			userPrompt = req.Messages[len(req.Messages)-1].Content
		}

		var cachedURLs []string
		for _, rawURL := range reply.MediaURLs {
			cachedAsset, cacheErr := s.storage.DownloadAndCacheWithAuth(
				ctx,
				rawURL,
				domain.MediaImagePNG,
				userPrompt,
				req.Model,
				cookies,
				userAgent,
			)
			if cacheErr == nil && cachedAsset != nil && cachedAsset.LocalURL != "" {
				cachedURLs = append(cachedURLs, cachedAsset.LocalURL)
			} else {
				cachedURLs = append(cachedURLs, rawURL)
			}
		}
		reply.MediaURLs = cachedURLs
	}

	var usage *domain.OpenAIUsage
	if s.tokenCounter != nil {
		usage = s.tokenCounter.CalculateUsage(req.Messages, reply)
	}

	if s.keyUseCase != nil && usage != nil {
		keyID := ""
		if vKey := domain.VirtualKeyFromContext(ctx); vKey != nil {
			keyID = vKey.ID
		} else if bill, ok := domain.BillingIdentityFromContext(ctx); ok && bill.KeyID != "" {
			keyID = bill.KeyID
		}
		if keyID != "" {
			_ = s.keyUseCase.RecordTokenUsage(ctx, keyID, usage.PromptTokens, usage.CompletionTokens)
		}
	}

	toolCalls := filter.GetEmittedToolCalls()
	if len(toolCalls) == 0 && (len(req.Tools) > 0 || req.ToolChoice != nil) {
		_, fallbackCalls := ExtractToolCallsWithAllowed(reply.Text, req.Tools)
		if len(fallbackCalls) > 0 {
			if validCalls, err := ValidateAndNormalizeToolCalls(fallbackCalls, req.Tools, req.ToolChoice); err == nil && len(validCalls) > 0 {
				toolCalls = validCalls
				// Phát chunk tool_calls bổ sung nếu chưa được phát qua stream
				toolChunk := domain.OpenAIChatResponse{
					ID:             "chatcmpl-" + conversationID,
					Object:         "chat.completion.chunk",
					Created:        createdTime,
					Model:          req.Model,
					ConversationID: conversationID,
					Choices: []domain.OpenAIChoice{{
						Index: 0,
						Delta: domain.OpenAIDelta{
							Role:      "assistant",
							ToolCalls: toolCalls,
						},
					}},
				}
				if b, err := json.Marshal(toolChunk); err == nil {
					_, _ = fmt.Fprintf(streamWriter, "data: %s\n\n", b)
					if flusher != nil {
						flusher()
					}
				}
			}
		}
	}

	var reasoningText string
	if len(reply.ThinkingBlocks) > 0 {
		var rParts []string
		for _, tb := range reply.ThinkingBlocks {
			if strings.TrimSpace(tb.Content) != "" {
				rParts = append(rParts, tb.Content)
			}
		}
		reasoningText = strings.Join(rParts, "\n\n")
	}

	// Phát chunk reasoning_content dự phòng nếu chưa được phát qua stream token-by-token
	if !streamedReasoning && reasoningText != "" {
		reasoningChunk := domain.OpenAIChatResponse{
			ID:             "chatcmpl-" + conversationID,
			Object:         "chat.completion.chunk",
			Created:        createdTime,
			Model:          req.Model,
			ConversationID: conversationID,
			Choices: []domain.OpenAIChoice{{
				Index: 0,
				Delta: domain.OpenAIDelta{
					ReasoningContent: reasoningText,
				},
			}},
		}
		if b, err := json.Marshal(reasoningChunk); err == nil {
			_, _ = fmt.Fprintf(streamWriter, "data: %s\n\n", b)
			if flusher != nil {
				flusher()
			}
		}
	}

	stopReason := "stop"
	if len(toolCalls) > 0 {
		stopReason = "tool_calls"
	} else if maxTokens := req.EffectiveMaxTokens(); maxTokens != nil && *maxTokens > 0 && usage != nil && usage.CompletionTokens >= *maxTokens {
		stopReason = "length"
	}

	finalChunk := domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + conversationID,
		Object:         "chat.completion.chunk",
		Created:        createdTime,
		Model:          req.Model,
		ConversationID: conversationID,
		ResponseID:     reply.ResponseID,
		ChoiceID:       reply.ChoiceID,
		Thinking:       reply.ThinkingBlocks,
		Grounding:      reply.Grounding,
		CodeExecutions: reply.CodeExecutions,
		MediaURLs:      reply.MediaURLs,
		Usage:          usage,
		Choices: []domain.OpenAIChoice{
			{
				Index:        0,
				Delta:        domain.OpenAIDelta{},
				FinishReason: &stopReason,
			},
		},
	}
	finalBytes, _ := json.Marshal(finalChunk)
	if _, writeErr := fmt.Fprintf(streamWriter, "data: %s\n\n", finalBytes); writeErr != nil {
		return writeErr
	}
	if _, writeErr := fmt.Fprintf(streamWriter, "data: [DONE]\n\n"); writeErr != nil {
		return writeErr
	}
	if flusher != nil {
		flusher()
	}
	if flushedToClient != nil {
		*flushedToClient = true
	}
	return nil
}

func (s *ChatService) syncRound(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
) (*domain.OpenAIChatResponse, error) {
	ctx, cancel := boundStream(s.upstream, ctx)
	defer cancel()
	resp, err := s.postGemini(ctx, account, modelDesc, req)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	reply, err := s.wire.DematerializeChat(ctx, resp, s.metrics.Bind(domain.OpChatCompletions), nil)
	if err != nil {
		return nil, normalizeWireErr(err)
	}

	// Tải và cache media cục bộ chống lỗi 403 Google CDN và bảo toàn TTL
	if s.storage != nil && len(reply.MediaURLs) > 0 {
		cookies := ""
		userAgent := ""
		if account != nil {
			if account.Jar != nil {
				cookies = account.Jar.GetCookieHeader(false)
			}
			userAgent = account.UserAgent
		}

		userPrompt := ""
		if len(req.Messages) > 0 {
			userPrompt = req.Messages[len(req.Messages)-1].Content
		}

		var cachedURLs []string
		for _, rawURL := range reply.MediaURLs {
			cachedAsset, cacheErr := s.storage.DownloadAndCacheWithAuth(
				ctx,
				rawURL,
				domain.MediaImagePNG,
				userPrompt,
				req.Model,
				cookies,
				userAgent,
			)
			if cacheErr == nil && cachedAsset != nil && cachedAsset.LocalURL != "" {
				cachedURLs = append(cachedURLs, cachedAsset.LocalURL)
				// Thay thế URL gốc bằng LocalURL trong văn bản phản hồi Markdown
				reply.Text = strings.ReplaceAll(reply.Text, rawURL, cachedAsset.LocalURL)
			} else {
				cachedURLs = append(cachedURLs, rawURL)
			}
		}
		reply.MediaURLs = cachedURLs
	}

	var usage *domain.OpenAIUsage
	if s.tokenCounter != nil {
		usage = s.tokenCounter.CalculateUsage(req.Messages, reply)
	}

	if s.keyUseCase != nil && usage != nil {
		keyID := ""
		if vKey := domain.VirtualKeyFromContext(ctx); vKey != nil {
			keyID = vKey.ID
		} else if bill, ok := domain.BillingIdentityFromContext(ctx); ok && bill.KeyID != "" {
			keyID = bill.KeyID
		}
		if keyID != "" {
			_ = s.keyUseCase.RecordTokenUsage(ctx, keyID, usage.PromptTokens, usage.CompletionTokens)
		}
	}

	var cleanText = reply.Text
	var toolCalls []domain.OpenAIToolCall
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		cText, rawCalls := ExtractToolCallsWithAllowed(reply.Text, req.Tools)
		cleanText = cText
		if len(rawCalls) > 0 || req.ToolChoice != nil {
			normCalls, err := ValidateAndNormalizeToolCalls(rawCalls, req.Tools, req.ToolChoice)
			if err != nil {
				// Nếu tool_choice không ép buộc (nil hoặc auto), fallback về plain text để không làm hỏng request
				isStrict := false
				if tc, ok := req.ToolChoice.(string); ok {
					s := strings.ToLower(strings.TrimSpace(tc))
					if s == "required" || s == "function" {
						isStrict = true
					}
				} else if req.ToolChoice != nil {
					isStrict = true
				}
				if isStrict {
					return nil, err
				}
				log.Printf("[ChatService] Cảnh báo gọi tool không hợp lệ: %v. Fallback sang plain text.", err)
				cleanText = reply.Text
				toolCalls = nil
			} else {
				toolCalls = normCalls
			}
		}
	}

	var reasoningText string
	if len(reply.ThinkingBlocks) > 0 {
		var rParts []string
		for _, tb := range reply.ThinkingBlocks {
			if strings.TrimSpace(tb.Content) != "" {
				rParts = append(rParts, tb.Content)
			}
		}
		reasoningText = strings.Join(rParts, "\n\n")
	}

	stopReason := "stop"
	if len(toolCalls) > 0 {
		stopReason = "tool_calls"
	} else if maxTokens := req.EffectiveMaxTokens(); maxTokens != nil && *maxTokens > 0 && usage != nil && usage.CompletionTokens >= *maxTokens {
		stopReason = "length"
	}

	choices := []domain.OpenAIChoice{
		{
			Index: 0,
			Message: domain.OpenAIMessage{
				Role:             "assistant",
				Content:          cleanText,
				ReasoningContent: reasoningText,
				ToolCalls:        toolCalls,
			},
			FinishReason: &stopReason,
		},
	}
	if len(reply.Drafts) > 1 {
		for i := 1; i < len(reply.Drafts); i++ {
			draftContent := reply.Drafts[i]
			if strings.TrimSpace(draftContent) != "" && draftContent != reply.Text {
				dClean := draftContent
				var dCalls []domain.OpenAIToolCall
				if len(req.Tools) > 0 || req.ToolChoice != nil {
					dc, dRawCalls := ExtractToolCallsWithAllowed(draftContent, req.Tools)
					dClean = dc
					if len(dRawCalls) > 0 || req.ToolChoice != nil {
						if norm, err := ValidateAndNormalizeToolCalls(dRawCalls, req.Tools, req.ToolChoice); err == nil {
							dCalls = norm
						}
					}
				}
				dStopReason := "stop"
				if len(dCalls) > 0 {
					dStopReason = "tool_calls"
				}
				choices = append(choices, domain.OpenAIChoice{
					Index: len(choices),
					Message: domain.OpenAIMessage{
						Role:      "assistant",
						Content:   dClean,
						ToolCalls: dCalls,
					},
					FinishReason: &dStopReason,
				})
			}
		}
	}

	return &domain.OpenAIChatResponse{
		ID:             "chatcmpl-" + reply.ConversationID,
		Object:         "chat.completion",
		Created:        time.Now().Unix(),
		Model:          req.Model,
		ConversationID: reply.ConversationID,
		ResponseID:     reply.ResponseID,
		ChoiceID:       reply.ChoiceID,
		Thinking:       reply.ThinkingBlocks,
		Grounding:      reply.Grounding,
		CodeExecutions: reply.CodeExecutions,
		MediaURLs:      reply.MediaURLs,
		Usage:          usage,
		Choices:        choices,
	}, nil
}

func (s *ChatService) postGemini(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
	req *domain.OpenAIChatRequest,
) (*http.Response, error) {
	if err := s.ensureAccountMode(ctx, account, modelDesc); err != nil {
		return nil, err
	}

	hasRemoteHistory := req.ConversationID != ""
	_, lastUserPrompt := domain.FlattenMessagesForModelWithContext(req.Messages, req.Model, hasRemoteHistory)
	if len(req.Tools) > 0 {
		toolPrompt := domain.CompileToolsInstructionWithChoice(req.Tools, req.ToolChoice)
		if toolPrompt != "" {
			lastUserPrompt = toolPrompt + "\n\n" + lastUserPrompt
		}
	}

	isThinking := s.chatDefaults.DefaultThinking()
	if req.Thinking != nil {
		isThinking = *req.Thinking
	} else if strings.Contains(strings.ToLower(req.Model), "no-thinking") {
		isThinking = false
	} else if strings.Contains(strings.ToLower(req.Model), "-thinking") {
		isThinking = true
	}

	// Xử lý tham số chuẩn OpenAI reasoning_effort và thinking_budget / budget_tokens
	effort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort))
	var reasoningInstruction string
	switch effort {
	case "none":
		isThinking = false
	case "low":
		isThinking = true
		reasoningInstruction = "[Reasoning Effort: low. Provide a concise, fast, and direct internal reasoning process.]"
	case "medium":
		isThinking = true
		reasoningInstruction = "[Reasoning Effort: medium. Provide a structured and balanced step-by-step reasoning process.]"
	case "high":
		isThinking = true
		reasoningInstruction = "[Reasoning Effort: high. Provide deep, comprehensive, rigorous step-by-step reasoning and self-verification.]"
	}

	budget := 0
	if req.ThinkingBudget != nil {
		budget = *req.ThinkingBudget
	} else if req.BudgetTokens != nil {
		budget = *req.BudgetTokens
	}

	if budget == 0 && (req.ThinkingBudget != nil || req.BudgetTokens != nil) {
		isThinking = false
	} else if budget > 0 {
		isThinking = true
		reasoningInstruction = fmt.Sprintf("[Thinking Budget: ~%d tokens. Adapt internal reasoning depth and verbosity to fit within this budget.]", budget)
	}

	if reasoningInstruction != "" && isThinking {
		lastUserPrompt = reasoningInstruction + "\n\n" + lastUserPrompt
	}
	isGrounding := s.chatDefaults.DefaultSearchGrounding()
	if req.SearchGrounding != nil {
		isGrounding = *req.SearchGrounding
	}
	isCodeInterpreter := s.chatDefaults.DefaultCodeInterpreter()
	if req.CodeInterpreter != nil {
		isCodeInterpreter = *req.CodeInterpreter
	}

	modelTier := modelDesc.ModelTierCode
	if isThinking && modelTier < 3 && strings.Contains(strings.ToLower(req.Model), "pro") {
		modelTier = 3
	}

	attachments := req.Attachments
	if s.visionResolver != nil {
		resolvedAtts, err := s.visionResolver.ProcessRequestAttachments(ctx, account, req)
		if err != nil {
			return nil, err
		}
		attachments = resolvedAtts
	}

	builder := domain.GeminiPayloadBuilder{
		UserPrompt:            lastUserPrompt,
		Locale:                "en",
		ConversationID:        req.ConversationID,
		ResponseID:            req.ResponseID,
		ChoiceID:              req.ChoiceID,
		ParentResponseID:      req.ParentResponseID,
		ParentChoiceID:        req.ParentChoiceID,
		ContextBlob:           req.ContextBlob,
		ModelTier:             modelTier,
		EnableThinking:        isThinking,
		EnableSearchGrounding: isGrounding,
		EnableCodeExecution:   isCodeInterpreter,
		Attachments:           attachments,
		ClientUUID:            fmt.Sprintf("req_%d", time.Now().UnixNano()),
		MaxOutputTokens:       req.EffectiveMaxTokens(),
	}

	if s.wire == nil {
		return nil, domain.CodecRejected(domain.OriginStreamGenerate, domain.ServiceGemini, "không đóng gói được yêu cầu")
	}
	attempt, err := s.wire.MaterializeChat(account, builder)
	if err != nil {
		return nil, err
	}
	reqPath := attempt.Path
	if attempt.TargetHost != "" && !strings.HasPrefix(reqPath, "http") {
		reqPath = attempt.TargetHost + reqPath
	}
	resp, err := s.upstream.DoRequest(
		ctx,
		account,
		modelDesc.TargetService,
		http.MethodPost,
		reqPath,
		strings.NewReader(attempt.Body),
		attempt.ContentType,
	)
	if err != nil {
		return nil, domain.CodecTransport(domain.OriginStreamGenerate, modelDesc.TargetService, err)
	}
	return resp, nil
}

func (s *ChatService) ensureAccountMode(
	ctx context.Context,
	account *domain.ManagedAccount,
	modelDesc *domain.ModelDescriptor,
) error {
	if account == nil || modelDesc == nil || modelDesc.ModeID == "" {
		return nil
	}
	if account.GetActiveModeID() == modelDesc.ModeID {
		return nil
	}

	reqPayload, err := domain.BuildModeSwitchRequest(modelDesc.ModeID)
	if err != nil {
		return err
	}

	path := "/_/BardChatUi/data/batchexecute?rpcids=L5adhe&source-path=%2Fapp&rt=c"
	if s.rpcRegistry != nil {
		if ep, ok := s.rpcRegistry.Get("L5adhe"); ok {
			if ep.PathPattern != "" {
				path = ep.PathPattern
				if !strings.Contains(path, "rt=c") {
					if strings.Contains(path, "?") {
						path += "&rt=c"
					} else {
						path += "?rt=c"
					}
				}
			}
			if ep.TargetHost != "" && !strings.HasPrefix(path, "http") {
				path = ep.TargetHost + path
			}
		}
	}

	postBody := "f.req=" + url.QueryEscape(reqPayload)
	if at := account.GetAtToken(domain.ServiceGemini); at != "" {
		postBody += "&at=" + url.QueryEscape(at)
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if s.upstream == nil {
		return nil
	}
	resp, err := s.upstream.DoRequest(
		callCtx,
		account,
		domain.ServiceGemini,
		http.MethodPost,
		path,
		strings.NewReader(postBody),
		"application/x-www-form-urlencoded;charset=UTF-8",
	)
	if err != nil {
		return domain.ClassifyTransport(domain.OpChatCompletions, "L5adhe", domain.ServiceGemini, err)
	}
	if resp != nil {
		if resp.Body != nil {
			defer resp.Body.Close()
		}
		if resp.StatusCode >= 400 {
			if ge := domain.ClassifyUpstreamStatus(domain.OpChatCompletions, "L5adhe", resp.StatusCode, false, domain.ServiceGemini); ge != nil {
				return ge
			}
			return domain.UpstreamRejected(domain.OpChatCompletions, "L5adhe", domain.ServiceGemini, "chuyển model mode không thành công")
		}
		if resp.Body != nil {
			bodyBytes, _ := io.ReadAll(resp.Body)
			if ok, parseErr := domain.ParseModeSwitchResponse(string(bodyBytes)); ok && parseErr == nil {
				account.SetActiveModeID(modelDesc.ModeID)
			}
		}
	}
	return nil
}

func boundStream(upstream ports.UpstreamGoogleTransport, ctx context.Context) (context.Context, context.CancelFunc) {
	if upstream == nil {
		return ctx, func() {}
	}
	return upstream.BoundStream(ctx)
}

func isNetworkOrTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "refused") ||
		strings.Contains(errStr, "use of closed") ||
		strings.Contains(errStr, "stream error") ||
		strings.Contains(errStr, "handshake") ||
		strings.Contains(errStr, "tls") ||
		strings.Contains(errStr, "unexpected eof") ||
		strings.Contains(errStr, "eof")
}

func normalizeWireErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if ge, ok := domain.AsGatewayError(err); ok {
		return ge
	}
	if ce, ok := domain.AsCodecError(err); ok {
		return ce
	}

	// 1. Lỗi ngắt kết nối mạng / Timeout -> Trả về ClassUpstreamUnavailable với Retryable = true để kích hoạt Failover
	if isNetworkOrTimeoutErr(err) {
		return domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, err)
	}

	// 2. Chỉ trả về ClassSchemaUnexpected khi nhận được HTTP 200 từ Google nhưng nội dung JSON bị rỗng hoàn toàn
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "empty") || strings.Contains(errStr, "rỗng") {
		return domain.CodecSchema(domain.OriginStreamGenerate, domain.ServiceGemini, "nhận được HTTP 200 từ Google nhưng nội dung JSON bị rỗng hoàn toàn")
	}

	// Mặc định đối với lỗi đọc wire / I/O không xác định, coi là lỗi upstream transport để kích hoạt Failover
	return domain.CodecTransport(domain.OriginStreamGenerate, domain.ServiceGemini, err)
}

// NormalizeWireErr chuẩn hóa lỗi từ wire codec để hỗ trợ kiểm thử và phân loại lỗi
func NormalizeWireErr(err error) error {
	return normalizeWireErr(err)
}
