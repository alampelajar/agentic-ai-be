package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// ============================================================
// SETTINGS
// ============================================================

const comboProviderType = "combo"

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && value > 0 {
		return value
	}
	return fallback
}

// Users may build combos unless the admin switches the feature off (COMBO_USER_ENABLED=false).
func comboFeatureEnabled() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("COMBO_USER_ENABLED"))) != "false"
}

func comboMaxPerUser() int  { return envInt("COMBO_MAX_PER_USER", 10) }
func comboMaxMembers() int  { return envInt("COMBO_MAX_MEMBERS", 4) }
func comboSystemDaily() int { return envInt("COMBO_SYSTEM_DAILY_LIMIT", 300) }

// The state a combo needs between requests lives in memory (see services.ComboHealth).
var (
	comboHealth     = services.NewComboHealth()
	comboRoundRobin = services.NewRoundRobin()
	comboSticky     = services.NewStickyChoices()
)

func validComboStrategy(strategy string) bool {
	return strategy == models.ComboStrategyFallback || strategy == models.ComboStrategyRoundRobin || strategy == models.ComboStrategyPipeline
}

const comboStrategyMessage = "Strategi harus fallback, round_robin, atau pipeline."
const comboPipelineMembersMessage = "Combo pipeline butuh minimal dua model: yang terakhir menjawab, yang sebelumnya menulis draf."

func isComboModel(model models.AIModel) bool {
	return strings.EqualFold(strings.TrimSpace(model.Provider.Type), comboProviderType)
}

// ============================================================
// RUNNING A COMBO
// ============================================================

type comboRunError struct {
	Status  int
	Message string
	Detail  string
}

// usableComboMember says whether a member may be called for this user right now.
// It is checked on every request, not only when the combo is saved, because a model
// can be switched off or deleted afterwards.
func usableComboMember(member models.AIModelComboMember, userID uint) bool {
	model := member.AIModel
	if !member.IsEnabled || model.ID == 0 || !model.IsActive {
		return false
	}
	if !model.IsSystem && (model.UserID == nil || *model.UserID != userID) {
		return false
	}
	provider := model.Provider
	if provider.ID == 0 || !provider.IsActive || strings.EqualFold(provider.Type, comboProviderType) {
		return false
	}
	if !provider.IsSystem && (provider.UserID == nil || *provider.UserID != userID) {
		return false
	}
	return true
}

func systemCallsToday(userID uint) int64 {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var count int64
	config.DB.Model(&models.AIModelCallLog{}).
		Where("user_id = ? AND is_system = ? AND created_at >= ?", userID, true, startOfDay).
		Count(&count)
	return count
}

