package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/utils"
)

// comboFakeProvider answers like an OpenAI-compatible provider and records the model it was asked for.
type comboFakeProvider struct {
	server *httptest.Server
	mu     sync.Mutex
	asked  []string
	// failFor lists model ids that answer with this status instead of a completion.
	failFor map[string]int
}

func newComboFakeProvider(t *testing.T, failFor map[string]int) *comboFakeProvider {
	t.Helper()
	fp := &comboFakeProvider{failFor: failFor}
	fp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fp.mu.Lock()
		fp.asked = append(fp.asked, body.Model)
		status := fp.failFor[body.Model]
		fp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"message":"simulated %d for %s"}}`, status, body.Model)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"x","model":%q,"choices":[{"message":{"role":"assistant","content":"jawaban dari %s"},"finish_reason":"stop"}]}`, body.Model, body.Model)
	}))
	t.Cleanup(fp.server.Close)
	return fp
}

func (fp *comboFakeProvider) models() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return strings.Join(fp.asked, ",")
}

type comboFixture struct {
	db       *gorm.DB
	user     models.User
	other    models.User
	agent    models.Agent
	mine     []models.AIModel // the user's own models, on the fake provider
	system   models.AIModel
	foreign  models.AIModel // belongs to another user
	provider *comboFakeProvider
}

