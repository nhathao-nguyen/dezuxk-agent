package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dezuxk-gateway/internal/adapters/outbound/gateway_client"
	"dezuxk-gateway/internal/adapters/outbound/session"
	"dezuxk-gateway/internal/adapters/outbound/tools"
	"dezuxk-gateway/internal/config"
	"dezuxk-gateway/internal/core/domain"
	"dezuxk-gateway/internal/core/services/agent"
)

// ANSI Color Codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// ConsoleApprovalProvider yêu cầu người dùng xác nhận trực tiếp trên Terminal
type ConsoleApprovalProvider struct {
	reader *bufio.Reader
}

func NewConsoleApprovalProvider() *ConsoleApprovalProvider {
	return &ConsoleApprovalProvider{
		reader: bufio.NewReader(os.Stdin),
	}
}

func (p *ConsoleApprovalProvider) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (bool, error) {
	fmt.Printf("\n%s%s⚠️  [BẢO MẬT - YÊU CẦU XÁC NHẬN HÀNH ĐỘNG] %s\n", colorBold, colorYellow, colorReset)
	fmt.Printf("  • Công cụ     : %s%s%s\n", colorBold, req.ToolName, colorReset)
	fmt.Printf("  • Mô tả       : %s\n", req.Description)
	fmt.Printf("  • Tham số gọi : %s%s%s\n", colorCyan, req.Arguments, colorReset)
	fmt.Printf("%sBạn có cho phép Agent thực thi hành động này không? [Y/n]: %s", colorBold, colorReset)

	line, err := p.reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" || answer == "y" || answer == "yes" {
		fmt.Printf("%s✓ Đã cấp quyền thực thi.%s\n\n", colorGreen, colorReset)
		return true, nil
	}

	fmt.Printf("%s✗ Đã từ chối cấp quyền.%s\n\n", colorRed, colorReset)
	return false, nil
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Đường dẫn file cấu hình YAML")
	workflow := flag.String("workflow", "graph", "Kiểu luồng thực thi: graph (State Machine PLAN-EXEC-VERIFY-FIX) | react (vòng lặp phẳng)")
	resumeTaskID := flag.String("resume", "", "Task ID cần phục hồi từ Checkpoint")
	model := flag.String("model", "gemini-3.8-flash", "Mô hình Gemini (gemini-3.8-flash | gemini-3.1-pro)")
	workspace := flag.String("workspace", ".", "Thư mục làm việc của Agent")
	supervised := flag.Bool("supervised", true, "Chế độ bán tự trị: hỏi xác nhận trước khi sửa file lớn hoặc chạy lệnh shell")
	maxSteps := flag.Int("max-steps", 25, "Số bước lặp tối đa cho một nhiệm vụ")
	cdpPort := flag.Int("cdp-port", 9222, "Cổng Chrome Remote Debugging cho Browser Tools (mặc định: 9222)")
	goalFlag := flag.String("goal", "", "Nhiệm vụ cần thực hiện một lần (One-shot mode)")
	gatewayURL := flag.String("gateway", "", "Địa chỉ Gateway URL (để trống sẽ nạp từ file cấu hình)")

	flag.Parse()

	absWorkspace, err := filepath.Abs(*workspace)
	if err != nil {
		absWorkspace = *workspace
	}

	// 1. Nạp cấu hình Gateway
	cfg, err := config.LoadConfig(*configPath)
	targetURL := *gatewayURL
	apiKey := ""

	if err == nil && cfg != nil {
		if targetURL == "" {
			targetURL = fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Server.Port)
		}
		apiKey = cfg.Server.APIKey
	} else if targetURL == "" {
		targetURL = "http://127.0.0.1:8080/v1"
	}

	// 2. Khởi tạo Outbound Adapters
	chatClient := gateway_client.NewHTTPChatAdapter(targetURL, apiKey, 300*time.Second)
	toolReg := tools.NewToolRegistry()
	tools.RegisterDefaultTools(toolReg, absWorkspace)
	approvalProvider := NewConsoleApprovalProvider()
	checkpointRepo := session.NewMemoryCheckpointRepository()

	// 3. Khởi tạo 3-Tier Memory Engine
	memRepo := session.NewMemoryMemoryRepository()
	initialCore := domain.CoreMemory{
		Persona:        "Dezuxk Autonomous Engineering Agent (Tự trị • Kiểm chứng • Chuẩn chỉ)",
		ProjectContext: "Hexagonal Clean Architecture, Zero Hardcoding, SQLite WAL persistence",
	}
	memoryService := agent.NewMemoryManager(memRepo, chatClient, *model, initialCore)
	tools.RegisterMemoryTools(toolReg, memoryService)

	// 4. Khởi tạo Chrome CDP Browser Tools & Sub-agents Supervisor
	tools.RegisterBrowserTools(toolReg, *cdpPort, filepath.Join(absWorkspace, ".dezuxk", "screenshots"))
	subagentSupervisor := agent.NewSubagentSupervisor(chatClient, toolReg, approvalProvider, memoryService)
	tools.RegisterSubagentTool(toolReg, subagentSupervisor)

	// 5. Khởi tạo Agent Engines
	reactRunner := agent.NewRunner(chatClient, toolReg, approvalProvider)
	reactRunner.SetMemoryService(memoryService)
	graphEngine := agent.NewGraphEngine(chatClient, toolReg, checkpointRepo, approvalProvider, 3)

	// Hiển thị Banner
	fmt.Printf("%s%s========================================================================%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s%s   DEZUXK AUTONOMOUS AGENT CLI (LangGraph • Letta Memory • Chuẩn chỉ) %s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s%s========================================================================%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("• %sGateway Endpoint%s: %s\n", colorBold, colorReset, targetURL)
	fmt.Printf("• %sQuy trình Workflow%s: %s%s%s (PLAN ➔ EXECUTE ➔ VERIFY ➔ FIX ➔ COMPLETE)\n", colorBold, colorReset, colorGreen, *workflow, colorReset)
	fmt.Printf("• %sHệ thống bộ nhớ %s: 3-Tier Memory (Working • Recall Auto-Compacted • Archival Hybrid)\n", colorBold, colorReset)
	fmt.Printf("• %sSub-agents      %s: Sẵn sàng (researcher, coder, reviewer, general)\n", colorBold, colorReset)
	fmt.Printf("• %sBrowser Tool CDP%s: Sẵn sàng (Port %d)\n", colorBold, colorReset, *cdpPort)
	fmt.Printf("• %sMô hình mặc định%s: %s%s%s\n", colorBold, colorReset, colorGreen, *model, colorReset)
	fmt.Printf("• %sThư mục làm việc%s: %s\n", colorBold, colorReset, absWorkspace)
	fmt.Printf("• %sChế độ an toàn  %s: Supervised = %v (Hỏi trước khi chạy lệnh phá hủy)\n", colorBold, colorReset, *supervised)
	fmt.Printf("• %sCheckpoint Repo %s: In-Memory / SQLite (WAL Enabled)\n", colorBold, colorReset)
	fmt.Printf("------------------------------------------------------------------------\n\n")

	// Bắt tín hiệu Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Printf("\n%s[Ngắt bởi người dùng (SIGINT)] Đang dừng Agent...%s\n", colorRed, colorReset)
		cancel()
	}()

	// 4. Nếu có yêu cầu Resume từ Task ID
	if *resumeTaskID != "" {
		executeGraphTask(ctx, graphEngine, "", *model, absWorkspace, *supervised, *maxSteps, *resumeTaskID)
		return
	}

	// 5. Nếu có truyền cờ -goal hoặc tham số dòng lệnh sau flags
	initialGoal := *goalFlag
	if initialGoal == "" && len(flag.Args()) > 0 {
		initialGoal = strings.Join(flag.Args(), " ")
	}

	if initialGoal != "" {
		if *workflow == "graph" {
			executeGraphTask(ctx, graphEngine, initialGoal, *model, absWorkspace, *supervised, *maxSteps, "")
		} else {
			executeReactTask(ctx, reactRunner, initialGoal, *model, absWorkspace, *supervised, *maxSteps)
		}
		return
	}

	// 6. Chế độ REPL Interactive
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s%sdezuxk-agent (%s)>%s ", colorBold, colorPurple, *workflow, colorReset)
		input, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		task := strings.TrimSpace(input)
		if task == "" {
			continue
		}
		if task == "exit" || task == "quit" || task == ":q" {
			fmt.Println("Tạm biệt!")
			break
		}

		if *workflow == "graph" {
			executeGraphTask(ctx, graphEngine, task, *model, absWorkspace, *supervised, *maxSteps, "")
		} else {
			executeReactTask(ctx, reactRunner, task, *model, absWorkspace, *supervised, *maxSteps)
		}
	}
}

