package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureWindow        = 10 * time.Minute
	loginIdentityLockDuration = 15 * time.Minute
	loginIPLockDuration       = 10 * time.Minute
	loginIdentityMaxFailures  = 5
	loginIPMaxFailures        = 30
	loginThrottleMaxEntries   = 10000
)

var globalLoginAttemptThrottle = newLoginAttemptThrottle()

type loginAttemptThrottle struct {
	mu      sync.Mutex
	entries map[string]*loginAttemptEntry
}

type loginAttemptEntry struct {
	failures     int
	firstFailure time.Time
	lockedUntil  time.Time
	lastSeen     time.Time
}

func newLoginAttemptThrottle() *loginAttemptThrottle {
	return &loginAttemptThrottle{
		entries: make(map[string]*loginAttemptEntry),
	}
}

func (t *loginAttemptThrottle) Check(ip, identity string) error {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cleanupLocked(now)
	for _, key := range loginThrottleKeys(ip, identity) {
		entry, exists := t.entries[key]
		if !exists {
			continue
		}
		entry.lastSeen = now
		if entry.lockedUntil.After(now) {
			return common.NewError(common.CodeTooManyRequests, "登录失败次数过多，请稍后重试")
		}
	}
	return nil
}

func (t *loginAttemptThrottle) RecordFailure(ip, identity string) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cleanupLocked(now)
	for _, item := range []struct {
		key          string
		maxFailures  int
		lockDuration time.Duration
	}{
		{key: loginThrottleIdentityKey(ip, identity), maxFailures: loginIdentityMaxFailures, lockDuration: loginIdentityLockDuration},
		{key: loginThrottleIPKey(ip), maxFailures: loginIPMaxFailures, lockDuration: loginIPLockDuration},
	} {
		if item.key == "" {
			continue
		}
		entry := t.entries[item.key]
		if entry == nil || now.Sub(entry.firstFailure) > loginFailureWindow {
			entry = &loginAttemptEntry{firstFailure: now}
			t.entries[item.key] = entry
		}
		entry.failures++
		entry.lastSeen = now
		if entry.failures >= item.maxFailures {
			entry.lockedUntil = now.Add(item.lockDuration)
		}
	}
}

func (t *loginAttemptThrottle) ResetIdentity(ip, identity string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, loginThrottleIdentityKey(ip, identity))
}

func (t *loginAttemptThrottle) cleanupLocked(now time.Time) {
	if len(t.entries) == 0 {
		return
	}
	for key, entry := range t.entries {
		if entry.lockedUntil.After(now) {
			continue
		}
		if now.Sub(entry.lastSeen) > loginFailureWindow {
			delete(t.entries, key)
		}
	}
	if len(t.entries) <= loginThrottleMaxEntries {
		return
	}
	for key, entry := range t.entries {
		if now.Sub(entry.lastSeen) > time.Minute {
			delete(t.entries, key)
			if len(t.entries) <= loginThrottleMaxEntries {
				return
			}
		}
	}
}

func loginThrottleIdentity(req authModel.LoginRequest, loginType string) string {
	switch loginType {
	case "email", "telegram", "qq":
		return req.Target
	default:
		return req.Username
	}
}

func loginThrottleKeys(ip, identity string) []string {
	keys := []string{}
	if key := loginThrottleIdentityKey(ip, identity); key != "" {
		keys = append(keys, key)
	}
	if key := loginThrottleIPKey(ip); key != "" {
		keys = append(keys, key)
	}
	return keys
}

func loginThrottleIdentityKey(ip, identity string) string {
	normalizedIP := normalizeThrottlePart(ip)
	normalizedIdentity := normalizeThrottlePart(identity)
	if normalizedIP == "" || normalizedIdentity == "" {
		return ""
	}
	return fmt.Sprintf("identity:%s:%s", hashThrottlePart(normalizedIP), hashThrottlePart(normalizedIdentity))
}

func loginThrottleIPKey(ip string) string {
	normalizedIP := normalizeThrottlePart(ip)
	if normalizedIP == "" {
		return ""
	}
	return "ip:" + hashThrottlePart(normalizedIP)
}

func normalizeThrottlePart(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func hashThrottlePart(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
