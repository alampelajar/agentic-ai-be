// Command builder-samples makes the example website of every design direction, so
// people can see a look before they choose it (WEBSITE-BUILDER.md, R2).
//
//	go run ./cmd/builder-samples -create     queue one create run per look that has no example yet
//	go run ./cmd/builder-samples -collect    copy every finished example to SITE_DATA_ROOT/_samples
//
// The examples are ordinary sites of one system account, made by the agent worker
// and checked by the execution worker like any other site, so both must be running
// for -create. -collect can run any time; run it again after the runs finish.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/models"
)

const sampleEmail = "contoh-website@builder.local"

// sample is the made-up business an example website shows. Nothing here claims a
// real business; the names are invented for the example.
type sample struct {
	name, tagline, about string
	contact              sitebuilder.Contact
	home, second         []string
	secondTitle          string
}

var samples = map[string]sample{
	"warm-editorial":  {"Kopi & Roti Senja", "Kafe kecil di sudut kota", "Kafe dengan kopi seduh manual dan roti yang dipanggang setiap pagi di Yogyakarta.", sitebuilder.Contact{WhatsApp: "0812 0000 0001", Address: "Jl. Contoh No. 1, Yogyakarta", Hours: "Setiap hari, 07.00 sampai 21.00"}, []string{"Pembuka dengan judul besar tentang kopi pagi", "Menu kopi dan roti dalam daftar bergaris", "Cerita singkat tentang kafe", "Ajakan datang dan pesan lewat WhatsApp"}, []string{"Daftar menu lengkap per kelompok", "Jam buka dan alamat", "Formulir pesan tempat"}, "Menu"},
	"bold-industrial": {"Bengkel Maju Jaya", "Servis motor cepat dan jujur", "Bengkel motor di Bandung untuk servis rutin, ganti oli, dan perbaikan mesin.", sitebuilder.Contact{WhatsApp: "0812 0000 0002", Address: "Jl. Contoh No. 2, Bandung", Hours: "Senin sampai Sabtu, 08.00 sampai 17.00"}, []string{"Pembuka tegas dengan jam buka dan tombol WhatsApp", "Empat layanan dalam kotak bernomor", "Tiga langkah servis", "Ajakan menghubungi"}, []string{"Daftar layanan lengkap", "Alamat dan jam buka", "Formulir pesan jadwal servis"}, "Layanan"},
	"quiet-luxury":    {"Salon Anggun", "Rambut dan wajah yang dirawat dengan tenang", "Salon kecantikan di Jakarta untuk potong rambut, perawatan wajah, dan rias.", sitebuilder.Contact{WhatsApp: "0812 0000 0003", Address: "Jl. Contoh No. 3, Jakarta", Hours: "Selasa sampai Minggu, 10.00 sampai 20.00"}, []string{"Pembuka tenang dengan satu kalimat", "Layanan dalam dua kolom", "Kalimat tentang pengalaman di salon", "Ajakan membuat janji"}, []string{"Daftar layanan", "Alamat dan jam buka", "Formulir janji temu"}, "Layanan"},
	"fresh-clinic":    {"Klinik Gigi Senyum", "Periksa gigi tanpa rasa takut", "Klinik gigi kecil di Surabaya untuk pemeriksaan, pembersihan karang, dan tambal gigi.", sitebuilder.Contact{WhatsApp: "0812 0000 0004", Address: "Jl. Contoh No. 4, Surabaya", Hours: "Senin sampai Jumat, 09.00 sampai 18.00"}, []string{"Pembuka dengan jam praktik di samping", "Layanan dalam kartu dengan ikon", "Pertanyaan yang sering ditanyakan", "Ajakan membuat janji"}, []string{"Formulir janji temu", "Alamat, telepon, dan jam praktik"}, "Janji Temu"},
	"playful-pop":     {"Taman Bermain Ceria", "Tempat bermain dan belajar anak", "Kelompok bermain anak usia 3 sampai 6 tahun di Malang.", sitebuilder.Contact{WhatsApp: "0812 0000 0005", Address: "Jl. Contoh No. 5, Malang", Hours: "Senin sampai Jumat, 07.30 sampai 12.00"}, []string{"Pembuka ceria dengan satu kata yang menonjol", "Tiga kegiatan dalam kartu", "Jadwal harian", "Ajakan berkunjung"}, []string{"Cara mendaftar", "Formulir pendaftaran", "Alamat"}, "Daftar"},
	"nordic-minimal":  {"Kayu Rumah", "Mebel kayu untuk rumah yang tenang", "Pembuat mebel kayu di Jepara: meja, kursi, dan lemari pesanan.", sitebuilder.Contact{WhatsApp: "0812 0000 0006", Address: "Jl. Contoh No. 6, Jepara", Hours: "Senin sampai Sabtu, 08.00 sampai 16.00"}, []string{"Pembuka dengan judul di kiri", "Contoh karya dalam kotak sama besar", "Tiga prinsip kerja", "Ajakan memesan"}, []string{"Cara memesan", "Formulir pesanan", "Alamat bengkel kayu"}, "Pesan"},
	"night-studio":    {"Lensa Malam", "Foto pernikahan dan acara di Bali", "Fotografer untuk pernikahan, lamaran, dan acara keluarga di Bali.", sitebuilder.Contact{WhatsApp: "0812 0000 0007", Email: "halo@contoh.test"}, []string{"Pembuka dengan judul sangat besar", "Daftar jenis acara dalam baris besar", "Cara kerja", "Ajakan menghubungi dengan tautan besar"}, []string{"Paket acara tanpa harga", "Formulir menanyakan tanggal"}, "Pesan Tanggal"},
	"heritage-serif":  {"Kantor Hukum Pratama", "Pendampingan hukum untuk keluarga dan usaha", "Kantor hukum di Medan untuk perkara keluarga, tanah, dan kontrak usaha.", sitebuilder.Contact{Phone: "061 0000 0008", Email: "kantor@contoh.test", Address: "Jl. Contoh No. 8, Medan", Hours: "Senin sampai Jumat, 09.00 sampai 17.00"}, []string{"Pembuka dengan tiga bidang di panel berbingkai", "Bidang layanan dalam dua kolom", "Tentang kantor", "Ajakan konsultasi"}, []string{"Formulir konsultasi", "Alamat dan jam kantor"}, "Konsultasi"},
	"tropical-sun":    {"Villa Pantai Lovina", "Menginap tenang dekat pantai", "Villa tiga kamar dekat Pantai Lovina, Bali, untuk keluarga dan teman.", sitebuilder.Contact{WhatsApp: "0812 0000 0009", Address: "Jl. Contoh No. 9, Lovina, Bali"}, []string{"Pembuka dalam panel membulat", "Kamar dan fasilitas dalam kartu", "Tiga langkah memesan", "Informasi lokasi"}, []string{"Formulir menanyakan tanggal menginap", "Lokasi dan cara datang"}, "Pesan"},
	"tech-precise":    {"Solusi Digital Nusantara", "Jasa komputer dan jaringan untuk kantor kecil", "Jasa pemasangan jaringan, perawatan komputer, dan bantuan teknologi untuk kantor kecil di Bandung.", sitebuilder.Contact{WhatsApp: "0812 0000 0010", Email: "halo@contoh.test"}, []string{"Pembuka dengan daftar layanan di panel", "Layanan dalam kotak berbagai ukuran", "Cara kerja dalam langkah bernomor", "Ajakan menghubungi"}, []string{"Formulir permintaan bantuan", "Kontak"}, "Hubungi"},
	"handmade-craft":  {"Rajut Bu Rina", "Rajutan buatan tangan dari Solo", "Tas, topi, dan boneka rajut buatan tangan, dibuat satu per satu di Solo.", sitebuilder.Contact{WhatsApp: "0812 0000 0011", Address: "Solo, Jawa Tengah"}, []string{"Pembuka dalam bingkai putus-putus", "Produk dalam kartu", "Cara membuat dalam garis waktu", "Catatan dari pembuat"}, []string{"Cara memesan", "Formulir pesanan"}, "Pesan"},
	"street-market":   {"Toko Sembako Berkah", "Belanja kebutuhan dapur, antar ke rumah", "Toko sembako di Depok: beras, minyak, telur, dan kebutuhan dapur, bisa diantar.", sitebuilder.Contact{WhatsApp: "0812 0000 0012", Address: "Jl. Contoh No. 12, Depok", Hours: "Setiap hari, 06.00 sampai 21.00"}, []string{"Pembuka dengan pesanan lewat WhatsApp", "Kelompok barang dalam kotak", "Tiga langkah memesan", "Antar, bayar, dan jam buka dalam tabel"}, []string{"Daftar barang per kelompok", "Formulir pesanan"}, "Belanja"},
}

