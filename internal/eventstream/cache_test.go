package eventstream

import (
	"sync"
	"testing"
)

// ─── 测试辅助 ─────────────────────────────────────────────────────

// makeRes 创建一个最小测试 Resource。
func makeRes(ns, name string) *Resource {
	return &Resource{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  ns,
		Name:       name,
		UID:        ns + "-" + name,
		RawJSON:    []byte(`{}`),
	}
}

// ─── Get / Put 基本场景 ────────────────────────────────────────────

func TestCache_PutGet(t *testing.T) {
	c := NewCache()
	r := makeRes("default", "app")
	c.Put(r)

	got, ok := c.Get("default", "app")
	if !ok {
		t.Fatal("Get 应返回 ok=true")
	}
	if got.Name != "app" {
		t.Errorf("Get name = %q, want %q", got.Name, "app")
	}
}

func TestCache_GetNotFound(t *testing.T) {
	c := NewCache()
	_, ok := c.Get("default", "nonexistent")
	if ok {
		t.Error("不存在的对象应返回 ok=false")
	}
}

func TestCache_GetNonexistentNamespace(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "app"))

	_, ok := c.Get("ns2", "app")
	if ok {
		t.Error("不存在的 ns 应返回 ok=false")
	}
}

func TestCache_PutOverwrite(t *testing.T) {
	c := NewCache()
	r1 := makeRes("default", "app")
	r1.Generation = 1
	c.Put(r1)

	r2 := makeRes("default", "app")
	r2.Generation = 2
	c.Put(r2)

	got, _ := c.Get("default", "app")
	if got.Generation != 2 {
		t.Errorf("Generation = %d, want 2 (覆盖)", got.Generation)
	}
}

func TestCache_PutNil(t *testing.T) {
	c := NewCache()
	c.Put(nil) // 不应 panic
	if got := c.Stats().TotalItems; got != 0 {
		t.Errorf("Put(nil) 后应仍为空，got %d", got)
	}
}

// ─── List 场景 ─────────────────────────────────────────────────────

func TestCache_ListByNamespace(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "app1"))
	c.Put(makeRes("ns1", "app2"))
	c.Put(makeRes("ns2", "app1"))

	items := c.List("ns1")
	if len(items) != 2 {
		t.Errorf("List(ns1) len = %d, want 2", len(items))
	}

	items = c.List("ns2")
	if len(items) != 1 {
		t.Errorf("List(ns2) len = %d, want 1", len(items))
	}
}

func TestCache_ListEmptyNamespace(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "app1"))

	items := c.List("nonexistent")
	if items != nil {
		t.Errorf("不存在的 ns List 应返回 nil，got %v", items)
	}
}

func TestCache_ListWithEmptyString(t *testing.T) {
	// ns="" 时等同 ListAll
	c := NewCache()
	c.Put(makeRes("ns1", "app1"))
	c.Put(makeRes("ns2", "app2"))

	items := c.List("")
	if len(items) != 2 {
		t.Errorf("List(\"\") len = %d, want 2 (= ListAll)", len(items))
	}
}

func TestCache_ListAll(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "app1"))
	c.Put(makeRes("ns1", "app2"))
	c.Put(makeRes("ns2", "app1"))

	items := c.ListAll()
	if len(items) != 3 {
		t.Errorf("ListAll len = %d, want 3", len(items))
	}
}

func TestCache_ListAllEmpty(t *testing.T) {
	c := NewCache()
	items := c.ListAll()
	if len(items) != 0 {
		t.Errorf("空 cache ListAll len = %d, want 0", len(items))
	}
}

// ─── PutBulk 场景 ─────────────────────────────────────────────────

func TestCache_PutBulk(t *testing.T) {
	c := NewCache()
	items := []*Resource{
		makeRes("ns1", "app1"),
		makeRes("ns1", "app2"),
		makeRes("ns2", "app1"),
	}
	c.PutBulk(items)

	if got := c.Stats().TotalItems; got != 3 {
		t.Errorf("PutBulk 后总数 = %d, want 3", got)
	}
	if got := c.Stats().NamespaceCount; got != 2 {
		t.Errorf("PutBulk 后 ns 数 = %d, want 2", got)
	}
}