func runComboRequest(userID uint, taskID uint, comboModel models.AIModel, request chatRequest) (string, models.AIModel, []services.ComboAttempt, *comboRunError) {
	if !comboFeatureEnabled() {
		return "", models.AIModel{}, nil, &comboRunError{Status: http.StatusForbidden, Message: "Fitur combo sedang dinonaktifkan oleh admin."}
	}

	var combo models.AIModelCombo
	err := config.DB.
		Preload("Members", func(tx *gorm.DB) *gorm.DB { return tx.Order("position ASC, id ASC") }).
		Preload("Members.AIModel.Provider").
		Where("ai_model_id = ? AND user_id = ? AND is_active = ?", comboModel.ID, userID, true).
		First(&combo).Error
	if err != nil {
		return "", models.AIModel{}, nil, &comboRunError{Status: http.StatusNotFound, Message: "Combo tidak ditemukan atau tidak aktif."}
	}

	overQuota := systemCallsToday(userID) >= int64(comboSystemDaily())
	byID := map[uint]models.AIModel{}
	targets := make([]services.ComboTarget, 0, len(combo.Members))
	for _, member := range combo.Members {
		if !usableComboMember(member, userID) {
			continue
		}
		if overQuota && member.AIModel.IsSystem {
			continue
		}
		byID[member.AIModelID] = member.AIModel
		targets = append(targets, services.ComboTarget{ModelDBID: member.AIModelID, Name: member.AIModel.Name, IsSystem: member.AIModel.IsSystem, Weight: member.Weight})
	}
	if len(targets) == 0 {
		message := "Combo tidak punya model yang bisa dipakai. Periksa model anggotanya."
		if overQuota {
			message = "Kuota harian model sistem untuk combo sudah habis dan combo ini tidak punya model milik Anda sendiri."
		}
		return "", models.AIModel{}, nil, &comboRunError{Status: http.StatusConflict, Message: message}
	}

	if combo.Strategy == models.ComboStrategyPipeline {
		return runPipelineRequest(userID, combo, targets, byID, request)
	}

	ordered := services.OrderTargetsForTask(combo.Strategy, combo.ID, targets, comboRoundRobin, comboHealth, comboSticky, userID, taskID)
	limits := services.ComboLimits{MaxAttempts: combo.MaxAttempts, Budget: 120 * time.Second}

	outcome, runErr := services.RunCombo(ordered, limits, comboHealth, func(target services.ComboTarget, timeout time.Duration) (string, error) {
		model := byID[target.ModelDBID]
		key, keyErr := resolveProviderAPIKey(model.Provider, userID)
		if keyErr != nil {
			return "", &services.ProviderHTTPError{Status: keyErr.Status, Message: keyErr.Message}
		}
		memberRequest := request
		memberRequest.Model = model.ModelID
		// Bounded by the combo's budget, so a model that hangs leaves time for the next one.
		memberRequest.Timeout = timeout
		return callAIProviderWithRepair(key, model.Provider.BaseURL, memberRequest)
	})

	logComboAttempts(userID, combo.ID, outcome.Attempts, byID)

	if runErr != nil {
		log.Printf("[COMBO] %s gagal: %v", combo.Name, runErr)
		return "", models.AIModel{}, outcome.Attempts, &comboRunError{Status: http.StatusBadGateway, Message: "Semua model dalam combo gagal menjawab.", Detail: runErr.Error()}
	}
	if combo.Strategy == models.ComboStrategyRoundRobin {
		comboSticky.Set(userID, combo.ID, taskID, outcome.Used.ModelDBID)
	}
	return outcome.Answer, byID[outcome.Used.ModelDBID], outcome.Attempts, nil
}

func logComboAttempts(userID, comboID uint, attempts []services.ComboAttempt, byID map[uint]models.AIModel) {
	for index, attempt := range attempts {
		if attempt.Status == "skipped" {
			continue
		}
		entry := models.AIModelCallLog{
			UserID: userID, ComboID: comboID, AIModelID: attempt.ModelDBID, IsSystem: byID[attempt.ModelDBID].IsSystem,
			Attempt: index + 1, Status: attempt.Status, ErrorClass: string(attempt.ErrorClass), LatencyMS: attempt.LatencyMS,
		}
		if err := config.DB.Create(&entry).Error; err != nil {
			log.Printf("[COMBO] gagal mencatat panggilan: %v", err)
		}
	}
}