func executeGraphTask(
	ctx context.Context,
	engine *agent.GraphEngine,
	goal string,
	model string,
	workspace string,
	supervised bool,
	maxSteps int,
	resumeTaskID string,
) {
	if resumeTaskID != "" {
		fmt.Printf("\n%s🔄 [PHỤC HỒI WORKFLOW]: Task ID %s%s\n\n", colorBold, resumeTaskID, colorReset)
	} else {
		fmt.Printf("\n%s🚀 [BẮT ĐẦU STATE MACHINE WORKFLOW]: %s%s%s\n\n", colorBold, colorGreen, goal, colorReset)
	}

	onProgress := func(step int, kind string, message string) {
		switch kind {
		case "node_plan":
			fmt.Printf("%s%s📋 %s%s\n", colorBold, colorCyan, message, colorReset)
		case "plan_created":
			fmt.Printf("%s  ✓ %s%s\n", colorGreen, message, colorReset)
		case "node_execute":
			fmt.Printf("\n%s%s⚙️  %s%s\n", colorBold, colorBlue, message, colorReset)
		case "node_verify":
			fmt.Printf("\n%s%s🔍 %s%s\n", colorBold, colorPurple, message, colorReset)
		case "verify_exec":
			fmt.Printf("%s  %s%s\n", colorCyan, message, colorReset)
		case "verify_pass":
			fmt.Printf("%s%s%s%s\n", colorBold, colorGreen, message, colorReset)
		case "verify_fail":
			fmt.Printf("%s%s%s%s\n", colorBold, colorRed, message, colorReset)
		case "node_fix":
			fmt.Printf("\n%s%s🛠️  %s%s\n", colorBold, colorYellow, message, colorReset)
		case "node_complete":
			fmt.Printf("\n%s%s🏆 %s%s\n", colorBold, colorGreen, message, colorReset)
		case "thinking":
			fmt.Printf("%s[Suy nghĩ] %s%s\n", colorCyan, message, colorReset)
		case "tool_start":
			fmt.Printf("%s  ⚡ %s%s\n", colorYellow, message, colorReset)
		case "tool_end":
			fmt.Printf("%s  ✓ %s%s\n", colorGreen, message, colorReset)
		}
	}

	opts := domain.AgentRunOptions{
		Model:      model,
		MaxSteps:   maxSteps,
		Supervised: supervised,
		Workspace:  workspace,
		OnProgress: onProgress,
	}

	startTime := time.Now()
	var state *domain.AgentGraphState
	var err error

	if resumeTaskID != "" {
		state, err = engine.ResumeGraph(ctx, resumeTaskID, opts)
	} else {
		state, err = engine.RunGraph(ctx, goal, opts)
	}
	elapsed := time.Since(startTime)

	if err != nil {
		fmt.Printf("\n%s%s❌ [LỖI WORKFLOW]: %v (Thời gian: %v)%s\n\n", colorBold, colorRed, err, elapsed.Round(time.Millisecond), colorReset)
		return
	}

	fmt.Printf("\n%s%s========================================================================%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s   BÁO CÁO KẾT QUẢ ĐÃ XÁC MINH (%d Checkpoints, %v)   %s\n", colorBold, state.Checkpoints, elapsed.Round(time.Millisecond), colorReset)
	fmt.Printf("%s%s========================================================================%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s\n\n", state.FinalSummary)
}

