// internal/eventstream/buf_pool.go — v3.2: 链式 Slab Allocator
//
// BufPool 是预分配的固定大小类内存池，用于高频 alloc/free 场景。
// 链式指针数组：4KB → 16KB → 64KB → 256KB → 1MB → 4MB → 8MB。
// 系统启动时预分配所有内存块（利用 OS lazy page commit）。
//
// 激活条件：Go GC >10% CPU（生产 pprof 驱动）。
// 当前阶段：代码就绪，默认不启用。

package eventstream

import (
	"sync"
)

// slabClass represents one size class in the slab allocator.
type slabClass struct {
	size     int      // block size
	freeList [][]byte // chain of free blocks
	mu       sync.Mutex
}

// BufPool is a slab allocator with fixed size classes.
type BufPool struct {
	classes []*slabClass
}

// slabSizes defines the size classes (powers of 4).
var slabSizes = []int{
	4 << 10,   // 4 KB
	16 << 10,  // 16 KB
	64 << 10,  // 64 KB
	256 << 10, // 256 KB
	1 << 20,   // 1 MB
	4 << 20,   // 4 MB
	8 << 20,   // 8 MB
}

// NewBufPool creates a slab allocator with pre-allocated blocks.
// count is the number of blocks per size class.
func NewBufPool(count int) *BufPool {
	bp := &BufPool{classes: make([]*slabClass, len(slabSizes))}
	for i, size := range slabSizes {
		sc := &slabClass{size: size, freeList: make([][]byte, 0, count)}
		for j := 0; j < count; j++ {
			sc.freeList = append(sc.freeList, make([]byte, size))
		}
		bp.classes[i] = sc
	}
	return bp
}

// Alloc returns a byte slice of at least the requested size.
// Returns the smallest size class that fits, or allocates directly if too large.
func (bp *BufPool) Alloc(size int) []byte {
	for _, sc := range bp.classes {
		if sc.size >= size {
			sc.mu.Lock()
			if len(sc.freeList) > 0 {
				b := sc.freeList[len(sc.freeList)-1]
				sc.freeList = sc.freeList[:len(sc.freeList)-1]
				sc.mu.Unlock()
				return b[:size]
			}
			sc.mu.Unlock()
			break // pool exhausted, fall through to direct alloc
		}
	}
	return make([]byte, size) // too large or pool exhausted
}

// Free returns a buffer to the pool.
func (bp *BufPool) Free(b []byte) {
	cap := cap(b)
	for _, sc := range bp.classes {
		if sc.size == cap {
			sc.mu.Lock()
			sc.freeList = append(sc.freeList, b[:cap])
			sc.mu.Unlock()
			return
		}
	}
	// Not a pooled size — let GC handle it
}

// Stats returns total pre-allocated memory across all slabs.
func (bp *BufPool) Stats() int64 {
	var total int64
	for _, sc := range bp.classes {
		sc.mu.Lock()
		total += int64(len(sc.freeList) * sc.size)
		sc.mu.Unlock()
	}
	return total
}
