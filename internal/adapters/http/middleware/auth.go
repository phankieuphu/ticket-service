package middleware

import (
	"net/http"
	"strings"
	"ticket-service/pkg/jwtutil"

	"github.com/gin-gonic/gin"
)

const (
	ContextUserIDKey      = "auth.user_id"
	ContextUsernameKey    = "auth.username"
	ContextRoleIDsKey     = "auth.role_ids"
	ContextPermissionsKey = "auth.permissions"
)

// Auth validates the JWT bearer token on the Authorization header and stores
// the resulting claims in the gin context for downstream handlers/middleware.
func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}

		claims, err := jwtutil.ParseToken(secret, strings.TrimSpace(parts[1]))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(ContextUserIDKey, claims.UserID)
		c.Set(ContextUsernameKey, claims.Username)
		c.Set(ContextRoleIDsKey, claims.RoleIDs)
		c.Set(ContextPermissionsKey, claims.Permissions)
		c.Next()
	}
}

// RequirePermission ensures the authenticated caller (set by Auth) has the
// given "resource:action" permission, aborting the request with 403 otherwise.
// It must be registered after Auth.
func RequirePermission(resource, action string) gin.HandlerFunc {
	required := resource + ":" + action

	return func(c *gin.Context) {
		raw, exists := c.Get(ContextPermissionsKey)
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}

		permissions, ok := raw.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}

		for _, permission := range permissions {
			if permission == required {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
	}
}
