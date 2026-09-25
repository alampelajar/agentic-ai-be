package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/models"
)

var DB *gorm.DB

func ConnectDatabase() {
	host := strings.TrimSpace(os.Getenv("DB_HOST"))
	if host == "" {
		host = "127.0.0.1"
	}

	port := strings.TrimSpace(os.Getenv("DB_PORT"))
	if port == "" {
		port = "5432"
	}

	missing := make([]string, 0, 3)
	for _, name := range []string{"DB_USER", "DB_PASSWORD", "DB_NAME"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		log.Fatalf("Konfigurasi PostgreSQL belum lengkap. Isi variabel: %s", strings.Join(missing, ", "))
	}

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Jakarta",
		host,
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		port,
	)

	db, err := gorm.Open(
		postgres.Open(dsn),
		&gorm.Config{},
	)

	if err != nil {
		log.Fatal(
			"Gagal terhubung ke PostgreSQL:",
			err,
		)
	}

	DB = db

	err = DB.AutoMigrate(
		&models.User{},
		&models.RefreshToken{},
		&models.AIProvider{},
		&models.AIModel{},
		&models.Agent{},
		&models.AgentModel{},
		&models.Task{},
		&models.TaskMessage{},
	)

	if err != nil {
		log.Fatal(
			"Gagal melakukan migration:",
			err,
		)
	}

	seedAgents()
	seedSystemProviderAndModels()

	fmt.Println("PostgreSQL berhasil terhubung")
	fmt.Println("Database migration berhasil")
}

func seedAgents() {
	agents := []models.Agent{
		{
			Slug: "coding",

			Name: "Coding Agent",

			Description: "Membuat, mengubah, memperbaiki, dan mengembangkan kode aplikasi.",

			Status: "ready",

			IsActive: true,
		},
		{
			Slug: "testing",

			Name: "Testing Agent",

			Description: "Menguji aplikasi dan membantu menemukan masalah atau bug pada sistem.",

			Status: "ready",

			IsActive: true,
		},
		{
			Slug: "review",

			Name: "Code Review Agent",

			Description: "Menganalisis kode untuk menemukan masalah kualitas dan potensi kesalahan.",

			Status: "ready",

			IsActive: true,
		},
		{
			Slug: "planning",

			Name: "Planning Agent",

			Description: "Menganalisis goal dan membuat rencana pengerjaan yang terstruktur.",

			Status: "ready",

			IsActive: true,
		},
		{
			Slug: "debugging",

			Name: "Debugging Agent",

			Description: "Menganalisis error dan membantu menemukan penyebab serta solusi masalah.",

			Status: "ready",

			IsActive: true,
		},
	}

	for _, agent := range agents {
		var existing models.Agent

		result := DB.
			Where(
				"slug = ?",
				agent.Slug,
			).
			First(&existing)

		if result.Error == gorm.ErrRecordNotFound {
			if err := DB.Create(&agent).Error; err != nil {
				log.Printf(
					"Gagal membuat agent %s: %v",
					agent.Name,
					err,
				)
			}

			continue
		}

		if result.Error != nil {
			log.Printf(
				"Gagal mencari agent %s: %v",
				agent.Name,
				result.Error,
			)

			continue
		}

		existing.Name = agent.Name
		existing.Description = agent.Description
		existing.Status = agent.Status
		existing.IsActive = true

		DB.Save(&existing)
	}
}

func seedSystemProviderAndModels() {
	var provider models.AIProvider

	err := DB.
		Where(
			"slug = ? AND is_system = ?",
			"nararouter",
			true,
		).
		First(&provider).
		Error

	if err == gorm.ErrRecordNotFound {
		provider = models.AIProvider{
			UserID: nil,

			Name: "NaraRouter",

			Slug: "nararouter",

			BaseURL: "https://router.bynara.id/v1",

			Type: "nararouter",

			IsSystem: true,

			IsActive: true,
		}

		if err := DB.Create(&provider).Error; err != nil {
			log.Printf(
				"Gagal membuat provider NaraRouter: %v",
				err,
			)

			return
		}
	} else if err != nil {
		log.Printf(
			"Gagal membaca provider NaraRouter: %v",
			err,
		)

		return
	}

	systemModels := []models.AIModel{
		{
			ProviderID: provider.ID,

			UserID: nil,

			Name: "Agnes 2.5 Flash",

			ModelID: "agnes-2.5-flash",

			Description: "Model NaraRouter untuk kebutuhan AI umum.",

			IsSystem: true,

			IsActive: true,
		},
	}

	for _, item := range systemModels {
		var aiModel models.AIModel

		result := DB.
			Where(
				"provider_id = ? AND model_id = ?",
				provider.ID,
				item.ModelID,
			).
			First(&aiModel)

		if result.Error == gorm.ErrRecordNotFound {
			aiModel = item

			if err := DB.Create(&aiModel).Error; err != nil {
				log.Printf(
					"Gagal membuat model %s: %v",
					item.Name,
					err,
				)

				continue
			}
		} else if result.Error != nil {
			log.Printf(
				"Gagal membaca model %s: %v",
				item.Name,
				result.Error,
			)

			continue
		}

		assignModelToAllAgents(aiModel.ID)
	}
}

func assignModelToAllAgents(aiModelID uint) {
	var agents []models.Agent

	if err := DB.
		Where("is_active = ?", true).
		Find(&agents).
		Error; err != nil {

		log.Printf(
			"Gagal mengambil agents untuk model %d: %v",
			aiModelID,
			err,
		)

		return
	}

	for _, agent := range agents {
		link := models.AgentModel{
			AgentID: agent.ID,

			AIModelID: aiModelID,
		}

		if err := DB.
			Clauses(clause.OnConflict{
				DoNothing: true,
			}).
			Create(&link).
			Error; err != nil {

			log.Printf(
				"Gagal menghubungkan %d -> %d: %v",
				agent.ID,
				aiModelID,
				err,
			)
		}
	}
}
