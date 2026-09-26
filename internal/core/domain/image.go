package domain

type ImageGenerationRequest struct {
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	N           int    `json:"n,omitempty"`
	Size        string `json:"size,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

type ImageGenerationResult struct {
	URL            string
	MimeType       string
	UnmappedFields int
	SpecVersion    string
	CreditsBalance *int
}

type VideoGenerationResult struct {
	URL            string
	MimeType       string
	UnmappedFields int
	SpecVersion    string
	CreditsBalance *int
}
