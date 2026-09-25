package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const naraRouterURL = "https://router.bynara.id/v1/chat/completions"

// ================================================================
// NARAROUTER TYPES
// ================================================================

type naraMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type naraRequest struct {
	Model           string        `json:"model"`
	Messages        []naraMessage `json:"messages"`
	Temperature     float64       `json:"temperature,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type naraResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type landingPageResult struct {
	HTML string `json:"html"`
	CSS  string `json:"css"`
	JS   string `json:"js"`
}

type uploadedImage struct {
	Header *multipart.FileHeader
}

// ================================================================
// GENERATE LANDING PAGE
// ================================================================

func GenerateLandingPage(c *gin.Context) {
	log.Println("[GENERATOR] Request generator diterima")

	apiKey := os.Getenv("NARAROUTER_API_KEY")

	if apiKey == "" {
		log.Println("[GENERATOR] ERROR: NARAROUTER_API_KEY kosong")

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "NARAROUTER_API_KEY belum dikonfigurasi.",
		})

		return
	}

	// ============================================================
	// PROMPT
	// ============================================================

	prompt := strings.TrimSpace(
		c.PostForm("prompt"),
	)

	if prompt == "" {
		log.Println("[GENERATOR] ERROR: prompt kosong")

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Prompt wajib diisi.",
		})

		return
	}

	log.Printf(
		"[GENERATOR] Panjang prompt: %d karakter",
		len(prompt),
	)

	// ============================================================
	// MULTIPART FORM
	// ============================================================

	form, err := c.MultipartForm()

	if err != nil {
		log.Printf(
			"[GENERATOR] ERROR multipart: %v",
			err,
		)

		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Format request harus multipart/form-data.",
		})

		return
	}

	// ============================================================
	// UPLOAD IMAGES
	// ============================================================

	var images []uploadedImage

	if form != nil && form.File != nil {
		if headers, exists := form.File["images"]; exists {

			for _, header := range headers {

				if header == nil {
					continue
				}

				log.Printf(
					"[GENERATOR] Image: %s (%d bytes)",
					header.Filename,
					header.Size,
				)

				if !isAllowedImage(header.Filename) {

					c.JSON(http.StatusBadRequest, gin.H{
						"ok": false,
						"error": fmt.Sprintf(
							"Format gambar tidak didukung: %s",
							header.Filename,
						),
					})

					return
				}

				const maxImageSize = 10 * 1024 * 1024

				if header.Size > maxImageSize {

					c.JSON(http.StatusBadRequest, gin.H{
						"ok": false,
						"error": fmt.Sprintf(
							"Ukuran gambar terlalu besar: %s. Maksimal 10MB.",
							header.Filename,
						),
					})

					return
				}

				images = append(
					images,
					uploadedImage{
						Header: header,
					},
				)
			}
		}
	}

	log.Printf(
		"[GENERATOR] Jumlah image: %d",
		len(images),
	)

	// ============================================================
	// IMAGE NAMES
	// ============================================================

	imageNames := make(
		[]string,
		0,
		len(images),
	)

	for _, image := range images {

		if image.Header == nil {
			continue
		}

		imageNames = append(
			imageNames,
			safeFilename(
				image.Header.Filename,
			),
		)
	}

	// ============================================================
	// PROMPT
	// ============================================================

	systemPrompt := buildLandingPageSystemPrompt()

	userPrompt := buildLandingPageUserPrompt(
		prompt,
		imageNames,
	)

	log.Printf(
		"[GENERATOR] Panjang system prompt: %d karakter",
		len(systemPrompt),
	)

	log.Printf(
		"[GENERATOR] Panjang user prompt: %d karakter",
		len(userPrompt),
	)

	// ============================================================
	// CALL AI
	// ============================================================

	log.Println(
		"[GENERATOR] Memanggil NaraRouter...",
	)

	result, err := callNaraRouter(
		apiKey,
		systemPrompt,
		userPrompt,
	)

	if err != nil {

		log.Printf(
			"[GENERATOR] ERROR AI: %v",
			err,
		)

		c.JSON(http.StatusBadGateway, gin.H{
			"ok":     false,
			"error":  "Gagal mendapatkan response dari AI.",
			"detail": err.Error(),
		})

		return
	}

	log.Println(
		"[GENERATOR] AI berhasil menghasilkan landing page",
	)

	// ============================================================
	// CREATE ZIP
	// ============================================================

	log.Println(
		"[GENERATOR] Membuat ZIP...",
	)

	zipData, err := createLandingPageZip(
		result,
		images,
	)

	if err != nil {

		log.Printf(
			"[GENERATOR] ERROR membuat ZIP: %v",
			err,
		)

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":     false,
			"error":  "Gagal membuat file ZIP.",
			"detail": err.Error(),
		})

		return
	}

	log.Printf(
		"[GENERATOR] ZIP berhasil dibuat: %d bytes",
		len(zipData),
	)

	// ============================================================
	// RESPONSE
	// ============================================================

	c.Header(
		"Content-Type",
		"application/zip",
	)

	c.Header(
		"Content-Disposition",
		`attachment; filename="landing-page.zip"`,
	)

	c.Data(
		http.StatusOK,
		"application/zip",
		zipData,
	)
}

// ================================================================
// CALL NARAROUTER
// ================================================================

func callNaraRouter(
	apiKey string,
	systemPrompt string,
	userPrompt string,
) (*landingPageResult, error) {

	model := os.Getenv(
		"NARAROUTER_MODEL",
	)

	if model == "" {
		model = "agnes-2.5-flash"
	}

	log.Printf(
		"[AI] Model yang digunakan: %s",
		model,
	)

	log.Printf(
		"[AI] Endpoint: %s",
		naraRouterURL,
	)

	// ============================================================
	// REQUEST BODY
	// ============================================================

	requestBody := naraRequest{
		Model: model,

		Messages: []naraMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: userPrompt,
			},
		},

		Temperature:     0.2,
		MaxTokens:       16000,
		ReasoningEffort: "medium",
	}

	log.Println(
		"[AI] Membuat request body...",
	)

	jsonBody, err := json.Marshal(
		requestBody,
	)

	if err != nil {
		return nil, fmt.Errorf(
			"gagal membuat JSON request: %w",
			err,
		)
	}

	log.Println(
		"[AI] Request body berhasil dibuat.",
	)

	log.Printf(
		"[AI] Ukuran request: %d bytes",
		len(jsonBody),
	)

	// ============================================================
	// HTTP REQUEST
	// ============================================================

	req, err := http.NewRequest(
		http.MethodPost,
		naraRouterURL,
		bytes.NewBuffer(jsonBody),
	)

	if err != nil {
		return nil, fmt.Errorf(
			"gagal membuat HTTP request: %w",
			err,
		)
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+apiKey,
	)

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	req.Header.Set(
		"Accept",
		"application/json",
	)

	log.Println(
		"[AI] Request siap.",
	)

	// ============================================================
	// HTTP CLIENT
	// ============================================================

	client := &http.Client{
		Timeout: 180 * time.Second,
	}

	start := time.Now()

	log.Println(
		"[AI] Menunggu response dari NaraRouter...",
	)

	resp, err := client.Do(req)

	duration := time.Since(start)

	if err != nil {

		log.Printf(
			"[AI] ERROR setelah %s: %v",
			duration,
			err,
		)

		return nil, fmt.Errorf(
			"gagal menghubungi NaraRouter: %w",
			err,
		)
	}

	defer resp.Body.Close()

	log.Printf(
		"[AI] Response diterima setelah: %s",
		duration,
	)

	log.Printf(
		"[AI] HTTP Status: %s",
		resp.Status,
	)

	log.Printf(
		"[AI] Content-Type: %s",
		resp.Header.Get("Content-Type"),
	)

	log.Printf(
		"[AI] Content-Length: %s",
		resp.Header.Get("Content-Length"),
	)

	log.Printf(
		"[AI] Transfer-Encoding: %v",
		resp.TransferEncoding,
	)

	// ============================================================
	// READ RESPONSE
	// ============================================================

	log.Println(
		"[AI] Mulai membaca response body...",
	)

	responseBody, err := io.ReadAll(
		resp.Body,
	)

	if err != nil {

		log.Printf(
			"[AI] ERROR membaca response body: %v",
			err,
		)

		return nil, fmt.Errorf(
			"gagal membaca response NaraRouter: %w",
			err,
		)
	}

	log.Println(
		"[AI] Selesai membaca response body.",
	)

	log.Printf(
		"[AI] Ukuran response: %d bytes",
		len(responseBody),
	)

	// ============================================================
	// HTTP ERROR
	// ============================================================

	if resp.StatusCode < 200 ||
		resp.StatusCode >= 300 {

		log.Printf(
			"[AI] HTTP ERROR BODY: %s",
			string(responseBody),
		)

		return nil, fmt.Errorf(
			"NaraRouter HTTP %d: %s",
			resp.StatusCode,
			string(responseBody),
		)
	}

	// ============================================================
	// PARSE NARAROUTER RESPONSE
	// ============================================================

	var naraRes naraResponse

	if err := json.Unmarshal(
		responseBody,
		&naraRes,
	); err != nil {

		return nil, fmt.Errorf(
			"response NaraRouter bukan JSON yang valid: %w",
			err,
		)
	}

	if len(naraRes.Choices) == 0 {
		return nil, fmt.Errorf(
			"AI tidak mengembalikan choices",
		)
	}

	// ============================================================
	// CONTENT
	// ============================================================

	content := strings.TrimSpace(
		naraRes.Choices[0].Message.Content,
	)

	log.Printf(
		"[AI] Panjang content mentah: %d bytes",
		len(content),
	)

	if content == "" {
		return nil, fmt.Errorf(
			"AI mengembalikan content kosong",
		)
	}

	// ============================================================
	// CLEAN JSON
	// ============================================================

	content = cleanJSONResponse(
		content,
	)

	log.Printf(
		"[AI] Panjang content setelah cleaning: %d bytes",
		len(content),
	)

	if len(content) == 0 {
		return nil, fmt.Errorf(
			"content setelah cleaning kosong",
		)
	}

	// ============================================================
	// NORMALIZE JSON
	// ============================================================

	normalizedContent := normalizeJSONStrings(
		content,
	)

	log.Printf(
		"[AI] Panjang content setelah normalize: %d bytes",
		len(normalizedContent),
	)

	// ============================================================
	// PARSE LANDING PAGE RESULT
	// ============================================================

	var result landingPageResult

	if err := json.Unmarshal(
		[]byte(normalizedContent),
		&result,
	); err != nil {

		// Tampilkan informasi debugging tanpa
		// mencetak seluruh response AI.

		if len(normalizedContent) > 500 {
			log.Printf(
				"[AI] Akhir content: %s",
				normalizedContent[len(normalizedContent)-500:],
			)
		} else {
			log.Printf(
				"[AI] Content: %s",
				normalizedContent,
			)
		}

		return nil, fmt.Errorf(
			"AI mengembalikan JSON yang tidak valid: %w",
			err,
		)
	}

	// ============================================================
	// VALIDATE HTML
	// ============================================================

	if strings.TrimSpace(
		result.HTML,
	) == "" {

		return nil, fmt.Errorf(
			"AI tidak menghasilkan HTML",
		)
	}

	// ============================================================
	// VALIDATE CSS
	// ============================================================

	if strings.TrimSpace(
		result.CSS,
	) == "" {

		return nil, fmt.Errorf(
			"AI tidak menghasilkan CSS",
		)
	}

	// ============================================================
	// VALIDATE JS
	// ============================================================

	if strings.TrimSpace(
		result.JS,
	) == "" {

		return nil, fmt.Errorf(
			"AI tidak menghasilkan JavaScript",
		)
	}

	log.Printf(
		"[AI] HTML: %d bytes",
		len(result.HTML),
	)

	log.Printf(
		"[AI] CSS: %d bytes",
		len(result.CSS),
	)

	log.Printf(
		"[AI] JS: %d bytes",
		len(result.JS),
	)

	return &result, nil
}

// ================================================================
// SYSTEM PROMPT
// ================================================================

func buildLandingPageSystemPrompt() string {
	return `
You are a senior frontend developer.

Generate a complete modern responsive landing page based on the user's request.

TECHNOLOGY:
- HTML5
- CSS3
- Vanilla JavaScript
- No React
- No Vue
- No npm
- No build tools
- No external JavaScript libraries

FILES:
- index.html
- style.css
- script.js

DESIGN:
- Modern
- Professional
- Responsive
- Clean
- Good spacing
- Attractive typography
- Mobile friendly
- Accessible
- Use semantic HTML

IMAGES:
Uploaded images will be available inside:
assets/

Use the exact provided asset filename when an image is useful.

CODE SIZE:
Keep the generated code concise.
Do not add unnecessary comments.
Do not repeat CSS rules.
Do not create unnecessary JavaScript.
Do not create extremely large sections.
Prefer reusable CSS classes.

IMPORTANT:
Return ONLY one valid JSON object.

Required format:

{
  "html": "complete index.html",
  "css": "complete style.css",
  "js": "complete script.js"
}

JSON REQUIREMENTS:
- html, css and js MUST be JSON strings.
- Escape quotation marks correctly.
- Escape newlines correctly.
- Do not use Markdown.
- Do not use code fences.
- Do not add explanations.
- Do not add text before the JSON.
- Do not add text after the JSON.
- Always finish the JSON object completely.
`
}

// ================================================================
// USER PROMPT
// ================================================================

func buildLandingPageUserPrompt(
	prompt string,
	imageNames []string,
) string {

	var imagesText string

	if len(imageNames) == 0 {

		imagesText = "Tidak ada gambar yang diupload."

	} else {

		imagesText = "Gambar yang tersedia:\n"

		for _, name := range imageNames {

			imagesText +=
				"- assets/" + name + "\n"
		}
	}

	return fmt.Sprintf(
		`
Buat landing page berdasarkan permintaan berikut:

%s

%s

Prioritas:
1. Penuhi kebutuhan utama pengguna.
2. Buat desain profesional.
3. Pastikan responsive.
4. Pastikan HTML, CSS dan JavaScript berfungsi.
5. Gunakan kode sesingkat mungkin tanpa mengurangi kualitas.
6. Jangan menambahkan fitur yang tidak diperlukan.

Output HARUS berupa JSON lengkap dengan tiga property:
html
css
js

JANGAN gunakan Markdown.
JANGAN gunakan code fence.
JANGAN memberikan penjelasan.
`,
		prompt,
		imagesText,
	)
}

// ================================================================
// CREATE ZIP
// ================================================================

func createLandingPageZip(
	result *landingPageResult,
	images []uploadedImage,
) ([]byte, error) {

	var buffer bytes.Buffer

	zipWriter := zip.NewWriter(
		&buffer,
	)

	// ============================================================
	// INDEX HTML
	// ============================================================

	if err := addZipFile(
		zipWriter,
		"index.html",
		result.HTML,
	); err != nil {

		return nil, err
	}

	// ============================================================
	// CSS
	// ============================================================

	if err := addZipFile(
		zipWriter,
		"style.css",
		result.CSS,
	); err != nil {

		return nil, err
	}

	// ============================================================
	// JS
	// ============================================================

	if err := addZipFile(
		zipWriter,
		"script.js",
		result.JS,
	); err != nil {

		return nil, err
	}

	// ============================================================
	// IMAGES
	// ============================================================

	for _, image := range images {

		if image.Header == nil {
			continue
		}

		file, err := image.Header.Open()

		if err != nil {
			return nil, err
		}

		filename := safeFilename(
			image.Header.Filename,
		)

		writer, err := zipWriter.Create(
			"assets/" + filename,
		)

		if err != nil {

			file.Close()

			return nil, err
		}

		_, copyErr := io.Copy(
			writer,
			file,
		)

		file.Close()

		if copyErr != nil {
			return nil, copyErr
		}
	}

	// ============================================================
	// CLOSE ZIP
	// ============================================================

	if err := zipWriter.Close(); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

// ================================================================
// ADD ZIP FILE
// ================================================================

func addZipFile(
	writer *zip.Writer,
	filename string,
	content string,
) error {

	file, err := writer.Create(
		filename,
	)

	if err != nil {
		return err
	}

	_, err = file.Write(
		[]byte(content),
	)

	return err
}

// ================================================================
// CLEAN JSON RESPONSE
// ================================================================
//
// Berbeda dengan versi sebelumnya, fungsi ini TIDAK menggunakan:
//
// strings.LastIndex(content, "}")
//
// karena HTML/CSS/JS dapat memiliki banyak karakter { dan }.
//
// Fungsi ini mencari pasangan { } JSON paling luar dan
// mengabaikan karakter { } yang berada di dalam string JSON.
// ================================================================

func cleanJSONResponse(
	content string,
) string {

	content = strings.TrimSpace(
		content,
	)

	// ============================================================
	// REMOVE ```json
	// ============================================================

	if strings.HasPrefix(
		content,
		"```json",
	) {

		content = strings.TrimPrefix(
			content,
			"```json",
		)

		content = strings.TrimSpace(
			content,
		)

		if strings.HasSuffix(
			content,
			"```",
		) {

			content = strings.TrimSuffix(
				content,
				"```",
			)
		}

		content = strings.TrimSpace(
			content,
		)
	}

	// ============================================================
	// REMOVE ```
	// ============================================================

	if strings.HasPrefix(
		content,
		"```",
	) {

		content = strings.TrimPrefix(
			content,
			"```",
		)

		content = strings.TrimSpace(
			content,
		)

		if strings.HasSuffix(
			content,
			"```",
		) {

			content = strings.TrimSuffix(
				content,
				"```",
			)
		}

		content = strings.TrimSpace(
			content,
		)
	}

	// ============================================================
	// FIND FIRST JSON OBJECT
	// ============================================================

	firstBrace := strings.Index(
		content,
		"{",
	)

	if firstBrace < 0 {
		return content
	}

	// ============================================================
	// FIND REAL JSON CLOSING BRACE
	// ============================================================

	inString := false
	escaped := false
	depth := 0

	for i := firstBrace; i < len(content); i++ {

		char := content[i]

		// --------------------------------------------------------
		// Escaped character
		// --------------------------------------------------------

		if escaped {

			escaped = false

			continue
		}

		// --------------------------------------------------------
		// Backslash inside JSON string
		// --------------------------------------------------------

		if char == '\\' &&
			inString {

			escaped = true

			continue
		}

		// --------------------------------------------------------
		// JSON string quote
		// --------------------------------------------------------

		if char == '"' {

			inString = !inString

			continue
		}

		// --------------------------------------------------------
		// Ignore braces inside strings
		// --------------------------------------------------------

		if inString {
			continue
		}

		// --------------------------------------------------------
		// JSON OBJECT DEPTH
		// --------------------------------------------------------

		switch char {

		case '{':

			depth++

		case '}':

			depth--

			// JSON object selesai.
			if depth == 0 {

				return strings.TrimSpace(
					content[firstBrace : i+1],
				)
			}
		}
	}

	// ============================================================
	// JSON TIDAK SELESAI
	// ============================================================

	return strings.TrimSpace(
		content[firstBrace:],
	)
}

