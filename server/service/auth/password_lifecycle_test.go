package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"
	authService "oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/user/notification"
	"oneclickvirt/service/user/profile"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPasswordEntryPointsInvalidateExistingLogin(t *testing.T) {
	const oldPassword = "Previous-secure-789!Z"
	const newPassword = "Replacement-secure-456!Z"
	for _, entry := range []string{"auth-change", "profile-change", "reset-link", "generated-link", "generated", "generated-notify"} {
		t.Run(entry, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "password.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&userModel.User{}, &userModel.PasswordReset{}); err != nil {
				t.Fatal(err)
			}
			oldDB, oldLog := global.APP_DB, global.APP_LOG
			global.APP_DB, global.APP_LOG = db, zap.NewNop()
			defer func() { global.APP_DB, global.APP_LOG = oldDB, oldLog; sql, _ := db.DB(); sql.Close() }()
			hash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			user := userModel.User{Username: "lifecycle-test", Password: string(hash)}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			reset := userModel.PasswordReset{UserUUID: user.UUID, Token: "local-regression-token", ExpiresAt: time.Now().Add(time.Hour)}
			if err := db.Create(&reset).Error; err != nil {
				t.Fatal(err)
			}
			key := cache.MakeUserAuthContextKey(user.ID)
			cache.GetUserCacheService().Set(key, "stale-login", time.Minute)
			before := time.Now()
			switch entry {
			case "auth-change":
				err = (&authService.AuthService{}).ChangePassword(user.ID, oldPassword, newPassword)
			case "profile-change":
				err = profile.NewService().ChangePassword(user.ID, oldPassword, newPassword)
			case "reset-link":
				err = (&authService.AuthService{}).ResetPassword(reset.Token, newPassword)
			case "generated-link":
				_ = (&authService.AuthService{}).ResetPasswordWithToken(reset.Token) // no notification channel configured
			case "generated":
				_, err = notification.NewService().ResetPassword(user.ID)
			case "generated-notify":
				_, _ = notification.NewService().ResetPasswordAndNotify(user.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var updated userModel.User
			if err := db.First(&updated, user.ID).Error; err != nil {
				t.Fatal(err)
			}
			if updated.Password == user.Password {
				t.Fatal("password was not changed")
			}
			if updated.TokensInvalidatedAt == nil || updated.TokensInvalidatedAt.Before(before) {
				t.Fatal("old login tokens were not invalidated")
			}
			if _, exists := cache.GetUserCacheService().Get(key); exists {
				t.Fatal("stale authentication cache survived password change")
			}
		})
	}
}
