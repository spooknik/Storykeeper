package library

import "testing"

func TestScanCoalesce(t *testing.T) {
	s := New(nil, "")
	if !s.beginScan(1) {
		t.Fatal("first begin should succeed")
	}
	if s.beginScan(1) {
		t.Fatal("second begin should be refused")
	}
	if !s.endScan(1) {
		t.Fatal("endScan should report a queued rescan")
	}
	if s.endScan(1) {
		t.Fatal("no rescan should be queued after it was consumed")
	}
}
