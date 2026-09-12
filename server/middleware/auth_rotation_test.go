package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"oneclickvirt/global"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/permission"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPasswordRotationSameSecondTokens(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", "local-rotation-regression-signing-key")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&userModel.User{}, &permission.UserPermission{}, &userModel.JWTBlacklistedToken{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	defer func() { global.APP_DB, global.APP_LOG = oldDB, oldLog; sqlDB, _ := db.DB(); sqlDB.Close() }()
	second := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	cutoff := second.Add(500 * time.Millisecond)
	u := userModel.User{Username: "rotation", Password: "unused", UserType: "user", Status: 1, TokensInvalidatedAt: &cutoff}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	cacheKey := cache.MakeUserAuthContextKey(u.ID)
	defer cache.GetUserCacheService().Delete(cacheKey)
	router := gin.New()
	router.GET("/protected", RequireAuth(authModel.AuthLevelUser), func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, tc := range []struct {
		name    string
		issued  time.Time
		precise bool
		want    int
	}{
		{"old same second", second.Add(100 * time.Millisecond), true, 401},
		{"new same second", second.Add(900 * time.Millisecond), true, 200},
		{"legacy earlier", second.Add(-time.Second), false, 401},
		{"legacy later", second.Add(time.Second), false, 200},
	} {
		for _, warm := range []bool{false, true} {
			t.Run(tc.name+"/cache="+strconv.FormatBool(warm), func(t *testing.T) {
				cache.GetUserCacheService().Delete(cacheKey)
				if warm {
					cache.GetUserCacheService().Set(cacheKey, &authModel.AuthContext{
						UserID: u.ID, UserType: "user", Level: 1, IsEffective: true, TokensInvalidatedAt: &cutoff,
					}, time.Minute)
				}
				claims := jwt.MapClaims{"user_id": u.ID, "iat": tc.issued.Unix(), "exp": time.Now().Add(time.Hour).Unix(), "jti": tc.name}
				if tc.precise {
					claims["iat_ns"] = strconv.FormatInt(tc.issued.UnixNano(), 10)
				}
				token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(utils.GetJWTKey()))
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest("GET", "/protected", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != tc.want {
					t.Fatalf("HTTP %d, want %d: %s", response.Code, tc.want, response.Body.String())
				}
			})
		}
	}
}
