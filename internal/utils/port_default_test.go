package utils

import "testing"

func TestDefaultRecommendPortStart(t *testing.T) {
	if DefaultRecommendPortStart != 11000 {
		t.Fatalf("DefaultRecommendPortStart = %d, want 11000", DefaultRecommendPortStart)
	}
}