func newComboFixture(t *testing.T, failFor map[string]int) *comboFixture {
	t.Helper()
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	config.DB = db
	if err := migrateAll(db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_PRIVATE_PROVIDER_URLS", "true")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("AI_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	encrypted, encErr := utils.EncryptString("user-test-key")
	if encErr != nil {
		t.Fatal(encErr)
	}
	t.Setenv("COMBO_SYSTEM_DAILY_LIMIT", "")

	suffix := strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(len(t.Name()))
	f := &comboFixture{db: db, provider: newComboFakeProvider(t, failFor)}
	f.user = models.User{Name: "Combo User", Email: "combo-user-" + suffix + "@example.test", Password: "x"}
	f.other = models.User{Name: "Combo Other", Email: "combo-other-" + suffix + "@example.test", Password: "x"}
	for _, u := range []*models.User{&f.user, &f.other} {
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.agent = models.Agent{Slug: "combo-agent-" + suffix, Name: "Combo Agent", Status: "ready", IsActive: true}
	if err := db.Create(&f.agent).Error; err != nil {
		t.Fatal(err)
	}

	var comboProv models.AIProvider
	if err := db.Where("type = ? AND is_system = ?", "combo", true).First(&comboProv).Error; err != nil {
		comboProv = models.AIProvider{Name: "Combo", Slug: "combo", BaseURL: "internal://combo", Type: "combo", IsSystem: true, IsActive: true}
		if err := db.Create(&comboProv).Error; err != nil {
			t.Fatal(err)
		}
	}
	userProv := models.AIProvider{UserID: &f.user.ID, Name: "Fake", Slug: "fake-user-" + suffix, BaseURL: f.provider.server.URL, Type: "openai", IsActive: true, APIKeyEncrypted: encrypted}
	systemProv := models.AIProvider{Name: "Fake System", Slug: "fake-system-" + suffix, BaseURL: f.provider.server.URL, Type: "openai", IsSystem: true, IsActive: true}
	otherProv := models.AIProvider{UserID: &f.other.ID, Name: "Other", Slug: "fake-other-" + suffix, BaseURL: f.provider.server.URL, Type: "openai", IsActive: true}
	for _, p := range []*models.AIProvider{&userProv, &systemProv, &otherProv} {
		if err := db.Create(p).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A user's provider needs its own stored key; the helper decrypts it.
	mk := func(provider models.AIProvider, userID *uint, name, modelID string, system bool) models.AIModel {
		m := models.AIModel{ProviderID: provider.ID, UserID: userID, Name: name, ModelID: modelID, IsSystem: system, IsActive: true}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		return m
	}
	f.mine = []models.AIModel{
		mk(userProv, &f.user.ID, "Mine A", "mine-a", false),
		mk(userProv, &f.user.ID, "Mine B", "mine-b", false),
	}
	f.system = mk(systemProv, nil, "System S", "system-s", true)
	f.foreign = mk(otherProv, &f.other.ID, "Foreign", "foreign-x", false)

	t.Cleanup(func() {
		var combos []models.AIModelCombo
		db.Where("user_id IN ?", []uint{f.user.ID, f.other.ID}).Find(&combos)
		for _, combo := range combos {
			db.Where("combo_id = ?", combo.ID).Delete(&models.AIModelComboMember{})
			db.Where("ai_model_id = ?", combo.AIModelID).Delete(&models.AgentModel{})
			db.Unscoped().Delete(&models.AIModel{}, combo.AIModelID)
			db.Delete(&combo)
		}
		db.Where("user_id IN ?", []uint{f.user.ID, f.other.ID}).Delete(&models.AIModelCallLog{})
		for _, m := range append(append([]models.AIModel{}, f.mine...), f.system, f.foreign) {
			db.Unscoped().Delete(&models.AIModel{}, m.ID)
		}
		db.Unscoped().Where("id IN ?", []uint{userProv.ID, systemProv.ID, otherProv.ID}).Delete(&models.AIProvider{})
		db.Delete(&f.agent)
		db.Delete(&f.user)
		db.Delete(&f.other)
	})
	return f
}

// callAs runs a handler as the given user.
func callAs(t *testing.T, userID uint, method, path string, params gin.Params, body any, handler gin.HandlerFunc) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	c.Set("user_id", userID)
	c.Set("userID", userID)
	handler(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *comboFixture) createCombo(t *testing.T, userID uint, body map[string]any) (int, map[string]any) {
	return callAs(t, userID, http.MethodPost, "/api/ai/combos", nil, body, CreateCombo)
}

func comboOf(response map[string]any) map[string]any {
	combo, _ := response["combo"].(map[string]any)
	return combo
}

func TestComboLifecycleAndOwnership(t *testing.T) {
	f := newComboFixture(t, nil)
	members := func(ids ...uint) []map[string]any {
		out := []map[string]any{}
		for _, id := range ids {
			out = append(out, map[string]any{"ai_model_id": id})
		}
		return out
	}

	// Members may be the user's own models and system models.
	status, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Combo Saya", "strategy": "fallback", "members": members(f.mine[0].ID, f.system.ID), "agent_ids": []uint{f.agent.ID}})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, resp)
	}
	combo := comboOf(resp)
	if combo["name"] != "Combo Saya" || len(combo["members"].([]any)) != 2 {
		t.Fatalf("combo = %v", combo)
	}

	// ...but not another user's model.
	status, resp = f.createCombo(t, f.user.ID, map[string]any{"name": "Curang", "members": members(f.mine[0].ID, f.foreign.ID), "agent_ids": []uint{f.agent.ID}})
	if status != http.StatusBadRequest {
		t.Fatalf("another user's model must be refused, got %d %v", status, resp)
	}

	// A combo cannot contain a combo.
	virtualID := uint(combo["ai_model_id"].(float64))
	status, _ = f.createCombo(t, f.user.ID, map[string]any{"name": "Nested", "members": members(virtualID), "agent_ids": []uint{f.agent.ID}})
	if status != http.StatusBadRequest {
		t.Fatalf("nesting must be refused, got %d", status)
	}

	// Invalid input.
	for name, body := range map[string]map[string]any{
		"no name":       {"members": members(f.mine[0].ID), "agent_ids": []uint{f.agent.ID}},
		"no members":    {"name": "x", "members": []any{}, "agent_ids": []uint{f.agent.ID}},
		"bad strategy":  {"name": "x", "strategy": "random", "members": members(f.mine[0].ID), "agent_ids": []uint{f.agent.ID}},
		"no agent":      {"name": "x", "members": members(f.mine[0].ID)},
		"duplicate":     {"name": "x", "members": members(f.mine[0].ID, f.mine[0].ID), "agent_ids": []uint{f.agent.ID}},
		"too many":      {"name": "x", "members": members(f.mine[0].ID, f.mine[1].ID, f.system.ID, f.foreign.ID, 99999), "agent_ids": []uint{f.agent.ID}},
		"unknown agent": {"name": "x", "members": members(f.mine[0].ID), "agent_ids": []uint{99999999}},
	} {
		if status, resp := f.createCombo(t, f.user.ID, body); status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %v", name, status, resp)
		}
	}

	// The virtual model shows up for the owner (and only for the owner) next to the normal models.
	status, resp = callAs(t, f.user.ID, http.MethodGet, "/api/ai/models", nil, nil, GetAIModels)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(resp["models"]), "Combo Saya") {
		t.Fatalf("the owner must see the combo as a model: %d", status)
	}
	_, resp = callAs(t, f.other.ID, http.MethodGet, "/api/ai/models", nil, nil, GetAIModels)
	if strings.Contains(fmt.Sprint(resp["models"]), "Combo Saya") {
		t.Fatal("another user must not see the combo")
	}

	// The generic model endpoints leave a combo's model alone.
	status, _ = callAs(t, f.user.ID, http.MethodDelete, "/api/ai/models/x", gin.Params{{Key: "id", Value: strconv.Itoa(int(virtualID))}}, nil, DeleteAIModel)
	if status != http.StatusNotFound {
		t.Fatalf("a combo is not deleted through the model endpoint, got %d", status)
	}

	// Another user cannot read, change or delete it.
	comboID := strconv.Itoa(int(combo["id"].(float64)))
	for name, run := range map[string]func() int{
		"update": func() int {
			s, _ := callAs(t, f.other.ID, http.MethodPut, "/", gin.Params{{Key: "id", Value: comboID}}, map[string]any{"name": "dicuri"}, UpdateCombo)
			return s
		},
		"delete": func() int {
			s, _ := callAs(t, f.other.ID, http.MethodDelete, "/", gin.Params{{Key: "id", Value: comboID}}, nil, DeleteCombo)
			return s
		},
	} {
		if got := run(); got != http.StatusNotFound {
			t.Errorf("%s by another user: want 404, got %d", name, got)
		}
	}

	// Update: rename, switch strategy, change members.
	status, resp = callAs(t, f.user.ID, http.MethodPut, "/", gin.Params{{Key: "id", Value: comboID}},
		map[string]any{"name": "Combo Baru", "strategy": "round_robin", "members": members(f.mine[1].ID, f.mine[0].ID)}, UpdateCombo)
	if status != http.StatusOK || comboOf(resp)["strategy"] != "round_robin" || comboOf(resp)["name"] != "Combo Baru" {
		t.Fatalf("update: %d %v", status, resp)
	}

	// Deleting a model that is a member takes it out of the combo.
	status, _ = callAs(t, f.user.ID, http.MethodDelete, "/", gin.Params{{Key: "id", Value: strconv.Itoa(int(f.mine[0].ID))}}, nil, DeleteAIModel)
	if status != http.StatusOK {
		t.Fatalf("delete member model: %d", status)
	}
	status, resp = callAs(t, f.user.ID, http.MethodGet, "/api/ai/combos", nil, nil, GetCombos)
	list := resp["combos"].([]any)
	if status != http.StatusOK || len(list[0].(map[string]any)["members"].([]any)) != 1 {
		t.Fatalf("the deleted model must leave the combo: %v", resp)
	}

	// Delete the combo and its model and links.
	status, _ = callAs(t, f.user.ID, http.MethodDelete, "/", gin.Params{{Key: "id", Value: comboID}}, nil, DeleteCombo)
	if status != http.StatusOK {
		t.Fatalf("delete: %d", status)
	}
	var left int64
	f.db.Model(&models.AgentModel{}).Where("ai_model_id = ?", virtualID).Count(&left)
	if left != 0 {
		t.Fatal("the agent link must be removed with the combo")
	}
}

