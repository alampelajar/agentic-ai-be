# Agentic AI — Backend

REST API untuk platform Agentic AI. Backend dibuat menggunakan Go, Gin, GORM, dan PostgreSQL serta menyediakan autentikasi, pengelolaan Agent/Model AI, chat, dan task.

## Teknologi

- Go (versi sesuai `go.mod`)
- Gin untuk HTTP API
- PostgreSQL sebagai database
- GORM untuk akses database dan migrasi skema
- JWT dan refresh token untuk autentikasi
- Google OAuth untuk login Google
- Resend untuk email pengaturan ulang kata sandi

## Fitur utama

- Registrasi, login, refresh token, dan logout
- Login Google
- Profil pengguna, ubah kata sandi, dan penghapusan akun
- Request, verifikasi OTP, dan reset kata sandi
- Daftar Agent dan pengelolaan task beserta pesan task
- Endpoint chat dan generator landing page
- Pengelolaan AI provider dan model, termasuk model sistem/admin
- Endpoint kesehatan layanan di `GET /api/health`

## Persyaratan

- Go versi yang tercantum di `go.mod`
- PostgreSQL yang dapat diakses dari komputer tempat backend berjalan
- API key provider AI untuk menjalankan fitur AI
- Kredensial tambahan jika ingin memakai Google Sign-In atau email reset kata sandi

## Instalasi lokal

1. Clone repo:

   ```bash
   git clone https://github.com/alampelajar/agentic-ai-be.git
   cd agentic-ai-be
   ```

2. Buat file konfigurasi lokal:

   **Windows PowerShell**
   ```powershell
   Copy-Item .env.example .env
   ```

   **macOS/Linux**
   ```bash
   cp .env.example .env
   ```

3. Buat database PostgreSQL bernama `agentic_ai` (atau gunakan nama lain sesuai konfigurasi), kemudian edit `.env`:

   ```env
   DB_HOST=127.0.0.1
   DB_PORT=5432
   DB_USER=postgres
   DB_PASSWORD=your_database_password
   DB_NAME=agentic_ai

   PORT=8080
   GOOGLE_CLIENT_ID=

   AI_ENCRYPTION_KEY=your_64_character_encryption_key

   NARAROUTER_API_KEY=your_api_key
   NARAROUTER_MODEL=agnes-2.5-flash

   RESEND_API_KEY=
   RESEND_FROM_EMAIL=
   PASSWORD_RESET_PEPPER=replace_with_a_random_secret_at_least_32_chars
   ```

   Sesuaikan seluruh nilai dengan lingkungan lokal. Buat `AI_ENCRYPTION_KEY` menggunakan secret acak 64 karakter heksadesimal dan simpan dengan aman. Gunakan `PASSWORD_RESET_PEPPER` yang acak dan rahasia. Nilai pengirim Resend harus berasal dari domain/alamat pengirim yang sudah diverifikasi jika pengiriman email diaktifkan.

4. Unduh dependency dan jalankan:

   ```bash
   go mod download
   go run .
   ```

   Saat startup, backend akan mencoba menghubungkan PostgreSQL, menerapkan migrasi skema melalui GORM, dan menyiapkan data awal Agent/provider/model.

5. Periksa status API:

   Buka `http://localhost:8080/api/health`. Jika layanan aktif, endpoint akan mengembalikan respons JSON dengan status `ok`.

## Konfigurasi lingkungan

| Variabel | Kegunaan |
| --- | --- |
| `DB_HOST`, `DB_PORT` | Host dan port PostgreSQL |
| `DB_USER`, `DB_PASSWORD`, `DB_NAME` | Kredensial dan nama database |
| `PORT` | Port HTTP backend; gunakan `8080` untuk konfigurasi frontend lokal saat ini |
| `GOOGLE_CLIENT_ID` | Client ID Google OAuth |
| `AI_ENCRYPTION_KEY` | Kunci untuk mengenkripsi kredensial provider AI |
| `NARAROUTER_API_KEY` | API key provider AI NaraRouter |
| `NARAROUTER_MODEL` | ID model yang digunakan untuk provider tersebut |
| `RESEND_API_KEY`, `RESEND_FROM_EMAIL` | Pengaturan email untuk reset kata sandi |
| `PASSWORD_RESET_PEPPER` | Secret tambahan untuk alur reset kata sandi |

Jangan commit file `.env` atau mengirim API key, password database, JWT secret, dan encryption key ke repository atau issue publik.

## Development dan pengujian

Jalankan pemeriksaan kode:

```bash
go test ./...
go build ./...
```

Jalankan backend dan frontend pada origin lokal yang diizinkan oleh konfigurasi CORS backend. Jika frontend menggunakan port berbeda, sesuaikan kebijakan CORS agar origin tersebut diizinkan.

## Catatan keamanan

- Gunakan secret yang berbeda untuk development dan production.
- Batasi akses ke database dan jangan gunakan kredensial default pada deployment publik.
- Atur HTTPS, CORS, dan pengelolaan secret yang sesuai sebelum deployment ke production.
- Jangan menggunakan alamat pengirim email yang belum diverifikasi untuk mengirim email ke pengguna umum.
