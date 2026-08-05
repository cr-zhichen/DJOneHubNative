package smscodec

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestReassemblerPassesThroughNonConcat(t *testing.T) {
	r := NewReassembler()

	complete, full := r.Add("10086", ConcatInfo{}, "hello")
	if !complete || full != "hello" {
		t.Fatalf("Add(non-concat) = (%v, %q), want (true, hello)", complete, full)
	}
}

func TestReassemblerReassemblesOutOfOrder(t *testing.T) {
	r := NewReassembler()
	concat2 := ConcatInfo{IsConcat: true, Ref: 7, Total: 2, Seq: 2}
	concat1 := ConcatInfo{IsConcat: true, Ref: 7, Total: 2, Seq: 1}

	if complete, full := r.Add("10086", concat2, "world"); complete || full != "" {
		t.Fatalf("first Add = (%v, %q), want (false, \"\")", complete, full)
	}
	complete, full := r.Add("10086", concat1, "hello ")
	if !complete || full != "hello world" {
		t.Fatalf("second Add = (%v, %q), want (true, \"hello world\")", complete, full)
	}
}

func TestReassemblerDeduplicatesSequence(t *testing.T) {
	r := NewReassembler()
	concat1 := ConcatInfo{IsConcat: true, Ref: 9, Total: 2, Seq: 1}
	concat2 := ConcatInfo{IsConcat: true, Ref: 9, Total: 2, Seq: 2}

	if complete, _ := r.Add("10010", concat1, "foo"); complete {
		t.Fatal("first seq should not complete")
	}
	if complete, _ := r.Add("10010", concat1, "foo-dup"); complete {
		t.Fatal("duplicate seq should not complete")
	}
	complete, full := r.Add("10010", concat2, "bar")
	if !complete || full != "foobar" {
		t.Fatalf("final Add = (%v, %q), want (true, \"foobar\")", complete, full)
	}
}

func TestReassemblerCleanupRemovesExpiredGroup(t *testing.T) {
	r := NewReassembler()
	concat1 := ConcatInfo{IsConcat: true, Ref: 11, Total: 2, Seq: 1}
	concat2 := ConcatInfo{IsConcat: true, Ref: 11, Total: 2, Seq: 2}

	if complete, _ := r.Add("alice", concat1, "part1"); complete {
		t.Fatal("first seq should not complete")
	}
	r.Cleanup(0)
	if complete, full := r.Add("alice", concat2, "part2"); complete || full != "" {
		t.Fatalf("Add after Cleanup(0) = (%v, %q), want (false, \"\")", complete, full)
	}
}

func TestReassemblerConcurrentAddDifferentSenders(t *testing.T) {
	r := NewReassembler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sender := fmt.Sprintf("sender-%d", i)
			concat1 := ConcatInfo{IsConcat: true, Ref: i, Total: 2, Seq: 1}
			concat2 := ConcatInfo{IsConcat: true, Ref: i, Total: 2, Seq: 2}
			if complete, _ := r.Add(sender, concat1, "foo"); complete {
				t.Errorf("%s first Add unexpectedly completed", sender)
			}
			if complete, full := r.Add(sender, concat2, "bar"); !complete || full != "foobar" {
				t.Errorf("%s second Add = (%v, %q), want (true, \"foobar\")", sender, complete, full)
			}
		}()
	}
	wg.Wait()
}

func TestReassemblerCleanupExpiresByLatestFragmentAge(t *testing.T) {
	r := NewReassembler()
	r.cache["bob_5"] = []Fragment{{Ref: 5, Total: 2, Seq: 1, Content: "x", Time: time.Now().Add(-time.Minute)}}
	r.Cleanup(30 * time.Second)
	if _, ok := r.cache["bob_5"]; ok {
		t.Fatal("expected expired group to be removed")
	}
}
