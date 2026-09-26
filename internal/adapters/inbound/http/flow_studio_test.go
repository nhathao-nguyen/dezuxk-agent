package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dezuxk-gateway/internal/core/domain"
	"github.com/go-chi/chi/v5"
)

type fakeFlowUseCase struct {
	extendResult   *domain.VideoGenerationResult
	upsampleResult *domain.Upsample4KResponse
	musicResult    *domain.FlowMusicResponse
	voices         []domain.VoicePersona
	projects       []domain.FlowProject
	trash          []domain.FlowTrashProject
	gallery        []*domain.MediaAsset
	err            error
}

func (f *fakeFlowUseCase) GenerateImage(ctx context.Context, req *domain.ImageGenerationRequest) (*domain.ImageGenerationResult, error) {
	return &domain.ImageGenerationResult{URL: "http://localhost:8080/media/test_img.png", MimeType: "image/png"}, f.err
}

func (f *fakeFlowUseCase) GenerateVideo(ctx context.Context, req *domain.VideoGenerationRequest) (*domain.VideoGenerationResult, error) {
	return &domain.VideoGenerationResult{URL: "http://localhost:8080/media/test_vid.mp4", MimeType: "video/mp4"}, f.err
}

func (f *fakeFlowUseCase) ExtendVideo(ctx context.Context, req *domain.VideoExtensionRequest) (*domain.VideoGenerationResult, error) {
	if f.extendResult != nil {
		return f.extendResult, f.err
	}
	return &domain.VideoGenerationResult{URL: "http://localhost:8080/media/test_ext.mp4", MimeType: "video/mp4"}, f.err
}

func (f *fakeFlowUseCase) UpsampleVideo4K(ctx context.Context, req *domain.Upsample4KRequest) (*domain.Upsample4KResponse, error) {
	if f.upsampleResult != nil {
		return f.upsampleResult, f.err
	}
	return &domain.Upsample4KResponse{OutputURL: "http://localhost:8080/media/test_4k.mp4", Status: "COMPLETED"}, f.err
}

func (f *fakeFlowUseCase) GenerateAudio(ctx context.Context, req domain.FlowMusicRequest) (*domain.FlowMusicResponse, error) {
	if f.musicResult != nil {
		return f.musicResult, f.err
	}
	return &domain.FlowMusicResponse{URL: "http://localhost:8080/media/test_music.mp3", DurationSeconds: 15}, f.err
}

func (f *fakeFlowUseCase) ListVoicePersonas(ctx context.Context) ([]domain.VoicePersona, error) {
	if f.voices != nil {
		return f.voices, f.err
	}
	return []domain.VoicePersona{
		{ID: "char_01", Name: "Kaelen", Gender: "Nam", SampleURL: "https://ssl.gstatic.com/sample1.wav"},
	}, f.err
}

func (f *fakeFlowUseCase) ListProjects(ctx context.Context) ([]domain.FlowProject, error) {
	if f.projects != nil {
		return f.projects, f.err
	}
	return []domain.FlowProject{
		{ID: "proj_01", Title: "Epic Cinematic Project"},
	}, f.err
}

func (f *fakeFlowUseCase) CreateProject(ctx context.Context, title string) (string, error) {
	return "proj_new_123", f.err
}

func (f *fakeFlowUseCase) MoveProjectToTrash(ctx context.Context, projectID string) error {
	return f.err
}

func (f *fakeFlowUseCase) ListTrash(ctx context.Context) ([]domain.FlowTrashProject, error) {
	if f.trash != nil {
		return f.trash, f.err
	}
	return []domain.FlowTrashProject{
		{ID: "proj_trash_01", Title: "Old Deleted Project"},
	}, f.err
}

func (f *fakeFlowUseCase) RestoreProject(ctx context.Context, projectID string) error {
	return f.err
}

func (f *fakeFlowUseCase) DeleteProjectPermanently(ctx context.Context, projectID string) error {
	return f.err
}

func (f *fakeFlowUseCase) ListMediaGallery(ctx context.Context, kind domain.MediaKind) ([]*domain.MediaAsset, error) {
	if f.gallery != nil {
		return f.gallery, f.err
	}
	return []*domain.MediaAsset{
		{ID: "asset_01", FileName: "test.mp4", Kind: domain.MediaVideoMP4, LocalURL: "http://localhost:8080/media/test.mp4"},
	}, f.err
}

func TestFlowStudio_FacadeRoutes(t *testing.T) {
	useCase := &fakeFlowUseCase{}
	studioHandler := NewFlowStudioHandler(useCase, domain.NewContractMetrics())
	router := chi.NewRouter()
	router.Route("/v1", func(v1 chi.Router) {
		v1.Post("/flow/videos/extend", studioHandler.HandleExtendVideo)
		v1.Post("/flow/videos/upsample-4k", studioHandler.HandleUpsample4K)
		v1.Post("/flow/audio/generate", studioHandler.HandleGenerateAudio)
		v1.Get("/flow/voices", studioHandler.HandleListVoices)
		v1.Get("/flow/projects", studioHandler.HandleListProjects)
		v1.Post("/flow/projects", studioHandler.HandleCreateProject)
		v1.Get("/flow/gallery", studioHandler.HandleListMediaGallery)
	})

	// 1. Extend Video
	extendBody, _ := json.Marshal(domain.VideoExtensionRequest{
		SourceVideoAssetID: "test_asset_01",
		Prompt:             "camera tiếp tục zoom",
		ExtensionDuration:  4,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/flow/videos/extend", bytes.NewReader(extendBody))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for extend video, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Upsample 4K
	upsampleBody, _ := json.Marshal(domain.Upsample4KRequest{
		VideoAssetID: "test_asset_01",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/flow/videos/upsample-4k", bytes.NewReader(upsampleBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for upsample-4k, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Generate Audio
	musicBody, _ := json.Marshal(domain.FlowMusicRequest{
		Prompt:                "Synthwave cyberpunk",
		Genre:                 "CINEMATIC",
		Mood:                  "DRAMATIC",
		TempoBPM:              120,
		TargetDurationSeconds: 15,
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/flow/audio/generate", bytes.NewReader(musicBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for audio generate, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Voices
	req = httptest.NewRequest(http.MethodGet, "/v1/flow/voices", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for voices, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Projects
	req = httptest.NewRequest(http.MethodGet, "/v1/flow/projects", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get projects, got %d: %s", rec.Code, rec.Body.String())
	}

	// 6. Create Project
	createProjBody, _ := json.Marshal(map[string]string{"title": "My New Movie"})
	req = httptest.NewRequest(http.MethodPost, "/v1/flow/projects", bytes.NewReader(createProjBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for create project, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. Gallery
	req = httptest.NewRequest(http.MethodGet, "/v1/flow/gallery", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for gallery, got %d: %s", rec.Code, rec.Body.String())
	}
}
