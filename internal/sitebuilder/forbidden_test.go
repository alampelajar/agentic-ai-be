package sitebuilder

import (
	"strings"
	"testing"
)

func TestForbiddenTopic(t *testing.T) {
	refused := map[string]string{
		"buatkan saya web judi online":              TopicGambling,
		"Situs SLOT GACOR maxwin, deposit pulsa":    TopicGambling,
		"agen togel terpercaya, pasaran hk dan sgp": TopicGambling,
		"website taruhan bola dan mix parlay":       TopicGambling,
		"sl0t g4c0r hari ini":                       TopicGambling,
		"j u d i online terbaik":                    TopicGambling,
		"daftar slot88 sekarang":                    TopicGambling,
		"live casino dan baccarat":                  TopicGambling,
		"web judol buat antisipasi":                 TopicGambling,
		"a sports betting site with the best odds":  TopicGambling,
		"web porn0 gratis":                          TopicAdult,
		"situs bokep indo":                          TopicAdult,
		"video dewasa 18+ tanpa sensor":             TopicAdult,
		"jasa open BO di Jakarta":                   TopicAdult,
		"p.o.r.n.o":                                 TopicAdult,
		"an adult website with sex videos":          TopicAdult,
		"cerita seks dan foto bugil":                TopicAdult,
	}
	for text, want := range refused {
		if got := ForbiddenTopic(text); got != want {
			t.Errorf("ForbiddenTopic(%q) = %q, want %q", text, got, want)
		}
	}

	// Ordinary websites that share a word with the list must pass.
	allowed := []string{
		"Toko burung kicau, burung gacor siap lomba",
		"Pesan meja, slot waktu tersedia jam 10 dan 14",
		"Bengkel motor, alamat Jl. xxx no 5, buka 08.00",
		"Undangan pernikahan Raka & Nadia, 13 Februari 2027",
		"Klinik kesehatan, konsultasi kesehatan seksual",
		"Kursus JavaScript untuk pemula",
		"Les bahasa Inggris, kelas alphabet untuk anak",
		"Kafe dengan menu kopi susu dan roti bakar",
		"Toko sepatu Totobuang di Ambon",
		"Rumah makan Padang, 1 porsi Rp 25.000, buka 7 hari",
		"Jasa pijat refleksi untuk keluarga",
		"Penjual baju anak unisex",
	}
	for _, text := range allowed {
		if got := ForbiddenTopic(text); got != "" {
			t.Errorf("ForbiddenTopic(%q) = %q, want none", text, got)
		}
	}
}

func TestPlannerIsAskedAboutForbiddenWebsites(t *testing.T) {
	system := QuestionsPrompt("toko roti", "id")[0].Content
	for _, want := range []string{`"forbidden"`, "gambling", "adult"} {
		if !strings.Contains(system, want) {
			t.Errorf("the questions prompt lacks %s", want)
		}
	}
	if !IsForbiddenTopic(TopicGambling) || !IsForbiddenTopic(TopicAdult) || IsForbiddenTopic("") || IsForbiddenTopic("food") {
		t.Fatal("IsForbiddenTopic is wrong")
	}
}