// runPipelineRequest runs a pipeline combo: the models work in position order, each one
// improving the previous draft, and the last one answers the person.
func runPipelineRequest(userID uint, combo models.AIModelCombo, stages []services.ComboTarget, byID map[uint]models.AIModel, request chatRequest) (string, models.AIModel, []services.ComboAttempt, *comboRunError) {
	limits := services.ComboLimits{Budget: 120 * time.Second}
	outcome, runErr := services.RunPipeline(stages, limits, comboHealth, func(target services.ComboTarget, final bool, draft string) (string, error) {
		model := byID[target.ModelDBID]
		key, keyErr := resolveProviderAPIKey(model.Provider, userID)
		if keyErr != nil {
			return "", &services.ProviderHTTPError{Status: keyErr.Status, Message: keyErr.Message}
		}
		stageRequest := request
		stageRequest.Model = model.ModelID
		stageRequest.Messages = pipelineMessages(request.Messages, final, draft)
		if !final {
			// Only the last model may act; the others just write.
			stageRequest.Tools = nil
			stageRequest.ToolChoice = ""
		}
		return callAIProviderWithRepair(key, model.Provider.BaseURL, stageRequest)
	})
	logComboAttempts(userID, combo.ID, outcome.Attempts, byID)
	if runErr != nil {
		log.Printf("[COMBO] %s gagal: %v", combo.Name, runErr)
		return "", models.AIModel{}, outcome.Attempts, &comboRunError{Status: http.StatusBadGateway, Message: "Model penjawab terakhir dalam combo gagal menjawab.", Detail: runErr.Error()}
	}
	return outcome.Answer, byID[outcome.Used.ModelDBID], outcome.Attempts, nil
}

// pipelineMessages adds the stage's instruction after the conversation. The messages stay as
// they are, so the models see the same conversation, tools' results and files as a single model would.
func pipelineMessages(messages []chatMessage, final bool, draft string) []chatMessage {
	out := append([]chatMessage(nil), messages...)
	draft = strings.TrimSpace(draft)
	var instruction string
	switch {
	case final && draft == "":
		return out
	case final:
		instruction = "[Bahan dari model sebelumnya]\nBerikut draf jawaban dari langkah sebelumnya:\n---\n" + draft + "\n---\n" +
			"Pakai draf ini hanya sebagai bahan. Periksa kebenarannya, perbaiki yang salah, lalu jawab pengguna langsung seperti biasa. " +
			"Jangan menyebut draf atau langkah ini."
	case draft == "":
		instruction = "[Langkah awal combo]\nTulis draf jawaban terbaik untuk permintaan di atas. Draf ini akan diperiksa dan disempurnakan oleh model berikutnya, " +
			"jadi fokus pada isi yang benar dan lengkap. Jangan memanggil tool."
	default:
		instruction = "[Langkah combo]\nBerikut draf dari model sebelumnya:\n---\n" + draft + "\n---\n" +
			"Periksa draf itu, perbaiki kesalahan, lengkapi yang kurang, lalu tulis draf yang lebih baik. Jangan memanggil tool."
	}
	return append(out, chatMessage{Role: "user", Content: instruction})
}

// ============================================================
// REQUESTS AND RESPONSES
// ============================================================

type comboMemberRequest struct {
	AIModelID uint  `json:"ai_model_id"`
	Weight    int   `json:"weight"`
	IsEnabled *bool `json:"is_enabled"`
}

type comboRequest struct {
	Name        *string               `json:"name"`
	Description *string               `json:"description"`
	Strategy    *string               `json:"strategy"`
	MaxAttempts *int                  `json:"max_attempts"`
	IsActive    *bool                 `json:"is_active"`
	Members     *[]comboMemberRequest `json:"members"`
	AgentIDs    *[]uint               `json:"agent_ids"`
}

type comboMemberResponse struct {
	AIModelID uint   `json:"ai_model_id"`
	Name      string `json:"name"`
	ModelID   string `json:"model_id"`
	Provider  string `json:"provider"`
	IsSystem  bool   `json:"is_system"`
	Position  int    `json:"position"`
	Weight    int    `json:"weight"`
	IsEnabled bool   `json:"is_enabled"`
	// Available is false when the model was switched off or removed since the combo was saved.
	Available bool `json:"available"`
}

type comboResponse struct {
	ID          uint                  `json:"id"`
	AIModelID   uint                  `json:"ai_model_id"`
	Name        string                `json:"name"`
	Slug        string                `json:"slug"`
	Description string                `json:"description"`
	Strategy    string                `json:"strategy"`
	MaxAttempts int                   `json:"max_attempts"`
	IsActive    bool                  `json:"is_active"`
	Members     []comboMemberResponse `json:"members"`
	AgentIDs    []uint                `json:"agent_ids"`
	CreatedAt   time.Time             `json:"created_at"`
}