func TestCache_PutBulkEmpty(t *testing.T) {
	c := NewCache()
	c.PutBulk(nil)           // 不应 panic
	c.PutBulk([]*Resource{}) // 不应 panic
	if got := c.Stats().TotalItems; got != 0 {
		t.Errorf("PutBulk(empty) 后应仍为空，got %d", got)
	}
}

func TestCache_PutBulkMergeExisting(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "old-app"))

	items := []*Resource{
		makeRes("ns1", "new-app1"),
		makeRes("ns1", "new-app2"),
	}
	c.PutBulk(items)

	// ns1 应有 3 个对象（1 旧 + 2 新）
	got := c.List("ns1")
	if len(got) != 3 {
		t.Errorf("PutBulk merge 后 ns1 = %d, want 3", len(got))
	}
}

func TestCache_PutBulkSkipsNil(t *testing.T) {
	c := NewCache()
	items := []*Resource{
		makeRes("ns1", "app1"),
		nil, // 应被跳过
		makeRes("ns1", "app2"),
	}
	c.PutBulk(items)

	if got := c.Stats().TotalItems; got != 2 {
		t.Errorf("PutBulk(含 nil) 后总数 = %d, want 2", got)
	}
}

// ─── Delete 场景 ──────────────────────────────────────────────────

func TestCache_Delete(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("default", "app"))
	c.Delete("default", "app")

	if _, ok := c.Get("default", "app"); ok {
		t.Error("Delete 后 Get 应返回 ok=false")
	}
}

func TestCache_DeleteNonexistent(t *testing.T) {
	c := NewCache()
	c.Delete("ns", "app") // 不应 panic
	c.Put(makeRes("ns", "app"))
	c.Delete("other-ns", "app") // 不存在的 ns
	c.Delete("ns", "other-app") // 不存在的 name

	if got := c.Stats().TotalItems; got != 1 {
		t.Errorf("无效 Delete 不应影响 cache，got %d items", got)
	}
}

func TestCache_DeleteEmptiesNamespace(t *testing.T) {
	c := NewCache()
	c.Put(makeRes("ns1", "only-app"))
	c.Delete("ns1", "only-app")

	// 删完后 ns 应被移除（不留空 map）
	stats := c.Stats()
	if stats.NamespaceCount != 0 {
		t.Errorf("删完最后一个对象后 NamespaceCount = %d, want 0", stats.NamespaceCount)
	}
}

// ─── Stats 测试 ────────────────────────────────────────────────────

func TestCache_StatsEmpty(t *testing.T) {
	c := NewCache()
	stats := c.Stats()
	if stats.TotalItems != 0 {
		t.Errorf("空 cache TotalItems = %d, want 0", stats.TotalItems)
	}
	if stats.NamespaceCount != 0 {
		t.Errorf("空 cache NamespaceCount = %d, want 0", stats.NamespaceCount)
	}
}

func TestCache_StatsBytesEstimated(t *testing.T) {
	c := NewCache()
	r := makeRes("default", "app")
	r.RawJSON = []byte("0123456789") // 10 bytes
	c.Put(r)

	stats := c.Stats()
	// 200 (estimate skeleton) + 10 (RawJSON) = 210
	if stats.BytesEstimated < 210 || stats.BytesEstimated > 220 {
		t.Errorf("BytesEstimated = %d, want ~210", stats.BytesEstimated)
	}
}

// ─── 并发安全测试 ─────────────────────────────────────────────────

func TestCache_ConcurrentReadWrite(t *testing.T) {
	c := NewCache()

	// 预填一些数据
	for i := 0; i < 100; i++ {
		c.Put(makeRes("ns", "app-"+itoa(i)))
	}

	var wg sync.WaitGroup
	const numReaders = 10
	const numWriters = 5
	const ops = 1000

	// 并发读
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				_, _ = c.Get("ns", "app-"+itoa(j%100))
				_ = c.List("ns")
			}
		}(i)
	}

	// 并发写
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops/10; j++ {
				c.Put(makeRes("ns", "writer-"+itoa(id)+"-"+itoa(j)))
			}
		}(i)
	}

	wg.Wait()
	// 没有 panic 即测试通过；race detector 跑出问题再说
}

// itoa 简单 int → string 转换（避免在测试中 import strconv）
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 5)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
