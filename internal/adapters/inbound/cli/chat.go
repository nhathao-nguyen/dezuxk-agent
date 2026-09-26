package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dezuxk-gateway/internal/core/domain"

	"github.com/spf13/cobra"
)

// NewChatCmd creates the "chat" command.
func NewChatCmd() *cobra.Command {
	var (
		prompt string
		model  string
		stream bool
		system string
		image  string
		temp   float64
	)

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Interact directly with AI models via prompt or stdin",
		RunE: func(cmd *cobra.Command, args []string) error {
			execCtx, err := GetExecutionContext(cmd)
			if err != nil {
				return err
			}
			defer execCtx.Close()

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Read prompt from stdin if not provided via --prompt / -p
			prompt = strings.TrimSpace(prompt)
			if prompt == "" {
				inBytes, err := io.ReadAll(cmd.InOrStdin())
				if err == nil {
					prompt = strings.TrimSpace(string(inBytes))
				}
			}

			if prompt == "" {
				return fmt.Errorf("prompt is required (use --prompt/-p or pipe via stdin)")
			}

			if model == "" {
				model = "gemini-3.1-pro"
			}

			// Build messages
			var messages []domain.OpenAIMessage
			if system != "" {
				messages = append(messages, domain.OpenAIMessage{
					Role:    "system",
					Content: system,
				})
			}

			userMsg := domain.OpenAIMessage{
				Role:    "user",
				Content: prompt,
			}
			if image != "" {
				userMsg.ContentParts = []domain.MessageContentPart{
					{Type: "text", Text: prompt},
					{Type: "image_url", ImageURL: &domain.MessageImageURL{URL: image}},
				}
			}
			messages = append(messages, userMsg)

			req := domain.OpenAIChatRequest{
				Model:       model,
				Messages:    messages,
				Stream:      stream,
				Temperature: temp,
			}

			if execCtx.IsOnline {
				if stream {
					err := execCtx.Client.StreamChat(ctx, req, func(token string) error {
						_, err := fmt.Fprint(cmd.OutOrStdout(), token)
						return err
					})
					if err != nil {
						return fmt.Errorf("stream chat failed: %w", err)
					}
					fmt.Fprintln(cmd.OutOrStdout())
					return nil
				}

				resp, err := execCtx.Client.SendChat(ctx, req)
				if err != nil {
					return fmt.Errorf("chat failed: %w", err)
				}

				if strings.EqualFold(execCtx.Format, "json") {
					return RenderJSON(cmd.OutOrStdout(), resp)
				}

				if len(resp.Choices) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), resp.Choices[0].Message.Content)
				}
				return nil
			}

			// Direct offline execution
			if execCtx.Direct.ChatService == nil {
				return fmt.Errorf("chat service is not available in direct mode")
			}

			if stream {
				err := execCtx.Direct.ChatService.ExecuteChatStream(ctx, &req, cmd.OutOrStdout(), func() {})
				if err != nil {
					return fmt.Errorf("offline stream chat failed: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout())
				return nil
			}

			resp, err := execCtx.Direct.ChatService.ExecuteChatSync(ctx, &req)
			if err != nil {
				return fmt.Errorf("offline chat failed: %w", err)
			}

			if strings.EqualFold(execCtx.Format, "json") {
				return RenderJSON(cmd.OutOrStdout(), resp)
			}

			if len(resp.Choices) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), resp.Choices[0].Message.Content)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "input prompt text for the AI model")
	cmd.Flags().StringVarP(&model, "model", "m", "gemini-3.1-pro", "AI model identifier")
	cmd.Flags().BoolVar(&stream, "stream", true, "stream tokens in real-time")
	cmd.Flags().StringVar(&system, "system", "", "system instruction for the prompt")
	cmd.Flags().StringVar(&image, "image", "", "path or URL of image attachment")
	cmd.Flags().Float64Var(&temp, "temp", 0.7, "sampling temperature (0.0 to 2.0)")

	return cmd
}
