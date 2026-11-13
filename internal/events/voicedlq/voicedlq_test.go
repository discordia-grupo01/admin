package voicedlq

import "testing"

func TestQueueNames(t *testing.T) {
	t.Run("trims and deduplicates voice queues", func(t *testing.T) {
		got, err := QueueNames(" voice.a,voice.b,voice.a ")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != "voice.a" || got[1] != "voice.b" {
			t.Fatalf("QueueNames returned %#v", got)
		}
	})

	t.Run("rejects non voice queues", func(t *testing.T) {
		if _, err := QueueNames("admin.events"); err == nil {
			t.Fatal("expected non-voice queue to be rejected")
		}
	})

	t.Run("requires a queue", func(t *testing.T) {
		if _, err := QueueNames(" , "); err == nil {
			t.Fatal("expected empty queue list to be rejected")
		}
	})
}
