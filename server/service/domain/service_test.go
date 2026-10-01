package domain

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupDomainServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:domain_service_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&domainModel.Domain{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLog
		_ = sqlDB.Close()
	})
	return db
}

func TestUpdateDomainRequestKeepsSSLWhenFlagIsOmitted(t *testing.T) {
	var request UpdateDomainRequest
	if err := json.Unmarshal([]byte(`{"internalPort":8443}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.EnableSSL != nil {
		t.Fatal("omitted enableSSL must not request a state change")
	}

	if err := json.Unmarshal([]byte(`{"enableSSL":false}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.EnableSSL == nil || *request.EnableSSL {
		t.Fatal("explicit false enableSSL must be preserved")
	}
}

func TestDomainProxyProviderLockReclaimsEntryAfterWaiters(t *testing.T) {
	providerID := uint(time.Now().UnixNano())
	unlockFirst := lockDomainProxyProviders(providerID)
	secondAcquired := make(chan struct{})
	allowSecondRelease := make(chan struct{})
	var allowRelease sync.Once
	releaseSecond := func() { allowRelease.Do(func() { close(allowSecondRelease) }) }
	defer releaseSecond()
	go func() {
		unlock := lockDomainProxyProviders(providerID)
		close(secondAcquired)
		<-allowSecondRelease
		unlock()
	}()

	deadline := time.Now().Add(time.Second)
	for {
		domainProxyProviderLocks.Lock()
		entry := domainProxyProviderLocks.entries[providerID]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		domainProxyProviderLocks.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			unlockFirst()
			t.Fatalf("provider lock references = %d, want 2", refs)
		}
		runtime.Gosched()
	}

	select {
	case <-secondAcquired:
		unlockFirst()
		releaseSecond()
		t.Fatal("second operation acquired the provider lock before the first released it")
	default:
	}

	unlockFirst()
	select {
	case <-secondAcquired:
	case <-time.After(time.Second):
		releaseSecond()
		t.Fatal("second operation did not acquire the provider lock")
	}
	releaseSecond()

	deadline = time.Now().Add(time.Second)
	for {
		domainProxyProviderLocks.Lock()
		_, exists := domainProxyProviderLocks.entries[providerID]
		domainProxyProviderLocks.Unlock()
		if !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("idle provider lock entry was not reclaimed")
		}
		runtime.Gosched()
	}
}

func TestDomainProxyMatchesCanonicalIPv6AndRevision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	domain := &domainModel.Domain{
		DomainName:     "App.Example.Test",
		InternalIP:     "[2001:DB8::3]",
		InternalPort:   8080,
		Protocol:       "HTTP",
		EnableSSL:      true,
		HasCert:        true,
		SSLCertContent: "cert-pem",
		SSLKeyContent:  "key-pem",
		UpdatedAt:      now,
	}
	proxy := agentService.DomainProxyItem{
		Domain:       "app.example.test",
		InternalIP:   "2001:db8::3",
		InternalPort: 8080,
		Protocol:     "http",
		EnableSSL:    true,
		HasCert:      true,
		CreatedAt:    now.Add(time.Second).Unix(),
		ConfigHash:   "49c37ba6116177519daeae7a4e7e0defefe64999f4968dd4a62f3a95a7b58257",
	}
	if got := domainProxyConfigHash(domain); got != proxy.ConfigHash {
		t.Fatalf("cross-language domain proxy digest = %q, want %q", got, proxy.ConfigHash)
	}
	if !domainProxyMatches(domain, proxy) {
		t.Fatal("canonical equivalent domain proxy should be treated as synchronized")
	}

	changedCert := *domain
	changedCert.SSLCertContent = "rotated-cert-pem"
	if domainProxyMatches(&changedCert, proxy) {
		t.Fatal("same-second certificate rotation must not be skipped")
	}
}

func TestDomainProxyMatchesDetectsDesiredStateChanges(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	domain := &domainModel.Domain{
		DomainName:   "app.example.test",
		InternalIP:   "192.0.2.3",
		InternalPort: 8080,
		Protocol:     "http",
		UpdatedAt:    now,
	}
	proxy := agentService.DomainProxyItem{
		Domain:       "app.example.test",
		InternalIP:   "192.0.2.3",
		InternalPort: 8080,
		Protocol:     "http",
		CreatedAt:    now.Unix(),
	}
	proxy.ConfigHash = domainProxyConfigHash(domain)
	for name, mutate := range map[string]func(*domainModel.Domain){
		"target address":    func(value *domainModel.Domain) { value.InternalIP = "192.0.2.4" },
		"target port":       func(value *domainModel.Domain) { value.InternalPort = 8081 },
		"upstream protocol": func(value *domainModel.Domain) { value.Protocol = "https" },
		"tls state":         func(value *domainModel.Domain) { value.EnableSSL = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *domain
			mutate(&changed)
			if domainProxyMatches(&changed, proxy) {
				t.Fatalf("changed %s must not be skipped", name)
			}
		})
	}
}

func TestDeleteInstanceDomainsInTxKeepsCleanupTombstone(t *testing.T) {
	db := setupDomainServiceTestDB(t)
	domain := &domainModel.Domain{
		UserID:       7,
		InstanceID:   11,
		ProviderID:   13,
		DomainName:   "app.example.test",
		InternalIP:   "192.0.2.10",
		InternalPort: 8080,
		Status:       "active",
	}
	if err := db.Create(domain).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&Service{}).DeleteInstanceDomainsInTx(db, domain.InstanceID); err != nil {
		t.Fatal(err)
	}
	var saved domainModel.Domain
	if err := db.First(&saved, domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != domainStatusDeleting {
		t.Fatalf("domain status = %q, want %q", saved.Status, domainStatusDeleting)
	}
}

func TestGetDomainConfigRejectsMissingProvider(t *testing.T) {
	db := setupDomainServiceTestDB(t)
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Service{}).GetDomainConfig(404); err == nil {
		t.Fatal("missing provider returned a synthetic domain configuration")
	}
}

func TestUpdateDomainConfigRejectsNilRequest(t *testing.T) {
	if err := (&Service{}).UpdateDomainConfig(1, nil); err == nil {
		t.Fatal("nil domain configuration request was accepted")
	}
}
