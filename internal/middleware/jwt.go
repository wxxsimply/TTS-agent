package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tts-agent/internal/auth"
	"github.com/tts-agent/internal/store"
)

const UserIDKey = "userID"

func JWT(authSvc *auth.Service, db *store.Postgres) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "请先登录"})
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		userID, err := authSvc.VerifyAccessToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "登录状态已失效，请重新登录"})
			return
		}
		user, err := db.GetUserByID(c.Request.Context(), userID)
		if err != nil || user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "账号不存在或已停用"})
			return
		}
		c.Set(UserIDKey, userID)
		c.Set("user", user)
		c.Next()
	}
}

func GetUserID(c *gin.Context) uuid.UUID {
	v, _ := c.Get(UserIDKey)
	id, _ := v.(uuid.UUID)
	return id
}