func buildComboResponse(userID uint, combo models.AIModelCombo) comboResponse {
	response := comboResponse{
		ID: combo.ID, AIModelID: combo.AIModelID, Name: combo.Name, Slug: combo.Slug, Description: combo.Description,
		Strategy: combo.Strategy, MaxAttempts: combo.MaxAttempts, IsActive: combo.IsActive, CreatedAt: combo.CreatedAt,
		Members: []comboMemberResponse{}, AgentIDs: []uint{},
	}
	for _, member := range combo.Members {
		response.Members = append(response.Members, comboMemberResponse{
			AIModelID: member.AIModelID, Name: member.AIModel.Name, ModelID: member.AIModel.ModelID, Provider: member.AIModel.Provider.Name,
			IsSystem: member.AIModel.IsSystem, Position: member.Position, Weight: member.Weight, IsEnabled: member.IsEnabled,
			Available: usableComboMember(member, userID),
		})
	}
	var links []models.AgentModel
	config.DB.Where("ai_model_id = ?", combo.AIModelID).Find(&links)
	for _, link := range links {
		response.AgentIDs = append(response.AgentIDs, link.AgentID)
	}
	sort.Slice(response.AgentIDs, func(i, j int) bool { return response.AgentIDs[i] < response.AgentIDs[j] })
	return response
}

