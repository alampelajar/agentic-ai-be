package handlers

import (
	"errors"
	"net/http"
	"strings"

	"agentic-ai-backend/services"
)

// imageRefusalHints are parts of provider errors that mean the model cannot take images: OpenRouter
// says no endpoint supports image input, and text-only APIs such as Groq's refuse a message whose
// content is a list of parts instead of a string.
var imageRefusalHints = []string{"image input", "support image", "content must be a string", "vision", "multimodal", "image_url"}

// imageUnsupportedMessage explains, in words a person can act on, that the chosen model cannot read
// the attached photo. It is "" when the request had no images or the failure is about something else.
func imageUnsupportedMessage(err error, hadImages bool, modelName string) string {
	if !hadImages || err == nil {
		return ""
	}
	var providerErr *services.ProviderHTTPError
	if !errors.As(err, &providerErr) {
		return ""
	}
	switch providerErr.Status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
	default:
		return ""
	}
	detail := strings.ToLower(providerErr.Message)
	for _, hint := range imageRefusalHints {
		if strings.Contains(detail, hint) {
			return "Model " + modelName + " tidak bisa membaca gambar. Pilih model yang mendukung gambar (vision), misalnya Gemini atau nvidia/nemotron-3.5-super-vl:free, lalu kirim ulang fotonya."
		}
	}
	return ""
}
