package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"

	"github.com/tts-agent/internal/config"
)

var ErrDeliveryNotConfigured = errors.New("邮件投递服务尚未配置")

type Sender interface {
	SendVerificationCode(ctx context.Context, to, code string) error
}

func NewSender(cfg *config.Config) (Sender, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.EmailProvider)) {
	case "", "log":
		return LogSender{}, nil
	case "smtp":
		if cfg.EmailSMTPHost == "" || cfg.EmailSMTPUsername == "" || cfg.EmailSMTPPassword == "" {
			return nil, fmt.Errorf("SMTP 邮件服务配置不完整")
		}
		return SMTPSender{
			from:     cfg.EmailFrom,
			host:     cfg.EmailSMTPHost,
			port:     cfg.EmailSMTPPort,
			tlsMode:  cfg.EmailSMTPTLSMode,
			username: cfg.EmailSMTPUsername,
			password: cfg.EmailSMTPPassword,
		}, nil
	default:
		return nil, fmt.Errorf("不支持的邮件服务提供方：%s", cfg.EmailProvider)
	}
}

type LogSender struct{}

func (LogSender) SendVerificationCode(_ context.Context, to, _ string) error {
	log.Printf("development email delivery is not configured for recipient domain %s", recipientDomain(to))
	return ErrDeliveryNotConfigured
}

type SMTPSender struct {
	from     string
	host     string
	port     int
	tlsMode  string
	username string
	password string
}

func (s SMTPSender) SendVerificationCode(ctx context.Context, to, code string) error {
	address := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	var connection net.Conn
	var err error
	switch strings.ToLower(strings.TrimSpace(s.tlsMode)) {
	case "", "starttls":
		connection, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	case "implicit":
		connection, err = (&tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
	default:
		return fmt.Errorf("不支持的 SMTP TLS 模式：%s", s.tlsMode)
	}
	if err != nil {
		return fmt.Errorf("连接邮件服务失败：%w", err)
	}
	defer connection.Close()

	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return fmt.Errorf("初始化邮件客户端失败：%w", err)
	}
	defer client.Quit()
	if strings.ToLower(strings.TrimSpace(s.tlsMode)) != "implicit" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("邮件服务不支持 STARTTLS，请改用 TLS 模式 implicit 或更换端口")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("邮件服务 TLS 协商失败：%w", err)
		}
	}
	if ok, _ := client.Extension("AUTH"); !ok {
		return fmt.Errorf("邮件服务不支持身份认证")
	}
	if err := client.Auth(smtp.PlainAuth("", s.username, s.password, s.host)); err != nil {
		return fmt.Errorf("邮件服务认证失败：%w", err)
	}
	if err := client.Mail(extractAddress(s.from)); err != nil {
		return fmt.Errorf("设置发件人失败：%w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败：%w", err)
	}
	body, err := client.Data()
	if err != nil {
		return fmt.Errorf("创建邮件内容失败：%w", err)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: =?UTF-8?B?5L+h566x6aqM6K+B56CB?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n您的经营分析助手登录验证码是：%s\n\n验证码 10 分钟内有效，请勿转发给他人。\n", s.from, to, code)
	if _, err := body.Write([]byte(message)); err != nil {
		return fmt.Errorf("写入邮件内容失败：%w", err)
	}
	if err := body.Close(); err != nil {
		return fmt.Errorf("发送邮件失败：%w", err)
	}
	return nil
}

func extractAddress(value string) string {
	start := strings.Index(value, "<")
	end := strings.Index(value, ">")
	if start >= 0 && end > start {
		return strings.TrimSpace(value[start+1 : end])
	}
	return strings.TrimSpace(value)
}

func recipientDomain(address string) string {
	parts := strings.SplitN(strings.TrimSpace(address), "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "invalid"
	}
	return strings.ToLower(parts[1])
}
