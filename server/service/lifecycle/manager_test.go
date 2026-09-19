package lifecycle

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

type reentrantService struct {
	manager *LifecycleManager
	done    chan struct{}
}

func (s *reentrantService) Stop() {
	// ShutdownAll must not hold the manager read lock while invoking this.
	s.manager.Register("registered-during-shutdown", struct{}{})
	close(s.done)
}

func TestShutdownAllAllowsReentrantRegistration(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()

	m := &LifecycleManager{}
	done := make(chan struct{})
	m.Register("reentrant", &reentrantService{manager: m, done: done})

	finished := make(chan struct{})
	go func() {
		m.ShutdownAll(time.Second)
		close(finished)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("service shutdown could not re-enter lifecycle registration")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("lifecycle shutdown did not finish")
	}
}

func TestRegisterCanProceedWhileShutdownRuns(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()

	started := make(chan struct{})
	release := make(chan struct{})
	m := &LifecycleManager{}
	m.Register("blocking", serviceFunc(func() {
		close(started)
		<-release
	}))

	shutdownDone := make(chan struct{})
	go func() {
		m.ShutdownAll(time.Second)
		close(shutdownDone)
	}()
	<-started

	registered := make(chan struct{})
	go func() {
		m.Register("concurrent", struct{}{})
		close(registered)
	}()
	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("registration blocked behind a running shutdown callback")
	}
	close(release)
	<-shutdownDone
}

type serviceFunc func()

func (f serviceFunc) Stop() { f() }