func loadUserCombo(userID uint, id string) (models.AIModelCombo, error) {
	var combo models.AIModelCombo
	err := config.DB.
		Preload("Members", func(tx *gorm.DB) *gorm.DB { return tx.Order("position ASC, id ASC") }).
		Preload("Members.AIModel.Provider").
		Where("id = ? AND user_id = ?", id, userID).
		First(&combo).Error
	return combo, err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func comboSlug(name string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "combo"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}

// validateComboMembers checks the members against what this user may use and
// returns them in order, or a message for the person.
func validateComboMembers(userID uint, requested []comboMemberRequest) ([]models.AIModelComboMember, string) {
	if len(requested) == 0 {
		return nil, "Combo harus punya minimal satu model."
	}
	if len(requested) > comboMaxMembers() {
		return nil, fmt.Sprintf("Combo hanya boleh berisi sampai %d model.", comboMaxMembers())
	}
	seen := map[uint]bool{}
	members := make([]models.AIModelComboMember, 0, len(requested))
	for index, item := range requested {
		if item.AIModelID == 0 || seen[item.AIModelID] {
			return nil, "Model dalam combo tidak valid atau dobel."
		}
		seen[item.AIModelID] = true
		var model models.AIModel
		err := config.DB.Preload("Provider").
			Where("id = ? AND is_active = ? AND (is_system = ? OR user_id = ?)", item.AIModelID, true, true, userID).
			First(&model).Error
		if err != nil || strings.EqualFold(model.Provider.Type, comboProviderType) || !model.Provider.IsActive {
			return nil, "Salah satu model tidak ditemukan, tidak aktif, atau bukan milik Anda. Combo tidak boleh berisi combo lain."
		}
		weight := item.Weight
		if weight == 0 {
			weight = 1
		}
		if weight < 1 || weight > 10 {
			return nil, "Bobot model harus antara 1 dan 10."
		}
		enabled := true
		if item.IsEnabled != nil {
			enabled = *item.IsEnabled
		}
		members = append(members, models.AIModelComboMember{AIModelID: item.AIModelID, Position: index, Weight: weight, IsEnabled: enabled})
	}
	return members, ""
}

func validComboAgents(userID uint, agentIDs []uint) ([]uint, string) {
	unique := []uint{}
	seen := map[uint]bool{}
	for _, id := range agentIDs {
		if id != 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return nil, "Pilih minimal satu Agent tempat combo ini dipakai."
	}
	var count int64
	config.DB.Model(&models.Agent{}).
		Where("id IN ? AND is_active = ? AND (user_id IS NULL OR user_id = ?)", unique, true, userID).
		Count(&count)
	if int(count) != len(unique) {
		return nil, "Salah satu Agent tidak ditemukan atau tidak aktif."
	}
	return unique, ""
}

func comboProvider(tx *gorm.DB) (models.AIProvider, error) {
	var provider models.AIProvider
	err := tx.Where("type = ? AND is_system = ?", comboProviderType, true).First(&provider).Error
	return provider, err
}

func linkComboToAgents(tx *gorm.DB, aiModelID uint, agentIDs []uint) error {
	if err := tx.Where("ai_model_id = ?", aiModelID).Delete(&models.AgentModel{}).Error; err != nil {
		return err
	}
	for _, agentID := range agentIDs {
		if err := tx.Create(&models.AgentModel{AgentID: agentID, AIModelID: aiModelID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func comboErrorJSON(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"ok": false, "error": message})
}

// ============================================================
// HANDLERS
// ============================================================

func GetCombos(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		comboErrorJSON(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	var combos []models.AIModelCombo
	err := config.DB.
		Preload("Members", func(tx *gorm.DB) *gorm.DB { return tx.Order("position ASC, id ASC") }).
		Preload("Members.AIModel.Provider").
		Where("user_id = ?", userID).Order("id ASC").Find(&combos).Error
	if err != nil {
		comboErrorJSON(c, http.StatusInternalServerError, "Gagal mengambil combo.")
		return
	}
	result := make([]comboResponse, 0, len(combos))
	for _, combo := range combos {
		result = append(result, buildComboResponse(userID, combo))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "combos": result, "enabled": comboFeatureEnabled(), "limits": gin.H{"max_combos": comboMaxPerUser(), "max_members": comboMaxMembers()}})
}

func CreateCombo(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		comboErrorJSON(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	if !comboFeatureEnabled() {
		comboErrorJSON(c, http.StatusForbidden, "Fitur combo sedang dinonaktifkan oleh admin.")
		return
	}
	var req comboRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		comboErrorJSON(c, http.StatusBadRequest, "Data combo tidak valid.")
		return
	}

	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" || len(name) > 100 {
		comboErrorJSON(c, http.StatusBadRequest, "Nama combo wajib diisi (maksimal 100 karakter).")
		return
	}
	strategy := models.ComboStrategyFallback
	if req.Strategy != nil && strings.TrimSpace(*req.Strategy) != "" {
		strategy = strings.TrimSpace(*req.Strategy)
	}
	if !validComboStrategy(strategy) {
		comboErrorJSON(c, http.StatusBadRequest, comboStrategyMessage)
		return
	}
	var requested []comboMemberRequest
	if req.Members != nil {
		requested = *req.Members
	}
	if strategy == models.ComboStrategyPipeline && len(requested) < 2 {
		comboErrorJSON(c, http.StatusBadRequest, comboPipelineMembersMessage)
		return
	}
	members, message := validateComboMembers(userID, requested)
	if message != "" {
		comboErrorJSON(c, http.StatusBadRequest, message)
		return
	}
	var agentIDs []uint
	if req.AgentIDs != nil {
		agentIDs = *req.AgentIDs
	}
	agentIDs, message = validComboAgents(userID, agentIDs)
	if message != "" {
		comboErrorJSON(c, http.StatusBadRequest, message)
		return
	}
	maxAttempts := 3
	if req.MaxAttempts != nil {
		maxAttempts = *req.MaxAttempts
	}
	if maxAttempts < 1 || maxAttempts > comboMaxMembers() {
		comboErrorJSON(c, http.StatusBadRequest, fmt.Sprintf("Percobaan maksimal harus antara 1 dan %d.", comboMaxMembers()))
		return
	}
	if maxAttempts > len(members) {
		maxAttempts = len(members)
	}
	description := ""
	if req.Description != nil {
		description = strings.TrimSpace(*req.Description)
	}

	var created models.AIModelCombo
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.AIModelCombo{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if int(count) >= comboMaxPerUser() {
			return fmt.Errorf("limit: Anda sudah memakai batas %d combo", comboMaxPerUser())
		}
		provider, err := comboProvider(tx)
		if err != nil {
			return fmt.Errorf("provider combo belum tersedia: %w", err)
		}
		slug := comboSlug(name)
		for suffix := 2; ; suffix++ {
			var taken int64
			tx.Model(&models.AIModelCombo{}).Where("user_id = ? AND slug = ?", userID, slug).Count(&taken)
			if taken == 0 {
				break
			}
			slug = fmt.Sprintf("%s-%d", comboSlug(name), suffix)
		}
		virtual := models.AIModel{
			ProviderID: provider.ID, UserID: &userID, Name: name, ModelID: fmt.Sprintf("combo/%d-%s", userID, slug),
			Description: description, IsSystem: false, IsActive: true,
		}
		if err := tx.Create(&virtual).Error; err != nil {
			return err
		}
		created = models.AIModelCombo{
			UserID: userID, AIModelID: virtual.ID, Name: name, Slug: slug, Description: description,
			Strategy: strategy, MaxAttempts: maxAttempts, IsActive: true,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		for i := range members {
			members[i].ComboID = created.ID
		}
		if err := tx.Create(&members).Error; err != nil {
			return err
		}
		return linkComboToAgents(tx, virtual.ID, agentIDs)
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "limit: ") {
			comboErrorJSON(c, http.StatusConflict, strings.TrimPrefix(err.Error(), "limit: ")+".")
			return
		}
		log.Printf("[COMBO] gagal membuat combo: %v", err)
		comboErrorJSON(c, http.StatusInternalServerError, "Gagal membuat combo.")
		return
	}
	loaded, _ := loadUserCombo(userID, strconv.Itoa(int(created.ID)))
	c.JSON(http.StatusCreated, gin.H{"ok": true, "combo": buildComboResponse(userID, loaded)})
}

func UpdateCombo(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		comboErrorJSON(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	combo, err := loadUserCombo(userID, c.Param("id"))
	if err != nil {
		comboErrorJSON(c, http.StatusNotFound, "Combo tidak ditemukan.")
		return
	}
	var req comboRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		comboErrorJSON(c, http.StatusBadRequest, "Data combo tidak valid.")
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 100 {
			comboErrorJSON(c, http.StatusBadRequest, "Nama combo wajib diisi (maksimal 100 karakter).")
			return
		}
		combo.Name = name
	}
	if req.Description != nil {
		combo.Description = strings.TrimSpace(*req.Description)
	}
	if req.Strategy != nil {
		strategy := strings.TrimSpace(*req.Strategy)
		if !validComboStrategy(strategy) {
			comboErrorJSON(c, http.StatusBadRequest, comboStrategyMessage)
			return
		}
		combo.Strategy = strategy
	}
	if req.IsActive != nil {
		combo.IsActive = *req.IsActive
	}
	var newMembers []models.AIModelComboMember
	if req.Members != nil {
		var message string
		newMembers, message = validateComboMembers(userID, *req.Members)
		if message != "" {
			comboErrorJSON(c, http.StatusBadRequest, message)
			return
		}
	}
	var newAgents []uint
	if req.AgentIDs != nil {
		var message string
		newAgents, message = validComboAgents(userID, *req.AgentIDs)
		if message != "" {
			comboErrorJSON(c, http.StatusBadRequest, message)
			return
		}
	}
	memberCount := len(combo.Members)
	if newMembers != nil {
		memberCount = len(newMembers)
	}
	if combo.Strategy == models.ComboStrategyPipeline && memberCount < 2 {
		comboErrorJSON(c, http.StatusBadRequest, comboPipelineMembersMessage)
		return
	}
	if req.MaxAttempts != nil {
		if *req.MaxAttempts < 1 || *req.MaxAttempts > comboMaxMembers() {
			comboErrorJSON(c, http.StatusBadRequest, fmt.Sprintf("Percobaan maksimal harus antara 1 dan %d.", comboMaxMembers()))
			return
		}
		combo.MaxAttempts = *req.MaxAttempts
	}
	if combo.MaxAttempts > memberCount {
		combo.MaxAttempts = memberCount
	}

	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.AIModelCombo{}).Where("id = ?", combo.ID).Updates(map[string]any{
			"name": combo.Name, "description": combo.Description, "strategy": combo.Strategy,
			"max_attempts": combo.MaxAttempts, "is_active": combo.IsActive,
		}).Error; err != nil {
			return err
		}
		// The virtual model follows the combo, so the model lists show the same name and state.
		if err := tx.Model(&models.AIModel{}).Where("id = ?", combo.AIModelID).Updates(map[string]any{
			"name": combo.Name, "description": combo.Description, "is_active": combo.IsActive,
		}).Error; err != nil {
			return err
		}
		if newMembers != nil {
			if err := tx.Where("combo_id = ?", combo.ID).Delete(&models.AIModelComboMember{}).Error; err != nil {
				return err
			}
			for i := range newMembers {
				newMembers[i].ComboID = combo.ID
			}
			if err := tx.Create(&newMembers).Error; err != nil {
				return err
			}
		}
		if newAgents != nil {
			return linkComboToAgents(tx, combo.AIModelID, newAgents)
		}
		return nil
	})
	if err != nil {
		log.Printf("[COMBO] gagal memperbarui combo: %v", err)
		comboErrorJSON(c, http.StatusInternalServerError, "Gagal memperbarui combo.")
		return
	}
	loaded, _ := loadUserCombo(userID, strconv.Itoa(int(combo.ID)))
	c.JSON(http.StatusOK, gin.H{"ok": true, "combo": buildComboResponse(userID, loaded)})
}