func chatRequestFor(model string) chatRequest {
	return chatRequest{Model: model, Messages: []chatMessage{{Role: "user", Content: "halo"}}}
}

func (f *comboFixture) virtualModel(t *testing.T, comboResp map[string]any) models.AIModel {
	t.Helper()
	var model models.AIModel
	if err := f.db.Preload("Provider").First(&model, uint(comboOf(comboResp)["ai_model_id"].(float64))).Error; err != nil {
		t.Fatal(err)
	}
	return model
}

func TestComboFallsBackToTheNextModelAndLogsEveryAttempt(t *testing.T) {
	f := newComboFixture(t, map[string]int{"mine-a": http.StatusTooManyRequests})
	status, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Fallback", "strategy": "fallback", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}, {"ai_model_id": f.mine[1].ID}}})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, resp)
	}
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.mine[0].ID)

	answer, used, trace, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("ignored"))
	if runErr != nil {
		t.Fatalf("runErr = %+v", runErr)
	}
	if !strings.Contains(answer, "mine-b") || used.ID != f.mine[1].ID {
		t.Fatalf("the second model must answer, got %q from %d", answer, used.ID)
	}
	if f.provider.models() != "mine-a,mine-b" {
		t.Fatalf("provider was asked for %q", f.provider.models())
	}
	if len(trace) != 2 || trace[0].Status != "failed" || trace[1].Status != "ok" {
		t.Fatalf("trace = %+v", trace)
	}
	var logs int64
	f.db.Model(&models.AIModelCallLog{}).Where("user_id = ?", f.user.ID).Count(&logs)
	if logs != 2 {
		t.Fatalf("every attempt must be logged, got %d", logs)
	}
}

