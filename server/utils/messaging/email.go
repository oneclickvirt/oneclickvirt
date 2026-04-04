package messaging

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SendEmail sends an HTML email via SMTP.
func SendEmail(smtpHost string, smtpPort int, username, password, to, subject, htmlBody string) error {
	if smtpHost == "" {
		return fmt.Errorf("SMTP host未配置")
	}
	if to == "" {
		return fmt.Errorf("收件人为空")
	}

	addr := fmt.Sprintf("%s:%d", smtpHost, smtpPort)

	// Build RFC 822 message
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", username))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", to))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	msg.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	// Try STARTTLS on port 587, direct TLS on 465, plain on 25
	if smtpPort == 465 {
		return sendEmailDirectTLS(addr, smtpHost, username, password, to, msg.String())
	}

	// STARTTLS / plain
	auth := smtp.PlainAuth("", username, password, smtpHost)
	return smtp.SendMail(addr, auth, username, []string{to}, []byte(msg.String()))
}

func sendEmailDirectTLS(addr, smtpHost, username, password, to, msg string) error {
	tlsConfig := &tls.Config{ServerName: smtpHost}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("TLS连接失败: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, smtpHost)
	if err != nil {
		return fmt.Errorf("SMTP客户端创建失败: %w", err)
	}
	defer client.Quit()

	auth := smtp.PlainAuth("", username, password, smtpHost)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP认证失败: %w", err)
	}

	if err = client.Mail(username); err != nil {
		return fmt.Errorf("SMTP MAIL FROM失败: %w", err)
	}
	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO失败: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA失败: %w", err)
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("SMTP写入失败: %w", err)
	}
	return w.Close()
}