func main() {
	create := flag.Bool("create", false, "queue a create run for every look without an example")
	collect := flag.Bool("collect", false, "copy finished examples to the samples folder")
	flag.Parse()
	if !*create && !*collect {
		flag.Usage()
		os.Exit(2)
	}
	_ = godotenv.Load()
	config.ConnectDatabase()
	db := config.DB

	var user models.User
	if err := db.Where("email = ?", sampleEmail).FirstOrCreate(&user, models.User{Name: "Contoh Website", Email: sampleEmail, Role: "user"}).Error; err != nil {
		log.Fatalf("sample account: %v", err)
	}
	settings := sitebuilder.LoadSettings()

	if *create {
		root := strings.TrimSpace(os.Getenv("CLOUD_WORKSPACE_ROOT"))
		if root == "" {
			root = "data/workspaces"
		}
		storage, err := cloudworkspace.NewLocalDevelopmentStorage(root)
		if err != nil {
			log.Fatalf("workspace storage: %v", err)
		}
		for _, direction := range sitebuilder.Directions() {
			var existing models.Site
			if db.Where("user_id = ? AND design_direction = ?", user.ID, direction.Key).First(&existing).Error == nil {
				var active int64
				db.Model(&models.AgentRun{}).Where("site_id = ? AND status IN ?", existing.ID, []string{models.RunQueued, models.RunRunning}).Count(&active)
				if existing.CurrentVersionID != nil || active > 0 {
					fmt.Printf("%-16s already has an example site (%d)\n", direction.Key, existing.ID)
					continue
				}
				// The earlier run did not finish: try again.
				if err := db.Create(&models.AgentRun{SiteID: existing.ID, UserID: user.ID, Kind: models.AgentRunCreate, Status: models.RunQueued, Background: true}).Error; err != nil {
					log.Fatalf("%s: %v", direction.Key, err)
				}
				fmt.Printf("%-16s queued again\n", direction.Key)
				continue
			}
			if err := queue(db, storage, user.ID, direction); err != nil {
				log.Fatalf("%s: %v", direction.Key, err)
			}
			fmt.Printf("%-16s queued\n", direction.Key)
		}
	}

	if *collect {
		var sites []models.Site
		db.Where("user_id = ?", user.ID).Find(&sites)
		for _, site := range sites {
			if site.CurrentVersionID == nil {
				fmt.Printf("%-16s not finished yet\n", site.DesignDirection)
				continue
			}
			var version models.SiteVersion
			db.First(&version, *site.CurrentVersionID)
			source := filepath.Join(settings.VersionDir(site.ID, version.Number), "output", "public")
			target := filepath.Join(settings.SamplesDir(), site.DesignDirection)
			if err := replaceTree(source, target); err != nil {
				log.Fatalf("%s: %v", site.DesignDirection, err)
			}
			// The gateway can show the example in each colour variant (?warna=<variant>).
			if direction, found := sitebuilder.DirectionByKey(site.DesignDirection); found {
				palettes := map[string]map[string]string{}
				for _, variant := range direction.Variants {
					palettes[variant.Key] = variant.Palette
				}
				data, _ := json.Marshal(palettes)
				if err := os.WriteFile(filepath.Join(target, "variants.json"), data, 0o644); err != nil {
					log.Fatalf("%s: %v", site.DesignDirection, err)
				}
			}
			fmt.Printf("%-16s copied version %d\n", site.DesignDirection, version.Number)
		}
	}
}

