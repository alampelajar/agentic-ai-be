package services

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeSMTP accepts one message and returns what the client sent.
func fakeSMTP(t *testing.T) (port string, received chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	received = make(chan string, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					received <- data.String()
					write("250 queued")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
				write("250 ok")
			case cmd == "DATA":
				inData = true
				write("354 go ahead")
			case cmd == "QUIT":
				write("221 bye")
				return
			default:
				write("250 ok")
			}
		}
	}()

	_, port, _ = net.SplitHostPort(listener.Addr().String())
	return port, received
}

func TestSendEmailUsesSMTPWhenConfigured(t *testing.T) {
	port, received := fakeSMTP(t)
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_USER", "")
	t.Setenv("MAIL_FROM", "Agentic AI <noreply@localhost>")
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("OTP_PEPPER", testPepper)

	_, sender, _, ok := EmailOTPConfig()
	if !ok || sender != "Agentic AI <noreply@localhost>" {
		t.Fatalf("SMTP needs only MAIL_FROM and the pepper: got sender %q ok=%v", sender, ok)
	}

	subject, htmlBody, textBody := OTPEmail("register", "123456")
	if err := SendEmail(context.Background(), "", sender, "rina@example.com", subject, htmlBody, textBody); err != nil {
		t.Fatal(err)
	}

	message := <-received
	for _, want := range []string{"To: rina@example.com", "From: ", "multipart/alternative", "123456"} {
		if !strings.Contains(message, want) {
			t.Errorf("message is missing %q:\n%s", want, message)
		}
	}
}

func TestSMTPHeadersCannotBeInjected(t *testing.T) {
	raw, err := buildMIME("a@b.com", "x@y.com", "Hi\r\nBcc: evil@example.com", "<p>x</p>", "x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatal("a line break in the subject must not start a new header")
	}
}

func TestResendStillRequiresItsKeyWithoutSMTP(t *testing.T) {
	t.Setenv("SMTP_HOST", "")
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_FROM_EMAIL", "Agentic AI <noreply@example.com>")
	t.Setenv("OTP_PEPPER", testPepper)
	if _, _, _, ok := EmailOTPConfig(); ok {
		t.Fatal("without SMTP, a missing RESEND_API_KEY must leave the service unconfigured")
	}
}
