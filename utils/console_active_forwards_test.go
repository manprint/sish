package utils

import (
	"testing"
)

func TestFinalizeActiveForwardListDedupesAndSorts(t *testing.T) {
	input := []string{
		"dufs.example.com",
		"myalias:8999",
		"dufs.example.com",
		"  myalias:8999  ",
		"",
		"   ",
	}

	got := finalizeActiveForwardList(input, "VIS-pending")

	want := []string{"VIS-pending", "dufs.example.com", "myalias:8999"}
	if len(got) != len(want) {
		t.Fatalf("len(finalizeActiveForwardList)=%d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("finalizeActiveForwardList[%d]=%q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestFinalizeActiveForwardListDedupesPendingForwarder(t *testing.T) {
	input := []string{
		"VIS-pending",
		"myalias:8999",
		"VIS-pending",
	}

	got := finalizeActiveForwardList(input, "VIS-pending")

	want := []string{"VIS-pending", "myalias:8999"}
	if len(got) != len(want) {
		t.Fatalf("len(finalizeActiveForwardList)=%d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("finalizeActiveForwardList[%d]=%q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

