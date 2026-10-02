package domain

import (
	"context"
)

type workspaceContextKey struct{}
type liveOutputContextKey struct{}

// LiveOutputFunc định nghĩa hàm callback nhận dữ liệu đầu ra thời gian thực từ shell / tool
type LiveOutputFunc func(streamType string, chunk string)

// WithWorkspace gắn đường dẫn workspace vào context
func WithWorkspace(ctx context.Context, workspace string) context.Context {
	if workspace == "" {
		return ctx
	}
	return context.WithValue(ctx, workspaceContextKey{}, workspace)
}

// GetWorkspace trích xuất đường dẫn workspace từ context, nếu không có trả về fallback
func GetWorkspace(ctx context.Context, fallback string) string {
	if ctx == nil {
		return fallback
	}
	if ws, ok := ctx.Value(workspaceContextKey{}).(string); ok && ws != "" {
		return ws
	}
	return fallback
}

// WithLiveOutput gắn callback xuất dữ liệu thời gian thực vào context
func WithLiveOutput(ctx context.Context, fn LiveOutputFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, liveOutputContextKey{}, fn)
}

// GetLiveOutput lấy callback xuất dữ liệu thời gian thực từ context
func GetLiveOutput(ctx context.Context) LiveOutputFunc {
	if ctx == nil {
		return nil
	}
	if fn, ok := ctx.Value(liveOutputContextKey{}).(LiveOutputFunc); ok {
		return fn
	}
	return nil
}
