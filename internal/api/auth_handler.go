package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/auth"
	mail "github.com/tts-agent/internal/email"
	"github.com/tts-agent/internal/store"
)

type AuthHandler struct {
	auth     *auth.Service
	db       *store.Postgres
	email    mail.Sender
	codeTTL  time.Duration
	resendIn time.Duration
	maxTry   int
	maxHour  int
}

func NewAuthHandler(authSvc *auth.Service, db *store.Postgres, sender mail.Sender, codeTTL, resendInterval time.Duration, maxAttempts, maxPerHour int) *AuthHandler {
	return &AuthHandler{auth: authSvc, db: db, email: sender, codeTTL: codeTTL, resendIn: resendInterval, maxTry: maxAttempts, maxHour: maxPerHour}
}

type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type emailCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type emailLoginRequest struct {
	Email string `json:"email" binding:"required,email"`
	Code  string `json:"code" binding:"required,len=6,numeric"`
}

type tokenResponse struct {
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	TokenType          string `json:"token_type"`
	PasswordConfigured bool   `json:"password_configured"`
}

type setPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password" binding:"required,min=6,max=72"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入有效邮箱，密码至少需要 6 位"})
		return
	}
	req.Email = normalizeEmail(req.Email)
	existing, _ := h.db.GetUserByEmail(c.Request.Context(), req.Email)
	if existing != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "该邮箱已注册，请直接登录"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "注册服务暂时不可用"})
		return
	}
	user, err := h.db.CreateUser(c.Request.Context(), req.Email, hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "账号创建失败，请稍后重试"})
		return
	}
	h.respondTokens(c, user.ID)
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入有效邮箱和密码"})
		return
	}
	req.Email = normalizeEmail(req.Email)
	user, _ := h.db.GetUserByEmail(c.Request.Context(), req.Email)
	if user == nil || !auth.VerifyPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "账号或密码错误"})
		return
	}
	h.respondTokens(c, user.ID)
}

func (h *AuthHandler) SendEmailCode(c *gin.Context) {
	var req emailCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入有效邮箱"})
		return
	}
	req.Email = normalizeEmail(req.Email)
	wait, err := h.db.EmailCodeRetryAfter(c.Request.Context(), req.Email, "login", h.resendIn, h.maxHour)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "验证码服务暂时不可用"})
		return
	}
	if wait > 0 {
		seconds := int(wait.Seconds()) + 1
		c.JSON(http.StatusTooManyRequests, gin.H{"detail": fmt.Sprintf("验证码发送过于频繁，请 %d 秒后再试", seconds), "retry_after_seconds": seconds})
		return
	}
	code, err := auth.GenerateEmailCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "生成验证码失败，请稍后重试"})
		return
	}
	if err := h.db.CreateEmailVerificationCode(c.Request.Context(), req.Email, "login", h.auth.HashEmailCode(req.Email, "login", code), time.Now().Add(h.codeTTL)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "验证码服务暂时不可用"})
		return
	}
	if err := h.email.SendVerificationCode(c.Request.Context(), req.Email, code); err != nil {
		_ = h.db.InvalidateEmailVerificationCodes(c.Request.Context(), req.Email, "login")
		if errors.Is(err, mail.ErrDeliveryNotConfigured) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "邮箱验证码服务尚未配置，请联系管理员设置 SMTP 邮件服务"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "验证码邮件发送失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "验证码已发送，请查收邮箱", "expires_in_seconds": int(h.codeTTL.Seconds()), "resend_in_seconds": int(h.resendIn.Seconds())})
}

func (h *AuthHandler) EmailLogin(c *gin.Context) {
	var req emailLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请输入有效邮箱和 6 位验证码"})
		return
	}
	req.Email = normalizeEmail(req.Email)
	valid, err := h.db.ConsumeEmailVerificationCode(c.Request.Context(), req.Email, "login", h.auth.HashEmailCode(req.Email, "login", req.Code), h.maxTry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "验证码服务暂时不可用"})
		return
	}
	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "验证码错误、已过期或已失效"})
		return
	}
	user, err := h.db.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "登录服务暂时不可用"})
		return
	}
	if user == nil {
		user, err = h.db.CreateUser(c.Request.Context(), req.Email, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "账号创建失败，请稍后重试"})
			return
		}
	}
	h.respondTokens(c, user.ID)
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "刷新令牌不能为空"})
		return
	}
	userID, err := h.auth.VerifyRefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "登录状态已失效，请重新登录"})
		return
	}
	hash := auth.HashRefreshToken(req.RefreshToken)
	stored, _ := h.db.GetRefreshToken(c.Request.Context(), hash)
	if stored == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "登录状态已失效，请重新登录"})
		return
	}
	_ = h.db.DeleteRefreshToken(c.Request.Context(), hash)
	h.respondTokens(c, userID)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "退出登录请求无效"})
		return
	}
	_ = h.db.DeleteRefreshToken(c.Request.Context(), auth.HashRefreshToken(req.RefreshToken))
	c.JSON(http.StatusOK, gin.H{"message": "已退出登录"})
}

func (h *AuthHandler) Me(c *gin.Context) {
	user, _ := c.Get("user")
	u := user.(*store.User)
	c.JSON(http.StatusOK, gin.H{"id": u.ID.String(), "email": u.Email, "password_configured": u.PasswordHash != ""})
}

func (h *AuthHandler) SetPassword(c *gin.Context) {
	var req setPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "新密码需要 6 至 72 位"})
		return
	}
	userValue, _ := c.Get("user")
	user := userValue.(*store.User)
	if user.PasswordHash != "" && !auth.VerifyPassword(req.CurrentPassword, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "当前密码错误"})
		return
	}
	passwordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "密码设置服务暂时不可用"})
		return
	}
	if err := h.db.UpdateUserPassword(c.Request.Context(), user.ID, passwordHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "密码保存失败，请稍后重试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码设置成功", "password_configured": true})
}

func (h *AuthHandler) respondTokens(c *gin.Context, userID uuid.UUID) {
	access, err := h.auth.CreateAccessToken(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "登录服务暂时不可用"})
		return
	}
	refresh, exp, err := h.auth.CreateRefreshToken(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "登录服务暂时不可用"})
		return
	}
	_ = h.db.SaveRefreshToken(c.Request.Context(), userID, auth.HashRefreshToken(refresh), exp)
	passwordConfigured := false
	if user, err := h.db.GetUserByID(c.Request.Context(), userID); err == nil && user != nil {
		passwordConfigured = user.PasswordHash != ""
	}
	c.JSON(http.StatusOK, tokenResponse{AccessToken: access, RefreshToken: refresh, TokenType: "bearer", PasswordConfigured: passwordConfigured})
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func decodeJSONRaw(raw json.RawMessage, v interface{}) {
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, v)
	}
}
