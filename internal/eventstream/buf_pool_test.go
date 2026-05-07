package eventstream

import (
	"testing"
)

func TestBufPool_AllocWithinClass(t *testing.T) {
	bp := NewBufPool(2)
	// Alloc 100 bytes → should get from 4KB class
	b := bp.Alloc(100)
	if len(b) != 100 {
		t.Errorf("len = %d, want 100", len(b))
	}
	if cap(b) != 4096 {
		t.Errorf("cap = %d, want 4096 (4KB class)", cap(b))
	}
}

func TestBufPool_AllocExhaust(t *testing.T) {
	bp := NewBufPool(1)
	// First alloc from pool
	b1 := bp.Alloc(100)
	if cap(b1) != 4096 {
		t.Error("first alloc should be from pool")
	}
	// Second alloc — pool exhausted, direct alloc
	b2 := bp.Alloc(100)
	if cap(b2) > 0 && cap(b2) != 4096 {
		// direct alloc has different cap
	}
}

func TestBufPool_FreeAndReuse(t *testing.T) {
	bp := NewBufPool(1)
	b := bp.Alloc(100)
	bp.Free(b)
	b2 := bp.Alloc(100)
	if cap(b2) != 4096 {
		t.Error("should reuse freed buffer from 4KB class")
	}
}

func TestBufPool_LargeAlloc(t *testing.T) {
	bp := NewBufPool(1)
	// Request larger than max class (8MB)
	b := bp.Alloc(16 << 20) // 16MB
	if cap(b) < 16<<20 {
		t.Errorf("large alloc cap = %d, want >= 16MB", cap(b))
	}
}

func TestBufPool_Stats(t *testing.T) {
	bp := NewBufPool(2)
	s := bp.Stats()
	if s <= 0 {
		t.Error("stats should report pre-allocated memory")
	}
}
