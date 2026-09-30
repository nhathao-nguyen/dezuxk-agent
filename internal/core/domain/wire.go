package domain

// OutboundAttempt là thân đã pack, sẵn sàng ghi. Không dùng làm log.
type OutboundAttempt struct {
	TargetHost  string
	Path        string
	Body        string
	ContentType string
}

// GeminiReply là kết quả unpack của StreamGenerate, chưa phải hợp đồng caller.
type GeminiReply struct {
	Text           string
	ConversationID string
	ResponseID     string
	ChoiceID       string
	ThinkingBlocks []ThoughtBlock
	Grounding      *GroundingMetadata
	CodeExecutions []CodeExecution
	MediaURLs      []string
	Unmapped       int
	Drafts         []string
}
