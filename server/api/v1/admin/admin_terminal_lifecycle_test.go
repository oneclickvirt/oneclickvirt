package admin

import "testing"

func TestAdminTerminalOldReleaseCannotDeleteReplacement(t *testing.T) {
	const providerID = 987654
	first, releaseFirst := acquireAdminTerminal(providerID)
	second, releaseSecond := acquireAdminTerminal(providerID)
	defer releaseSecond()
	select {
	case <-first.Done():
	default:
		t.Fatal("old terminal not cancelled")
	}
	releaseFirst()
	third, releaseThird := acquireAdminTerminal(providerID)
	defer releaseThird()
	select {
	case <-second.Done():
	default:
		t.Fatal("old release erased replacement ownership")
	}
	select {
	case <-third.Done():
		t.Fatal("new terminal already cancelled")
	default:
	}
	releaseSecond()
	fourth, releaseFourth := acquireAdminTerminal(providerID)
	defer releaseFourth()
	select {
	case <-third.Done():
	default:
		t.Fatal("stale release erased latest terminal")
	}
	select {
	case <-fourth.Done():
		t.Fatal("latest terminal already cancelled")
	default:
	}
}
