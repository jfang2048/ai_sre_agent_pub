package ring

import "testing"

func TestRing_PushAndSlice(t *testing.T) {
	r := New[int](3)
	if r == nil {
		t.Fatalf("expected non-nil ring")
	}

	if got := r.SliceOldest(); got != nil {
		t.Fatalf("expected nil slice, got %#v", got)
	}

	r.Push(1)
	r.Push(2)
	if got := r.SliceOldest(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("unexpected slice: %#v", got)
	}

	r.Push(3)
	if got := r.SliceOldest(); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("unexpected slice: %#v", got)
	}

	// Wrap overwrite oldest.
	r.Push(4)
	if got := r.SliceOldest(); len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("unexpected slice after wrap: %#v", got)
	}

	if got := r.SliceLastN(2); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("unexpected last2: %#v", got)
	}

	if got, ok := r.Oldest(); !ok || got != 2 {
		t.Fatalf("unexpected oldest: %v %v", got, ok)
	}
	if got, ok := r.Newest(); !ok || got != 4 {
		t.Fatalf("unexpected newest: %v %v", got, ok)
	}
}