func DeleteCombo(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		comboErrorJSON(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	combo, err := loadUserCombo(userID, c.Param("id"))
	if err != nil {
		comboErrorJSON(c, http.StatusNotFound, "Combo tidak ditemukan.")
		return
	}
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("ai_model_id = ?", combo.AIModelID).Delete(&models.AgentModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("combo_id = ?", combo.ID).Delete(&models.AIModelComboMember{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&models.AIModelCombo{}, combo.ID).Error; err != nil {
			return err
		}
		return tx.Delete(&models.AIModel{}, combo.AIModelID).Error
	})
	if err != nil {
		log.Printf("[COMBO] gagal menghapus combo: %v", err)
		comboErrorJSON(c, http.StatusInternalServerError, "Gagal menghapus combo.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// removeModelFromCombos keeps combos honest when a model is deleted: it leaves them,
// and a combo left with no member is switched off.
func removeModelFromCombos(tx *gorm.DB, aiModelID uint) {
	var memberships []models.AIModelComboMember
	tx.Where("ai_model_id = ?", aiModelID).Find(&memberships)
	if len(memberships) == 0 {
		return
	}
	tx.Where("ai_model_id = ?", aiModelID).Delete(&models.AIModelComboMember{})
	for _, membership := range memberships {
		var left int64
		tx.Model(&models.AIModelComboMember{}).Where("combo_id = ?", membership.ComboID).Count(&left)
		if left == 0 {
			var combo models.AIModelCombo
			if tx.First(&combo, membership.ComboID).Error == nil {
				tx.Model(&models.AIModelCombo{}).Where("id = ?", combo.ID).Update("is_active", false)
				tx.Model(&models.AIModel{}).Where("id = ?", combo.AIModelID).Update("is_active", false)
			}
		}
	}
}

// usesSystemModel is true when any of the models a chat may call is a shared system model.
func usesSystemModel(candidates []models.AIModel) bool {
	for _, model := range candidates {
		if model.IsSystem {
			return true
		}
	}
	return false
}

