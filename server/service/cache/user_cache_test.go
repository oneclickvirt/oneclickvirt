package cache

import (
	"testing"
	"time"
)

func TestUserCacheService_Basic(t *testing.T) {
	cache := GetUserCacheService()

	// 测试基本的 Set/Get
	key := "test_key"
	value := map[string]interface{}{
		"id":   1,
		"name": "test",
	}

	cache.Set(key, value, 1*time.Minute)

	// 获取缓存
	if data, ok := cache.Get(key); !ok {
		t.Error("缓存数据获取失败")
	} else if dataMap, ok := data.(map[string]interface{}); !ok {
		t.Error("缓存数据类型不匹配")
	} else if dataMap["id"].(int) != 1 {
		t.Error("缓存数据内容不正确")
	}

	// 测试过期
	cache.Set("expire_key", "test", 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if _, ok := cache.Get("expire_key"); ok {
		t.Error("过期缓存仍然存在")
	}
}

func TestUserCacheService_Delete(t *testing.T) {
	cache := GetUserCacheService()

	key := "delete_test"
	cache.Set(key, "value", 1*time.Minute)

	// 验证存在
	if _, ok := cache.Get(key); !ok {
		t.Error("缓存设置失败")
	}

	// 删除
	cache.Delete(key)

	// 验证删除
	if _, ok := cache.Get(key); ok {
		t.Error("缓存删除失败")
	}
}

func TestUserCacheService_DeleteByPrefix(t *testing.T) {
	cache := GetUserCacheService()

	// 设置多个带前缀的缓存
	cache.Set("user:1:dashboard", "data1", 1*time.Minute)
	cache.Set("user:1:traffic", "data2", 1*time.Minute)
	cache.Set("user:2:dashboard", "data3", 1*time.Minute)

	// 删除 user:1: 前缀的所有缓存
	cache.DeleteByPrefix("user:1:")

	// 验证
	if _, ok := cache.Get("user:1:dashboard"); ok {
		t.Error("前缀删除失败: user:1:dashboard")
	}
	if _, ok := cache.Get("user:1:traffic"); ok {
		t.Error("前缀删除失败: user:1:traffic")
	}
	if _, ok := cache.Get("user:2:dashboard"); !ok {
		t.Error("不应该删除其他前缀的缓存")
	}
}

func TestUserCacheService_InvalidateUserCache(t *testing.T) {
	cache := GetUserCacheService()

	userID := uint(1)

	// 设置用户相关缓存
	cache.Set(MakeUserDashboardKey(userID), "dashboard", 1*time.Minute)
	cache.Set(MakeUserTrafficOverviewKey(userID), "traffic", 1*time.Minute)
	cache.Set(MakeUserTrafficSummaryKey(userID, 2025, 12), "summary", 1*time.Minute)

	// 使缓存失效
	cache.InvalidateUserCache(userID)

	// 验证所有缓存都被删除
	if _, ok := cache.Get(MakeUserDashboardKey(userID)); ok {
		t.Error("Dashboard缓存未被清除")
	}
	if _, ok := cache.Get(MakeUserTrafficOverviewKey(userID)); ok {
		t.Error("流量概览缓存未被清除")
	}
	if _, ok := cache.Get(MakeUserTrafficSummaryKey(userID, 2025, 12)); ok {
		t.Error("流量汇总缓存未被清除")
	}
}

func TestUserCacheService_GetOrSet(t *testing.T) {
	cache := GetUserCacheService()

	key := "getorset_test"
	callCount := 0

	// 第一次调用 - 缓存未命中
	fn := func() (interface{}, error) {
		callCount++
		return map[string]interface{}{"result": callCount}, nil
	}

	data1, err := cache.GetOrSet(key, 1*time.Minute, fn)
	if err != nil {
		t.Errorf("GetOrSet失败: %v", err)
	}
	if callCount != 1 {
		t.Errorf("函数应该被调用1次，实际调用%d次", callCount)
	}

	// 第二次调用 - 缓存命中
	data2, err := cache.GetOrSet(key, 1*time.Minute, fn)
	if err != nil {
		t.Errorf("GetOrSet失败: %v", err)
	}
	if callCount != 1 {
		t.Errorf("函数不应该被再次调用，实际调用%d次", callCount)
	}

	// 验证两次获取的数据相同
	if data1 == nil || data2 == nil {
		t.Error("GetOrSet返回数据为nil")
	}
}

func TestCacheKeys(t *testing.T) {
	// 测试缓存键生成
	userID := uint(123)
	year := 2025
	month := 12
	instanceID := uint(456)

	dashboardKey := MakeUserDashboardKey(userID)
	if dashboardKey != "user:dashboard:123" {
		t.Errorf("Dashboard键生成错误: %s", dashboardKey)
	}

	trafficKey := MakeUserTrafficSummaryKey(userID, year, month)
	if trafficKey != "user:traffic:summary:123:2025:12" {
		t.Errorf("流量汇总键生成错误: %s", trafficKey)
	}

	overviewKey := MakeUserTrafficOverviewKey(userID)
	if overviewKey != "user:traffic:overview:123" {
		t.Errorf("流量概览键生成错误: %s", overviewKey)
	}

	instanceKey := MakeInstanceTrafficDetailKey(instanceID)
	if instanceKey != "instance:traffic:detail:456" {
		t.Errorf("实例流量键生成错误: %s", instanceKey)
	}
}