// queue makes the example site of one look, like CreateSite and SiteBrief would,
// with a hand-written brief, and queues its create run.
func queue(db *gorm.DB, storage cloudworkspace.WorkspaceStorage, userID uint, direction sitebuilder.Direction) error {
	example, ok := samples[direction.Key]
	if !ok {
		return fmt.Errorf("no example business for this look")
	}
	brief := sitebuilder.Brief{
		Name: example.name, Tagline: example.tagline, Summary: example.about, Language: "id", Kind: direction.Fits[0],
		Audience: "Orang di sekitar yang mencari " + strings.ToLower(example.tagline),
		Pages: []sitebuilder.BriefPage{
			{Slug: "beranda", Title: "Beranda", Purpose: "Mengenalkan " + example.name + " dan mengajak menghubungi", Sections: example.home},
			{Slug: sitebuilder.PageSlug(example.secondTitle), Title: example.secondTitle, Purpose: "Bagian kedua dari website contoh", Sections: example.second},
		},
		Forms:        []sitebuilder.BriefForm{{Name: "pesan", Purpose: "Pengunjung mengirim pesan atau pesanan", Fields: []string{"Nama", "Nomor WhatsApp", "Pesan"}}},
		ContentNotes: example.about + " Ini website contoh: jangan menulis harga, nama orang, atau klaim apa pun.",
		Contact:      example.contact,
	}
	if err := brief.Normalize(); err != nil {
		return err
	}
	ref, err := storage.Create(userID)
	if err != nil {
		return err
	}
	filesDir, _ := storage.FilesDir(userID, ref)
	metaDir, _ := storage.MetaDir(userID, ref)
	if err := sitebuilder.CopyTemplate(filesDir); err != nil {
		return err
	}
	_ = cloudworkspace.SaveBaseline(filesDir, metaDir, cloudworkspace.DefaultLimits())
	briefJSON, _ := json.Marshal(brief)
	return db.Transaction(func(tx *gorm.DB) error {
		workspace := models.CloudWorkspace{UserID: userID, Name: "Contoh: " + direction.Key, SourceType: models.WorkspaceSourceEmpty, Status: models.WorkspaceStatusReady, StorageRef: ref, DefaultRuntime: "node"}
		if err := tx.Create(&workspace).Error; err != nil {
			return err
		}
		site := models.Site{UserID: userID, WorkspaceID: workspace.ID, Name: example.name, Description: example.about, Language: "id", UILanguage: "id",
			Kind: brief.Kind, Status: models.SiteStatusBuilding, BriefJSON: string(briefJSON), DesignDirection: direction.Key}
		if err := tx.Create(&site).Error; err != nil {
			return err
		}
		return tx.Create(&models.AgentRun{SiteID: site.ID, UserID: userID, Kind: models.AgentRunCreate, Status: models.RunQueued, Background: true}).Error
	})
}

// replaceTree replaces target with a copy of source (regular files and folders only).
func replaceTree(source, target string) error {
	if _, err := os.Stat(filepath.Join(source, "index.html")); err != nil {
		return fmt.Errorf("the finished example has no index.html in %s", source)
	}
	temporary := target + ".new"
	_ = os.RemoveAll(temporary)
	err := filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(source, current)
		destination := filepath.Join(temporary, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o750)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		return err
	}
	_ = os.RemoveAll(target)
	return os.Rename(temporary, target)
}
