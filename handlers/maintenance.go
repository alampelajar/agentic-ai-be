package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
)

const maintenanceTableSQL = `
CREATE TABLE IF NOT EXISTS system_settings (
	id BIGSERIAL PRIMARY KEY,
	maintenance_mode BOOLEAN NOT NULL DEFAULT FALSE,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

func ensureMaintenanceTable() error {
	return config.DB.Exec(maintenanceTableSQL).Error
}

// GET /api/system/maintenance
// Endpoint public untuk mengecek apakah website sedang maintenance.
func GetMaintenanceStatus(c *gin.Context) {
	if err := ensureMaintenanceTable(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyiapkan system settings",
		})
		return
	}

	var maintenance bool

	row := config.DB.
		Table("system_settings").
		Select("maintenance_mode").
		Order("id ASC").
		Limit(1).
		Row()

	if err := row.Scan(&maintenance); err != nil {
		// Belum ada row, buat default OFF.
		if err := config.DB.Exec(`
			INSERT INTO system_settings (maintenance_mode, updated_at)
			VALUES (FALSE, NOW())
		`).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal membaca status maintenance",
			})
			return
		}

		maintenance = false
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"maintenance": maintenance,
	})
}

// PUT /api/admin/settings/maintenance
// Endpoint khusus admin untuk mengaktifkan / menonaktifkan maintenance.
func UpdateMaintenanceStatus(c *gin.Context) {
	if err := ensureMaintenanceTable(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal menyiapkan system settings",
		})
		return
	}

	var req struct {
		Maintenance bool `json:"maintenance"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":      false,
			"message": "Data maintenance tidak valid",
		})
		return
	}

	var count int64

	if err := config.DB.
		Table("system_settings").
		Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":      false,
			"message": "Gagal mengecek system settings",
		})
		return
	}

	if count == 0 {
		if err := config.DB.Exec(`
			INSERT INTO system_settings (maintenance_mode, updated_at)
			VALUES (?, NOW())
		`, req.Maintenance).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal menyimpan maintenance mode",
			})
			return
		}
	} else {
		if err := config.DB.Exec(`
			UPDATE system_settings
			SET maintenance_mode = ?, updated_at = NOW()
			WHERE id = (
				SELECT id
				FROM system_settings
				ORDER BY id ASC
				LIMIT 1
			)
		`, req.Maintenance).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":      false,
				"message": "Gagal memperbarui maintenance mode",
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"message":     "Maintenance mode berhasil diperbarui",
		"maintenance": req.Maintenance,
	})
}