func executeReactTask(
	ctx context.Context,
	runner *agent.Runner,
	goal string,
	model string,
	workspace string,
	supervised bool,
	maxSteps int,
) {
	fmt.Printf("\n%s🚀 [BẮT ĐẦU REACT TASK]: %s%s%s\n\n", colorBold, colorGreen, goal, colorReset)

	onProgress := func(step int, kind string, message string) {
		switch kind {
		case "thinking":
			fmt.Printf("%s[Bước %d] 💭 %s%s\n", colorCyan, step, message, colorReset)
		case "tool_start":
			fmt.Printf("%s  ⚡ %s%s\n", colorYellow, message, colorReset)
		case "tool_end":
			fmt.Printf("%s  ✓ %s%s\n", colorGreen, message, colorReset)
		case "completed":
			fmt.Printf("\n%s🎉 %s%s\n", colorBold, message, colorReset)
		}
	}

	opts := domain.AgentRunOptions{
		Model:      model,
		MaxSteps:   maxSteps,
		Supervised: supervised,
		Workspace:  workspace,
		OnProgress: onProgress,
	}

	startTime := time.Now()
	state, err := runner.Run(ctx, goal, opts)
	elapsed := time.Since(startTime)

	if err != nil {
		fmt.Printf("\n%s%s❌ [LỖI THỰC THI]: %v (Thời gian: %v)%s\n\n", colorBold, colorRed, err, elapsed.Round(time.Millisecond), colorReset)
		return
	}

	fmt.Printf("\n%s%s========================================================================%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s   KẾT QUẢ CUỐI CÙNG (%d bước, %v)   %s\n", colorBold, len(state.Steps), elapsed.Round(time.Millisecond), colorReset)
	fmt.Printf("%s%s========================================================================%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s\n\n", state.FinalAnswer)
}
