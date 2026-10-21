package handlers

import (
	"bytes"
	"encoding/base64"
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

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

// ================================================================
// TYPES
// ================================================================

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Temperature     float64       `json:"temperature,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type chatHistoryItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type incomingChatRequest struct {
	Message string            `json:"message"`
	History []chatHistoryItem `json:"history"`
	AgentID uint              `json:"agent_id"`
	ModelID uint              `json:"model_id"`
}

// ================================================================
// CHAT
// ================================================================

func Chat(c *gin.Context) {
	log.Println("[CHAT] Request chat diterima")

	// ============================================================
	// USER
	// ============================================================

	userID, ok := getCurrentUserID(c)

	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "User tidak ditemukan.",
		})
		return
	}

	// ============================================================
	// REQUEST DATA
	// ============================================================

	contentType := c.GetHeader("Content-Type")

	var prompt string
	var history []chatHistoryItem

	var agentID uint
	var modelID uint

	// ============================================================
	// JSON REQUEST
	// ============================================================

	if strings.HasPrefix(
		contentType,
		"application/json",
	) {
		var body incomingChatRequest

		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Format request tidak valid.",
			})
			return
		}

		prompt = strings.TrimSpace(
			body.Message,
		)

		history = body.History
		agentID = body.AgentID
		modelID = body.ModelID
	}

	// ============================================================
	// MULTIPART REQUEST
	// ============================================================

	var images []*multipart.FileHeader

	if strings.HasPrefix(
		contentType,
		"multipart/form-data",
	) {
		prompt = strings.TrimSpace(
			c.PostForm("message"),
		)

		// --------------------------------------------------------
		// HISTORY
		// --------------------------------------------------------

		historyJSON := strings.TrimSpace(
			c.PostForm("history"),
		)

		if historyJSON != "" {
			if err := json.Unmarshal(
				[]byte(historyJSON),
				&history,
			); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"ok":    false,
					"error": "History chat tidak valid.",
				})
				return
			}
		}

		// --------------------------------------------------------
		// AGENT ID
		// --------------------------------------------------------

		agentID = parseUintForm(
			c.PostForm("agent_id"),
		)

		// --------------------------------------------------------
		// MODEL ID
		// --------------------------------------------------------

		modelID = parseUintForm(
			c.PostForm("model_id"),
		)

		// --------------------------------------------------------
		// FILES
		// --------------------------------------------------------

		form, err := c.MultipartForm()

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Format multipart/form-data tidak valid.",
			})
			return
		}

		if form != nil &&
			form.File != nil {

			if uploaded, exists :=
				form.File["images"]; exists {

				images = uploaded
			}
		}
	}

	// ============================================================
	// VALIDATE MESSAGE
	// ============================================================

	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Message wajib diisi.",
		})
		return
	}

	log.Printf(
		"[CHAT] User: %d | Agent: %d | Requested Model: %d",
		userID,
		agentID,
		modelID,
	)

	// ============================================================
	// FIND AGENT
	// ============================================================

	if agentID != 0 {
		var agent models.Agent

		if err := config.DB.
			Where(
				"id = ? AND is_active = ?",
				agentID,
				true,
			).
			First(&agent).
			Error; err != nil {

			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Agent tidak ditemukan atau tidak aktif.",
			})
			return
		}
	}

	// ============================================================
	// FIND MODEL
	// ============================================================

	var aiModel models.AIModel

	// ------------------------------------------------------------
	// MODEL DIPILIH USER
	// ------------------------------------------------------------

	if modelID != 0 {

		err := config.DB.
			Table("ai_models").
			Preload("Provider").
			Joins(`
				INNER JOIN agent_models
					ON agent_models.ai_model_id = ai_models.id
			`).
			Joins(`
				INNER JOIN a_iproviders
					ON a_iproviders.id = ai_models.provider_id
			`).
			Where(
				"ai_models.id = ? AND agent_models.agent_id = ?",
				modelID,
				agentID,
			).
			Where(
				"ai_models.is_active = ?",
				true,
			).
			Where(
				"agent_models.agent_id = ?",
				agentID,
			).
			Where(
				"a_iproviders.is_active = ?",
				true,
			).
			Where(`
				ai_models.is_system = ?
				OR ai_models.user_id = ?
			`,
				true,
				userID,
			).
			First(&aiModel).
			Error

		if err != nil {
			log.Printf(
				"[CHAT] MODEL ERROR: %v",
				err,
			)

			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Model AI tidak ditemukan atau tidak dapat digunakan.",
			})
			return
		}
	}

	// ------------------------------------------------------------
	// FALLBACK MODEL DEFAULT
	// ------------------------------------------------------------

	if modelID == 0 {

		query := config.DB.
			Table("ai_models").
			Preload("Provider").
			Joins(`
				INNER JOIN a_iproviders
					ON a_iproviders.id = ai_models.provider_id
			`).
			Where(
				"ai_models.is_active = ?",
				true,
			).
			Where(
				"a_iproviders.is_active = ?",
				true,
			).
			Where(`
				ai_models.is_system = ?
				OR ai_models.user_id = ?
			`,
				true,
				userID,
			)

		if agentID != 0 {
			query = query.
				Joins(`
					INNER JOIN agent_models
						ON agent_models.ai_model_id = ai_models.id
				`).
				Where("agent_models.agent_id = ?", agentID)
		}

		err := query.
			Order("ai_models.is_system DESC").
			Order("ai_models.id ASC").
			First(&aiModel).
			Error

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":    false,
				"error": "Tidak ada model AI yang tersedia.",
			})
			return
		}
	}

	// ============================================================
	// MODEL SUDAH DIVALIDASI TERHADAP AGENT DI QUERY DI ATAS.
	// ============================================================

	// ============================================================
	// LOG MODEL YANG AKAN DIGUNAKAN
	// ============================================================

	log.Printf(
		"[CHAT] Selected Agent: %d",
		agentID,
	)

	log.Printf(
		"[CHAT] Selected Model ID: %d",
		aiModel.ID,
	)

	log.Printf(
		"[CHAT] Selected Model: %s",
		aiModel.ModelID,
	)

	log.Printf(
		"[CHAT] Provider: %s",
		aiModel.Provider.Name,
	)

	// ============================================================
	// API KEY
	// ============================================================

	var apiKey string

	// ------------------------------------------------------------
	// SYSTEM PROVIDER
	// ------------------------------------------------------------

	if aiModel.Provider.IsSystem {
		// Admin-provisioned system providers store the shared key encrypted
		// in PostgreSQL. Keep environment-variable fallback for legacy seeds.
		if strings.TrimSpace(aiModel.Provider.APIKeyEncrypted) != "" {
			decryptedKey, err := utils.DecryptString(aiModel.Provider.APIKeyEncrypted)
			if err != nil {
				log.Printf("[CHAT] ERROR decrypt system provider API key: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"ok": false, "error": "Gagal membaca API key provider sistem.",
				})
				return
			}
			apiKey = strings.TrimSpace(decryptedKey)
		} else {
			switch strings.ToLower(strings.TrimSpace(aiModel.Provider.Type)) {
			case "nararouter":
				apiKey = strings.TrimSpace(os.Getenv("NARAROUTER_API_KEY"))
			case "openrouter":
				apiKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
			case "openai":
				apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
			case "google":
				apiKey = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
				if apiKey == "" {
					apiKey = strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
				}
			case "deepseek":
				apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
			}
		}
	} else {
		// --------------------------------------------------------
		// USER-OWNED PROVIDER: never use another user's provider.
		// --------------------------------------------------------
		if aiModel.Provider.UserID == nil || *aiModel.Provider.UserID != userID {
			c.JSON(http.StatusForbidden, gin.H{
				"ok": false, "error": "Provider bukan milik user.",
			})
			return
		}

		if strings.TrimSpace(aiModel.Provider.APIKeyEncrypted) == "" {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok": false, "error": "API key provider belum dikonfigurasi.",
			})
			return
		}

		decryptedKey, err := utils.DecryptString(aiModel.Provider.APIKeyEncrypted)
		if err != nil {
			log.Printf("[CHAT] ERROR decrypt user provider API key: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok": false, "error": "Gagal membaca API key provider.",
			})
			return
		}
		apiKey = strings.TrimSpace(decryptedKey)
	}

	// ============================================================
	// API KEY VALIDATION
	// ============================================================

	if apiKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "API key provider belum dikonfigurasi.",
		})
		return
	}

	// ============================================================
	// SYSTEM PROMPT
	// ============================================================

	agentSlug := ""
	if agentID != 0 {
		var selectedAgent models.Agent
		if err := config.DB.Select("slug").First(&selectedAgent, agentID).Error; err == nil {
			agentSlug = strings.ToLower(strings.TrimSpace(selectedAgent.Slug))
		}
	}
	systemPrompt := `
You are Agentic AI, a helpful AI assistant.

Your job is to answer the user's questions directly and naturally.

IMPORTANT:
- Answer the user's actual question.
- Use the conversation history to understand context.
- Remember information from previous messages in the current conversation.
- If the user refers to something like "itu", "yang tadi", "sebelumnya", or "tersebut", use the conversation history to determine what they mean.
- Do not create a website unless the user explicitly asks you to create one.
- Do not return HTML, CSS, JavaScript, or JSON unless the user specifically asks for them.
- If the user asks about an uploaded image, analyze the image and explain what you can see.
- If an image is provided but the user does not ask about it, use it only when relevant.
- Be concise but helpful.
- Use Indonesian when the user speaks Indonesian.
- Do not pretend that you can see an image if no image was provided.
`

	if strings.Contains(agentSlug, "coding") || strings.Contains(agentSlug, "code") {
		systemPrompt += `

AGENT ROLE: CODING AGENT
- Focus on implementing and modifying software, debugging code, and explaining changes.
- Prefer concrete patches, small steps, and existing project conventions.
- When the user explicitly asks you to inspect/edit/run a local project and the frontend says a Local Agent Runner is connected, you may request exactly one runner tool at a time using this strict marker:
<LOCAL_AGENT_TOOL>{"action":"git-status","runtime":"native"}</LOCAL_AGENT_TOOL>
- Allowed actions: git-status, go-test, go-build, npm-test, npm-build, npm-lint, python-test. runtime is native or docker.
- Never invent tool results. Wait for the actual tool output before continuing.
- Do not request arbitrary shell commands, scripts, file deletion, network operations, secret reads, or commands outside the allowlist.
- If no runner is connected, provide code/patches and manual steps instead of claiming you changed local files.
- When done, summarize files changed and tests actually run.
`
	} else if strings.Contains(agentSlug, "testing") || strings.Contains(agentSlug, "test") {
		systemPrompt += `

AGENT ROLE: TESTING AGENT
- Focus on test strategy, test creation, test execution, reproducing bugs, and reporting evidence.
- Prioritize existing project test commands and targeted tests before broader suites.
- When the user asks to test a local project and the frontend says a Local Agent Runner is connected, you may request exactly one runner tool at a time using this strict marker:
<LOCAL_AGENT_TOOL>{"action":"go-test","runtime":"native"}</LOCAL_AGENT_TOOL>
- Allowed actions: git-status, go-test, go-build, npm-test, npm-build, npm-lint, python-test. runtime is native or docker.
- Never invent test output or claim a test passed without actual runner output.
- Do not request arbitrary shell commands, scripts, file deletion, network operations, or commands outside the allowlist.
- If no runner is connected, explain what the user should run manually and what evidence is needed.
- End with a concise pass/fail/blocked report, listing actual commands and exit codes.
`
	} else if strings.Contains(agentSlug, "review") {
		systemPrompt += `

AGENT ROLE: CODE REVIEW AGENT
- Prioritize correctness, security, regressions, and maintainability.
- Rank findings by severity and cite file/line references when available.
- Do not claim that code was executed unless an actual tool result is present.
`
	} else if strings.Contains(agentSlug, "planning") || strings.Contains(agentSlug, "plan") {
		systemPrompt += `

AGENT ROLE: PLANNING AGENT
- Focus on requirements, architecture, task decomposition, dependencies, and acceptance criteria.
- Produce an actionable plan before suggesting implementation details.
`
	} else if strings.Contains(agentSlug, "debug") {
		systemPrompt += `

AGENT ROLE: DEBUGGING AGENT
- Focus on reproducing the failure, tracing the root cause, proposing the smallest safe fix, and verifying it with evidence.
- Do not report a hypothesis as a confirmed root cause until supported by logs or test output.
`
	}

	// ============================================================
	// CURRENT MESSAGE CONTENT
	// ============================================================

	messageContent, err :=
		buildChatContent(
			prompt,
			images,
		)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":     false,
			"error":  "Gagal memproses gambar.",
			"detail": err.Error(),
		})
		return
	}

	// ============================================================
	// BUILD MESSAGES
	// ============================================================

	messages := []chatMessage{
		{
			Role:    "system",
			Content: systemPrompt,
		},
	}

	// ------------------------------------------------------------
	// HISTORY
	// ------------------------------------------------------------

	for _, item := range history {

		role := item.Role

		if role != "user" &&
			role != "assistant" {
			continue
		}

		content := strings.TrimSpace(
			item.Content,
		)

		if content == "" {
			continue
		}

		messages = append(
			messages,
			chatMessage{
				Role:    role,
				Content: content,
			},
		)
	}

	// ------------------------------------------------------------
	// CURRENT MESSAGE
	// ------------------------------------------------------------

	messages = append(
		messages,
		chatMessage{
			Role:    "user",
			Content: messageContent,
		},
	)

	log.Printf(
		"[CHAT] Total messages ke AI: %d",
		len(messages),
	)

	// ============================================================
	// REQUEST BODY
	// ============================================================

	// PENTING:
	// Model diambil dari database berdasarkan model_id
	// yang dipilih user.
	requestBody := chatRequest{
		Model: aiModel.ModelID,

		Messages: messages,

		Temperature: 0.5,

		MaxTokens: 2048,

		ReasoningEffort: "low",
	}

	log.Printf(
		"[CHAT] Sending model: %s",
		requestBody.Model,
	)

	// ============================================================
	// CALL PROVIDER
	// ============================================================

	answer, err :=
		callAIProvider(
			apiKey,
			aiModel.Provider.BaseURL,
			requestBody,
		)

	if err != nil {
		log.Printf(
			"[CHAT] ERROR AI: %v",
			err,
		)

		c.JSON(http.StatusBadGateway, gin.H{
			"ok":     false,
			"error":  "Gagal mendapatkan jawaban dari AI.",
			"detail": err.Error(),
		})
		return
	}

	// ============================================================
	// RESPONSE
	// ============================================================

	c.JSON(http.StatusOK, gin.H{
		"ok": true,

		"message": answer,

		"model": aiModel.ModelID,

		"model_id": aiModel.ID,

		"provider": aiModel.Provider.Name,

		"agent_id": agentID,
	})
}

// ================================================================
// PARSE UINT FORM
// ================================================================

func parseUintForm(
	value string,
) uint {

	value = strings.TrimSpace(
		value,
	)

	if value == "" {
		return 0
	}

	var result uint

	for _, char := range value {

		if char < '0' ||
			char > '9' {

			return 0
		}

		result =
			result*10 +
				uint(char-'0')
	}

	return result
}

// ================================================================
// CALL OPENAI-COMPATIBLE PROVIDER
// ================================================================

func callAIProvider(
	apiKey string,
	baseURL string,
	requestBody chatRequest,
) (string, error) {

	baseURL = strings.TrimRight(
		strings.TrimSpace(baseURL),
		"/",
	)

	if baseURL == "" {
		return "", fmt.Errorf(
			"base URL provider kosong",
		)
	}

	// ------------------------------------------------------------
	// NORMALIZE ENDPOINT
	// ------------------------------------------------------------

	var endpoint string

	if strings.HasSuffix(
		baseURL,
		"/chat/completions",
	) {
		endpoint = baseURL
	} else {
		endpoint =
			baseURL +
				"/chat/completions"
	}

	// ------------------------------------------------------------
	// JSON
	// ------------------------------------------------------------

	jsonBody, err :=
		json.Marshal(
			requestBody,
		)

	if err != nil {
		return "", fmt.Errorf(
			"gagal membuat JSON request: %w",
			err,
		)
	}

	// ------------------------------------------------------------
	// HTTP REQUEST
	// ------------------------------------------------------------

	req, err :=
		http.NewRequest(
			http.MethodPost,
			endpoint,
			bytes.NewBuffer(jsonBody),
		)

	if err != nil {
		return "", fmt.Errorf(
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

	// ------------------------------------------------------------
	// CLIENT
	// ------------------------------------------------------------

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	start := time.Now()

	log.Printf(
		"[CHAT AI] Endpoint: %s",
		endpoint,
	)

	log.Printf(
		"[CHAT AI] Model: %s",
		requestBody.Model,
	)

	// ------------------------------------------------------------
	// REQUEST
	// ------------------------------------------------------------

	resp, err :=
		client.Do(req)

	duration :=
		time.Since(start)

	if err != nil {
		return "", fmt.Errorf(
			"gagal menghubungi provider AI: %w",
			err,
		)
	}

	defer resp.Body.Close()

	// ------------------------------------------------------------
	// RESPONSE BODY
	// ------------------------------------------------------------

	responseBody, err :=
		io.ReadAll(
			resp.Body,
		)

	if err != nil {
		return "", fmt.Errorf(
			"gagal membaca response provider: %w",
			err,
		)
	}

	log.Printf(
		"[CHAT AI] HTTP %s setelah %s",
		resp.Status,
		duration,
	)

	// ------------------------------------------------------------
	// HTTP ERROR
	// ------------------------------------------------------------

	if resp.StatusCode < 200 ||
		resp.StatusCode >= 300 {

		log.Printf(
			"[CHAT AI] ERROR BODY: %s",
			string(responseBody),
		)

		return "", fmt.Errorf(
			"provider HTTP %d: %s",
			resp.StatusCode,
			string(responseBody),
		)
	}

	// ------------------------------------------------------------
	// PARSE JSON
	// ------------------------------------------------------------

	var aiResponse chatResponse

	if err := json.Unmarshal(
		responseBody,
		&aiResponse,
	); err != nil {

		return "", fmt.Errorf(
			"response provider bukan JSON yang valid: %w",
			err,
		)
	}

	// ------------------------------------------------------------
	// VALIDATE CHOICES
	// ------------------------------------------------------------

	if len(aiResponse.Choices) == 0 {
		return "", fmt.Errorf(
			"AI tidak mengembalikan choices",
		)
	}

	// ------------------------------------------------------------
	// ANSWER
	// ------------------------------------------------------------

	answer := strings.TrimSpace(
		aiResponse.Choices[0].Message.Content,
	)

	if answer == "" {
		return "", fmt.Errorf(
			"AI mengembalikan jawaban kosong",
		)
	}

	return answer, nil
}

// ================================================================
// BUILD CHAT CONTENT
// ================================================================

func buildChatContent(
	prompt string,
	images []*multipart.FileHeader,
) (interface{}, error) {

	// ============================================================
	// TEXT ONLY
	// ============================================================

	if len(images) == 0 {
		return prompt, nil
	}

	// ============================================================
	// MULTIMODAL
	// ============================================================

	content := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": prompt,
		},
	}

	for _, image := range images {

		if image == nil {
			continue
		}

		// --------------------------------------------------------
		// VALIDATE EXTENSION
		// --------------------------------------------------------

		if !isAllowedChatImage(
			image.Filename,
		) {
			return nil, fmt.Errorf(
				"format gambar tidak didukung: %s",
				image.Filename,
			)
		}

		// --------------------------------------------------------
		// MAX SIZE
		// --------------------------------------------------------

		const maxImageSize = 10 * 1024 * 1024

		if image.Size >
			maxImageSize {

			return nil, fmt.Errorf(
				"gambar %s terlalu besar. Maksimal 10MB",
				image.Filename,
			)
		}

		// --------------------------------------------------------
		// OPEN
		// --------------------------------------------------------

		file, err :=
			image.Open()

		if err != nil {
			return nil, fmt.Errorf(
				"gagal membuka gambar %s: %w",
				image.Filename,
				err,
			)
		}

		// --------------------------------------------------------
		// READ
		// --------------------------------------------------------

		data, err :=
			io.ReadAll(file)

		file.Close()

		if err != nil {
			return nil, fmt.Errorf(
				"gagal membaca gambar %s: %w",
				image.Filename,
				err,
			)
		}

		// --------------------------------------------------------
		// MIME
		// --------------------------------------------------------

		mimeType :=
			detectImageMimeType(
				image.Filename,
			)

		// --------------------------------------------------------
		// BASE64
		// --------------------------------------------------------

		base64Image :=
			base64.StdEncoding.EncodeToString(
				data,
			)

		imageURL :=
			fmt.Sprintf(
				"data:%s;base64,%s",
				mimeType,
				base64Image,
			)

		// --------------------------------------------------------
		// APPEND IMAGE
		// --------------------------------------------------------

		content =
			append(
				content,
				map[string]interface{}{
					"type": "image_url",

					"image_url": map[string]interface{}{
						"url": imageURL,
					},
				},
			)
	}

	return content, nil
}

// ================================================================
// IMAGE VALIDATION
// ================================================================

func isAllowedChatImage(
	filename string,
) bool {

	ext :=
		strings.ToLower(
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
// IMAGE MIME TYPE
// ================================================================

func detectImageMimeType(
	filename string,
) string {

	ext :=
		strings.ToLower(
			filepath.Ext(filename),
		)

	switch ext {

	case ".png":

		return "image/png"

	case ".jpg",
		".jpeg":

		return "image/jpeg"

	case ".webp":

		return "image/webp"

	case ".gif":

		return "image/gif"

	default:

		return "application/octet-stream"
	}
}
