package sitebuilder

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Step sentences the person sees, in the site's language. No technical words
// (WEBSITE-BUILDER.md, section 2).
var stepWords = map[string]map[string]string{
	"prepare":       {"id": "Menyiapkan desain dan halaman", "en": "Preparing the design and pages"},
	"layout":        {"id": "Merancang menu dan bagian bawah halaman", "en": "Designing the menu and footer"},
	"page":          {"id": "Menulis halaman %s", "en": "Writing the %s page"},
	"revise":        {"id": "Mengerjakan perubahan yang Anda minta", "en": "Working on the change you asked for"},
	"restyle":       {"id": "Menerapkan tampilan baru", "en": "Applying the new look"},
	"check":         {"id": "Memeriksa apakah website berjalan dengan baik", "en": "Checking that the website works"},
	"checked":       {"id": "Website berjalan dengan baik", "en": "The website works"},
	"repair":        {"id": "Memperbaiki kendala yang ditemukan", "en": "Fixing a problem that was found"},
	"tokens":        {"id": "Merapikan warna dan huruf agar sesuai tampilan", "en": "Aligning colours and fonts with the look"},
	"review":        {"id": "Meninjau tampilan dan isi tulisan", "en": "Reviewing the design and the text"},
	"reviewed":      {"id": "Tinjauan selesai, tidak ada yang perlu diubah", "en": "Review done, nothing to change"},
	"improve":       {"id": "Menyempurnakan %s", "en": "Polishing %s"},
	"save":          {"id": "Menyimpan versi baru", "en": "Saving a new version"},
	"failed":        {"id": "Ada kendala saat membuat website. Versi sebelumnya tetap aman.", "en": "Something went wrong while making the website. The previous version is safe."},
	"failedFirst":   {"id": "Ada kendala saat membuat website. Silakan coba lagi.", "en": "Something went wrong while making the website. Please try again."},
	"cancelled":     {"id": "Dihentikan atas permintaan Anda", "en": "Stopped at your request"},
	"busy":          {"id": "Layanan pembuat website sedang tidak tersedia. Coba lagi beberapa saat lagi.", "en": "The website maker is not available right now. Try again in a while."},
	"noModel":       {"id": "Tidak ada model AI yang bisa dipakai saat ini. Coba lagi beberapa saat lagi.", "en": "No AI model is available right now. Try again in a while."},
	"reviewSkipped": {"id": "Website sudah lolos pemeriksaan dan disimpan. Tinjauan tambahan bisa diminta nanti.", "en": "The website passed its check and is saved. A further review can be asked for later."},
	"resumed":       {"id": "Memakai bagian yang sudah jadi sebelumnya", "en": "Using the parts already made"},
	"tokenLimit":    {"id": "Jatah AI Anda bulan ini sudah habis. Bagian yang sudah jadi tetap disimpan dan bisa dilanjutkan bulan depan.", "en": "Your AI allowance for this month is used up. The finished parts are kept and can be continued next month."},
	"colours":       {"id": "Mengganti warna website", "en": "Changing the website's colours"},
	"waitModel":    {"id": "Menunggu AI siap...", "en": "Waiting for the AI to be ready..."},
	"createdDone":   {"id": "Website Anda sudah jadi.", "en": "Your website is ready."},
	"reviseDone":    {"id": "Perubahan sudah diterapkan.", "en": "The change is done."},
	"restyleDone":   {"id": "Tampilan baru sudah diterapkan.", "en": "The new look is applied."},
}

func say(language, key string, args ...any) string {
	words := stepWords[key]
	text := words[language]
	if text == "" {
		text = words["id"]
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

// DecodeJSON reads the first JSON object in a model answer, with or without a
// Markdown fence or text around it.
func DecodeJSON(answer string, target any) error {
	start := strings.Index(answer, "{")
	end := strings.LastIndex(answer, "}")
	if start < 0 || end <= start {
		return errors.New("the answer holds no JSON object")
	}
	return json.Unmarshal([]byte(answer[start:end+1]), target)
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "\n..."
}
