package services

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// SMTP delivery, for local development (Mailpit, Mailtrap) or any SMTP relay.
//
//	SMTP_HOST   host name; setting it switches mail from Resend to SMTP
//	SMTP_PORT   default 1025 (Mailpit); 465 uses implicit TLS, others STARTTLS when offered
//	SMTP_USER / SMTP_PASS   optional; plain auth is used only when SMTP_USER is set
//	MAIL_FROM   sender address, e.g. "Agentic AI <noreply@localhost>"

// SMTPConfigured reports whether SMTP delivery is selected.
func SMTPConfigured() bool {
	return strings.TrimSpace(os.Getenv("SMTP_HOST")) != ""
}

// cleanHeader removes line breaks so a value can never inject another header.
func cleanHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(value))
}

func buildMIME(from, to, subject, htmlBody, textBody string) ([]byte, error) {
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	boundary := "agentic-" + hex.EncodeToString(id)

	var b strings.Builder
	b.WriteString("From: " + cleanHeader(from) + "\r\n")
	b.WriteString("To: " + cleanHeader(to) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", cleanHeader(subject)) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + hex.EncodeToString(id) + "@agentic-ai.local>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")
	for _, part := range []struct{ kind, body string }{{"text/plain", textBody}, {"text/html", htmlBody}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + part.kind + "; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		// Dot-stuffing is done by the SMTP client's data writer; only normalise line ends.
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(part.body, "\r\n", "\n"), "\n", "\r\n") + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func sendSMTP(ctx context.Context, sender, recipient, subject, htmlBody, textBody string) error {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if port == "" {
		port = "1025"
	}

	from, err := mail.ParseAddress(cleanHeader(sender))
	if err != nil {
		return fmt.Errorf("smtp: invalid sender address")
	}
	to, err := mail.ParseAddress(cleanHeader(recipient))
	if err != nil {
		return fmt.Errorf("smtp: invalid recipient address")
	}
	message, err := buildMIME(from.String(), to.Address, subject, htmlBody, textBody)
	if err != nil {
		return err
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	address := net.JoinHostPort(host, port)
	var conn net.Conn
	if port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("smtp: connect failed: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: handshake failed: %w", err)
	}
	defer client.Close()

	if port != "465" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp: STARTTLS failed: %w", err)
			}
		}
	}
	if user := strings.TrimSpace(os.Getenv("SMTP_USER")); user != "" {
		if err := client.Auth(smtp.PlainAuth("", user, os.Getenv("SMTP_PASS"), host)); err != nil {
			return fmt.Errorf("smtp: authentication failed")
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp: sender refused: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp: recipient refused: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data failed: %w", err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("smtp: write failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: delivery failed: %w", err)
	}
	return client.Quit()
}
