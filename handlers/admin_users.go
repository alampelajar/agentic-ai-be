package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/config"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// ============================================================
// GET /api/admin/users
// ============================================================

type AgentSummary struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	TaskCount int64  `json:"task_count"`
}

type UserResponse struct {
	ID        uint           `json:"id"`
	Name      string         `json:"name"`
	Email     string         `json:"email"`
	Avatar    string         `json:"avatar"`
	Role      string         `json:"role"`
	Agents    []AgentSummary `json:"agents"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

func getAgentsUsedByUser(userID uint) ([]AgentSummary, error) {
	var agents []AgentSummary

	err := config.DB.
		Table("tasks AS t").
		Select(`
			a.id,
			a.name,
			a.slug,
			COUNT(t.id) AS task_count
		`).
		Joins(`
			INNER JOIN agents AS a
				ON a.id = t.agent_id
		`).
		Where(
			"t.user_id = ?",
			userID,
		).
		Where(
			"a.is_active = ?",
			true,
		).
		Group(
			"a.id, a.name, a.slug",
		).
		Order(
			"a.name ASC",
		).
		Scan(&agents).
		Error

	if err != nil {
		return nil, err
	}

	if agents == nil {
		agents = make([]AgentSummary, 0)
	}

	return agents, nil
}

func AdminGetUsers(c *gin.Context) {
	var users []models.User

	if err := config.DB.
		Order("id ASC").
		Find(&users).Error; err != nil {

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengambil data users",
		})
		return
	}

	result := make([]UserResponse, 0, len(users))

	for _, user := range users {
		agents, err := getAgentsUsedByUser(user.ID)

		if err != nil {
			c.Error(err)

			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal mengambil data agent user",
			})
			return
		}

		result = append(result, UserResponse{
			ID:        user.ID,
			Name:      user.Name,
			Email:     user.Email,
			Avatar:    user.Avatar,
			Role:      user.Role,
			Agents:    agents,
			CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt: user.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"users": result,
	})
}

// ============================================================
// POST /api/admin/users
// ============================================================

type AdminCreateUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func AdminCreateUser(c *gin.Context) {
	var req AdminCreateUserRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Data request tidak valid",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))

	// --------------------------------------------------------
	// Validasi nama
	// --------------------------------------------------------

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Nama wajib diisi",
		})
		return
	}

	// --------------------------------------------------------
	// Validasi email
	// --------------------------------------------------------

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email wajib diisi",
		})
		return
	}

	// --------------------------------------------------------
	// Validasi password
	// --------------------------------------------------------

	if req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Password wajib diisi",
		})
		return
	}

	if code, message := services.PasswordProblem(req.Password, req.Email, req.Name); code != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"code":    code,
			"message": message,
		})
		return
	}

	// --------------------------------------------------------
	// Default role
	// --------------------------------------------------------

	if req.Role == "" {
		req.Role = "user"
	}

	// Backend hanya mendukung user dan admin
	if req.Role != "user" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Role harus user atau admin",
		})
		return
	}

	// --------------------------------------------------------
	// Cek email
	// --------------------------------------------------------

	var existingUser models.User

	err := config.DB.
		Where("email = ?", req.Email).
		First(&existingUser).
		Error

	if err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Email sudah digunakan",
		})
		return
	}

	if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengecek email",
		})
		return
	}

	// --------------------------------------------------------
	// Hash password
	// --------------------------------------------------------

	hashedPassword, err := bcrypt.GenerateFromPassword(
		[]byte(req.Password),
		bcrypt.DefaultCost,
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat password",
		})
		return
	}

	// --------------------------------------------------------
	// Buat user
	// --------------------------------------------------------

	user := models.User{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     req.Role,
	}

	if err := config.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal membuat user",
		})
		return
	}

	recordAudit(c, "user.created_by_admin", "user", strconv.FormatUint(uint64(user.ID), 10), user.Name,
		"Akun \""+user.Email+"\" dengan role "+user.Role+" dibuat oleh administrator.")

	c.JSON(http.StatusCreated, gin.H{
		"ok":      true,
		"message": "User berhasil dibuat",
		"user": gin.H{
			"id":         user.ID,
			"name":       user.Name,
			"email":      user.Email,
			"avatar":     user.Avatar,
			"role":       user.Role,
			"created_at": user.CreatedAt.Format("2006-01-02 15:04:05"),
			"updated_at": user.UpdatedAt.Format("2006-01-02 15:04:05"),
		},
	})
}

// ============================================================
// PUT /api/admin/users/:id
// ============================================================

type AdminUpdateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func AdminUpdateUser(c *gin.Context) {
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 64)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "ID user tidak valid",
		})
		return
	}

	var req AdminUpdateUserRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Data request tidak valid",
		})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))

	// --------------------------------------------------------
	// Validasi nama
	// --------------------------------------------------------

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Nama wajib diisi",
		})
		return
	}

	// --------------------------------------------------------
	// Validasi email
	// --------------------------------------------------------

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Email wajib diisi",
		})
		return
	}

	// --------------------------------------------------------
	// Validasi role
	// --------------------------------------------------------

	if req.Role != "user" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Role harus user atau admin",
		})
		return
	}

	// --------------------------------------------------------
	// Cari user
	// --------------------------------------------------------

	var user models.User

	if err := config.DB.
		First(&user, uint(userID)).
		Error; err != nil {

		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"ok":      false,
				"message": "User tidak ditemukan",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengambil data user",
		})
		return
	}

	// --------------------------------------------------------
	// Cek email user lain
	// --------------------------------------------------------

	var emailUser models.User

	err = config.DB.
		Where(
			"email = ? AND id <> ?",
			req.Email,
			user.ID,
		).
		First(&emailUser).
		Error

	if err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Email sudah digunakan oleh user lain",
		})
		return
	}

	if err != nil && err != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengecek email",
		})
		return
	}

	// --------------------------------------------------------
	// Update user
	// --------------------------------------------------------

	// An admin cannot take away their own admin role: they would lose this page at once.
	if user.ID == c.GetUint("user_id") && user.Role == "admin" && req.Role != "admin" {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Admin tidak dapat menurunkan role akun sendiri",
		})
		return
	}

	oldName, oldEmail, oldRole := user.Name, user.Email, user.Role
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		if oldRole == "admin" && req.Role != "admin" {
			// Lock every admin row so two demotions at once cannot both see a second admin.
			var admins []models.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("role = ?", "admin").Find(&admins).Error; err != nil {
				return err
			}
			if len(admins) <= 1 {
				return errCannotDemoteLastAdmin
			}
		}

		user.Name = req.Name
		user.Email = req.Email
		user.Role = req.Role
		if err := tx.Save(&user).Error; err != nil {
			return err
		}

		// A new address ends every session: the old sign-in belonged to the old address.
		if !strings.EqualFold(oldEmail, user.Email) {
			if err := tx.Model(&models.RefreshToken{}).
				Where("user_id = ? AND revoked_at IS NULL", user.ID).
				Update("revoked_at", time.Now()).Error; err != nil {
				return err
			}
		}

		changes := userChanges(oldName, user.Name, oldEmail, user.Email, oldRole, user.Role)
		if changes == "" {
			return nil
		}
		audit := newAuditLog(c, "user.updated_by_admin", "user", strconv.FormatUint(userID, 10), user.Name,
			"Akun pengguna \""+oldEmail+"\" diubah oleh administrator: "+changes+".")
		return tx.Create(&audit).Error
	})
	if errors.Is(err, errCannotDemoteLastAdmin) {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Admin terakhir tidak dapat diturunkan menjadi user",
		})
		return
	}
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengupdate user",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "User berhasil diupdate",
		"user": gin.H{
			"id":         user.ID,
			"name":       user.Name,
			"email":      user.Email,
			"avatar":     user.Avatar,
			"role":       user.Role,
			"created_at": user.CreatedAt.Format("2006-01-02 15:04:05"),
			"updated_at": user.UpdatedAt.Format("2006-01-02 15:04:05"),
		},
	})
}

// ============================================================
// DELETE /api/admin/users/:id
// ============================================================

func AdminDeleteUser(c *gin.Context) {
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 64)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "ID user tidak valid",
		})
		return
	}

	// --------------------------------------------------------
	// Jangan izinkan admin menghapus dirinya sendiri
	// --------------------------------------------------------

	currentUserIDValue, exists := c.Get("user_id")

	if exists {
		currentUserID, ok := currentUserIDValue.(uint)

		if ok && currentUserID == uint(userID) {
			c.JSON(http.StatusBadRequest, gin.H{
				"ok":      false,
				"message": "Admin tidak dapat menghapus akun sendiri",
			})
			return
		}
	}

	// --------------------------------------------------------
	// Cari user
	// --------------------------------------------------------

	var user models.User

	if err := config.DB.
		First(&user, uint(userID)).
		Error; err != nil {

		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{
				"ok":      false,
				"message": "User tidak ditemukan",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengambil data user",
		})
		return
	}

	// --------------------------------------------------------
	// HARD DELETE
	// --------------------------------------------------------
	//
	// Unscoped() sangat penting.
	//
	// Tanpa Unscoped():
	//
	//     config.DB.Delete(&user)
	//
	// GORM akan melakukan SOFT DELETE:
	//
	//     deleted_at = waktu sekarang
	//
	// Dengan Unscoped():
	//
	//     config.DB.Unscoped().Delete(&user)
	//
	// row benar-benar dihapus dari PostgreSQL.
	// --------------------------------------------------------

	audit := newAuditLog(
		c,
		"user.deleted_by_admin",
		"user",
		strconv.FormatUint(userID, 10),
		user.Name,
		"Akun pengguna \""+user.Email+"\" dihapus oleh administrator.",
	)
	workspaceRefs := userWorkspaceRefs(user.ID)
	err = config.DB.Transaction(func(tx *gorm.DB) error {
		return purgeUserAccount(tx, &user, &audit)
	})
	if errors.Is(err, errCannotDeleteLastAdmin) {
		c.JSON(http.StatusConflict, gin.H{
			"ok":      false,
			"message": "Akun admin terakhir tidak dapat dihapus",
		})
		return
	}
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menghapus user",
		})
		return
	}
	removeWorkspaceFiles(user.ID, workspaceRefs)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "User berhasil dihapus permanen",
	})
}
var errCannotDemoteLastAdmin = errors.New("cannot demote the last admin account")

// userChanges describes what an admin changed, for the audit log, or "" when nothing changed.
func userChanges(oldName, newName, oldEmail, newEmail, oldRole, newRole string) string {
	var parts []string
	if oldName != newName {
		parts = append(parts, "nama \""+oldName+"\" menjadi \""+newName+"\"")
	}
	if !strings.EqualFold(oldEmail, newEmail) {
		parts = append(parts, "email "+oldEmail+" menjadi "+newEmail)
	}
	if oldRole != newRole {
		parts = append(parts, "role "+oldRole+" menjadi "+newRole)
	}
	return strings.Join(parts, ", ")
}