// ================================================================
// NORMALIZE JSON STRINGS
// ================================================================
//
// Memperbaiki control character mentah yang muncul di dalam
// JSON string, seperti newline, tab, carriage return, dll.
// ================================================================

func normalizeJSONStrings(
	content string,
) string {

	var result strings.Builder

	result.Grow(
		len(content),
	)

	inString := false
	escaped := false

	for i := 0; i < len(content); i++ {

		char := content[i]

		// --------------------------------------------------------
		// Character setelah escape
		// --------------------------------------------------------

		if escaped {

			result.WriteByte(
				char,
			)

			escaped = false

			continue
		}

		// --------------------------------------------------------
		// Backslash
		// --------------------------------------------------------

		if char == '\\' &&
			inString {

			result.WriteByte(
				char,
			)

			escaped = true

			continue
		}

		// --------------------------------------------------------
		// Quote
		// --------------------------------------------------------

		if char == '"' {

			result.WriteByte(
				char,
			)

			inString = !inString

			continue
		}

		// --------------------------------------------------------
		// Control character
		// --------------------------------------------------------

		if inString {

			switch char {

			case '\n':

				result.WriteString(
					`\n`,
				)

			case '\r':

				result.WriteString(
					`\r`,
				)

			case '\t':

				result.WriteString(
					`\t`,
				)

			case '\b':

				result.WriteString(
					`\b`,
				)

			case '\f':

				result.WriteString(
					`\f`,
				)

			default:

				if char < 0x20 {

					fmt.Fprintf(
						&result,
						`\\u%04x`,
						char,
					)

				} else {

					result.WriteByte(
						char,
					)
				}
			}

			continue
		}

		result.WriteByte(
			char,
		)
	}

	return result.String()
}

// ================================================================
// IMAGE VALIDATION
// ================================================================

func isAllowedImage(
	filename string,
) bool {

	ext := strings.ToLower(
		filepath.Ext(filename),
	)

	switch ext {

	case ".png",
		".jpg",
		".jpeg",
		".webp",
		".gif":

		return true

	default:

		return false
	}
}

// ================================================================
// SAFE FILENAME
// ================================================================

func safeFilename(
	filename string,
) string {

	filename = filepath.Base(
		filename,
	)

	filename = strings.ReplaceAll(
		filename,
		" ",
		"-",
	)

	var result strings.Builder

	for _, char := range filename {

		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' ||
			char == '_' ||
			char == '.' {

			result.WriteRune(
				char,
			)
		}
	}

	cleaned := result.String()

	if cleaned == "" {
		return "image"
	}

	return cleaned
}