func TestComboRoundRobinSpreadsRequests(t *testing.T) {
	f := newComboFixture(t, nil)
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Gilir", "strategy": "round_robin", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}, {"ai_model_id": f.mine[1].ID}}})
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.mine[0].ID)
	comboHealth.Success(f.mine[1].ID)

	for i := 0; i < 4; i++ {
		if _, _, _, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x")); runErr != nil {
			t.Fatalf("run %d: %+v", i, runErr)
		}
	}
	if f.provider.models() != "mine-a,mine-b,mine-a,mine-b" {
		t.Fatalf("requests must take turns, provider saw %q", f.provider.models())
	}
}

func TestComboReportsWhenEveryModelFails(t *testing.T) {
	f := newComboFixture(t, map[string]int{"mine-a": http.StatusBadGateway, "mine-b": http.StatusBadRequest})
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Gagal", "strategy": "fallback", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}, {"ai_model_id": f.mine[1].ID}}})
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.mine[0].ID)
	comboHealth.Success(f.mine[1].ID)

	_, _, trace, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x"))
	if runErr == nil || runErr.Status != http.StatusBadGateway || len(trace) != 2 {
		t.Fatalf("runErr = %+v trace = %+v", runErr, trace)
	}
	if !strings.Contains(runErr.Detail, "Mine A") || !strings.Contains(runErr.Detail, "Mine B") {
		t.Fatalf("the detail must say what happened to each model: %q", runErr.Detail)
	}
}

func TestComboSkipsSystemModelsOnceTheDailyAllowanceIsUsed(t *testing.T) {
	f := newComboFixture(t, nil)
	t.Setenv("COMBO_SYSTEM_DAILY_LIMIT", "1")
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Kuota", "strategy": "fallback", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.system.ID}, {"ai_model_id": f.mine[0].ID}}})
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.system.ID)
	comboHealth.Success(f.mine[0].ID)

	if _, used, _, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x")); runErr != nil || used.ID != f.system.ID {
		t.Fatalf("the first request may use the system model: %+v %v", runErr, used.ID)
	}
	if _, used, _, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x")); runErr != nil || used.ID != f.mine[0].ID {
		t.Fatalf("after the allowance the user's own model must answer: %+v %v", runErr, used.ID)
	}
}

func TestComboRefusesAModelThatBecameForeignAndWhenSwitchedOff(t *testing.T) {
	f := newComboFixture(t, nil)
	_, resp := f.createCombo(t, f.user.ID, map[string]any{"name": "Cek", "strategy": "fallback", "agent_ids": []uint{f.agent.ID},
		"members": []map[string]any{{"ai_model_id": f.mine[0].ID}}})
	virtual := f.virtualModel(t, resp)
	comboHealth.Success(f.mine[0].ID)

	// Ownership is checked again when the combo runs, not only when it is saved.
	f.db.Model(&models.AIModel{}).Where("id = ?", f.mine[0].ID).Update("user_id", f.other.ID)
	if _, _, _, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x")); runErr == nil || runErr.Status != http.StatusConflict {
		t.Fatalf("a model that is no longer the user's must not be called: %+v", runErr)
	}
	if f.provider.models() != "" {
		t.Fatalf("the provider must not have been called, saw %q", f.provider.models())
	}

	f.db.Model(&models.AIModel{}).Where("id = ?", f.mine[0].ID).Update("user_id", f.user.ID)
	t.Setenv("COMBO_USER_ENABLED", "false")
	if _, _, _, runErr := runComboRequest(f.user.ID, virtual, chatRequestFor("x")); runErr == nil || runErr.Status != http.StatusForbidden {
		t.Fatalf("the admin switch must stop combos: %+v", runErr)
	}
}
