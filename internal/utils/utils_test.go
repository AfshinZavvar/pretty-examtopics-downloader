package utils

import (
	"context"
	"testing"
	"time"
)

func TestCleanTextRemovesVoteEmoji(t *testing.T) {
	input := "Refer to the exhibit. Which type of route does R1 use to reach host 10.10.13.10/32?\n🗳️"

	got := CleanText(input)
	want := "Refer to the exhibit. Which type of route does R1 use to reach host 10.10.13.10/32?"
	if got != want {
		t.Fatalf("unexpected cleaned text\nwant: %q\ngot:  %q", want, got)
	}
}

func TestCleanTextRemovesVoteEmojiWithoutVariationSelector(t *testing.T) {
	input := "Suggested Answer: D 🗳"

	got := CleanText(input)
	want := "\nSuggested Answer: D"
	if got != want {
		t.Fatalf("unexpected cleaned text\nwant: %q\ngot:  %q", want, got)
	}
}

func TestAdaptiveLimiterWaitContextAfterUsesGreaterDelay(t *testing.T) {
	limiter := NewAdaptiveLimiter(10, 10, 10, 1, 1)
	started := time.Now()
	if err := limiter.WaitContextAfter(context.Background(), 150*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if elapsed < 130*time.Millisecond || elapsed > 350*time.Millisecond {
		t.Fatalf("expected one combined wait near 150ms, got %v", elapsed)
	}
}
