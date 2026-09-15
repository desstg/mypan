package cache

import (
	"container/list"
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"litepan/pkg/singleflight"
)

// Service 元数据 LRU 缓存：TTL + 字节软上限 + singleflight。
type Service struct {
	mu       sync.Mutex
	ll       *list.List // LRU：队首最新，队尾最旧
	items    map[string]*list.Element
	maxItems int
	memLimit int64 // 字节软上限，<=0 不限
	curMem   int64

	hits        atomic.Int64
	misses      atomic.Int64
	evictions   atomic.Int64
	expirations atomic.Int64

	sf    singleflight.Group[any]
	fence mutationFence

	stop chan struct{}
	once sync.Once

	persistMu       sync.Mutex
	persistDir      string
	persistEnabled  bool
	persistInterval time.Duration
	persistStop     chan struct{}
}

type entry struct {
	key       string
	value     any
	size      int64
	expiresAt time.Time // 零值表示不过期
}

// Stats 是底层缓存统计，区别于面向请求的 HitTracker。
type Stats struct {
	Hits        int64
	Misses      int64
	Evictions   int64
	Expirations int64
	Items       int
	Bytes       int64
}

// Options 构造缓存所需参数。
type Options struct {
	MaxItems   int           // 条数上限（主约束），<=0 不限
	MemLimit   int64         // 字节软上限（辅约束），<=0 不限
	GCInterval time.Duration // 过期清理间隔，<=0 用默认 1 分钟
}

func NewService(opts Options) *Service {
	if opts.GCInterval <= 0 {
		opts.GCInterval = time.Minute
	}
	s := &Service{
		ll:       list.New(),
		items:    make(map[string]*list.Element),
		maxItems: opts.MaxItems,
		memLimit: opts.MemLimit,
		stop:     make(chan struct{}),
	}
	go s.gcLoop(opts.GCInterval)
	return s
}

// Close 停止后台 GC 与持久化调度。可重复调用。
func (s *Service) Close() {
	s.once.Do(func() {
		s.stopPersistence()
		close(s.stop)
	})
}

// Get 读取缓存并记录命中/未命中统计。过期项视为未命中并删除。
func (s *Service) Get(key string) (any, bool) {
	v, ok := s.lookup(key)
	if ok {
		s.hits.Add(1)
	} else {
		s.misses.Add(1)
	}
	return v, ok
}

// Set 写入或更新缓存项。ttl<=0 表示不过期。
func (s *Service) Set(key string, val any, ttl time.Duration) {
	size := entrySize(key, val)
	exp := time.Time{}
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[key]; ok {
		en := el.Value.(*entry)
		s.curMem += size - en.size
		en.value, en.size, en.expiresAt = val, size, exp
		s.ll.MoveToFront(el)
		s.ensureCapacity()
		return
	}
	el := s.ll.PushFront(&entry{key: key, value: val, size: size, expiresAt: exp})
	s.items[key] = el
	s.curMem += size
	s.ensureCapacity()
}

// RemainingTTL 返回键的剩余 TTL；不过期项 ok=true 且返回 0。
func (s *Service) RemainingTTL(key string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.items[key]
	if !ok {
		return 0, false
	}
	en := el.Value.(*entry)
	if en.expiresAt.IsZero() {
		return 0, true
	}
	rem := time.Until(en.expiresAt)
	if rem <= 0 {
		return 0, false
	}
	return rem, true
}

// Coalesce 对同一 key 合并并发调用，供调用方在 loader 内自行 Set（如动态 TTL）。
func (s *Service) Coalesce(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	return s.sf.DoCtx(ctx, key, fn)
}

// GetOrLoad 命中即返回；未命中经 singleflight 调 loader 加载并写入（空结果也缓存防穿透）。
func (s *Service) GetOrLoad(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (any, error)) (any, bool, error) {
	if v, ok := s.lookup(key); ok {
		s.hits.Add(1)
		return v, true, nil
	}

	v, err := s.sf.DoCtx(ctx, key, func(callCtx context.Context) (any, error) {
		if v, ok := s.lookup(key); ok {
			return v, nil
		}
		val, err := loader(callCtx)
		if err != nil {
			return nil, err
		}
		s.Set(key, val, ttl)
		return val, nil
	})
	if err != nil {
		return nil, false, err
	}
	s.misses.Add(1)
	return v, false, nil
}

// InvalidateKey 失效单个键。
func (s *Service) InvalidateKey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[key]; ok {
		s.removeElement(el)
	}
}

// InvalidatePrefix 失效所有以 prefix 开头的键。
func (s *Service) InvalidatePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rm []*list.Element
	for k, el := range s.items {
		if strings.HasPrefix(k, prefix) {
			rm = append(rm, el)
		}
	}
	for _, el := range rm {
		s.removeElement(el)
	}
}

// InvalidateAccount 失效某账号的全部元数据与 WebDAV 缓存。
func (s *Service) InvalidateAccount(accountID int64) {
	for _, p := range accountTypePrefixes(accountID) {
		s.InvalidatePrefix(p)
	}
}

// InvalidateAccountType 失效某账号在某一缓存类型下的全部键。
func (s *Service) InvalidateAccountType(accountID int64, t CacheType) {
	s.InvalidatePrefix(string(t) + sep + strconv.FormatInt(accountID, 10) + sep)
}

// ClearAll 清空全部缓存项，返回清理条数。
func (s *Service) ClearAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.items)
	s.items = make(map[string]*list.Element)
	s.ll = list.New()
	s.curMem = 0
	return n
}

// AccountStats 返回某账号的缓存条目数与估算字节。
func (s *Service) AccountStats(accountID int64) (count int, bytes int64) {
	prefixes := accountTypePrefixes(accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, el := range s.items {
		en := el.Value.(*entry)
		for _, p := range prefixes {
			if strings.HasPrefix(en.key, p) {
				count++
				bytes += en.size
				break
			}
		}
	}
	return count, bytes
}

// ApplyLimits 运行时更新条数/内存上限（持久化类设置变更无需重启时可调用）。
func (s *Service) ApplyLimits(maxItems int, memLimit int64) {
	s.mu.Lock()
	s.maxItems = maxItems
	s.memLimit = memLimit
	s.mu.Unlock()
	s.ensureCapacityLocked()
}

func (s *Service) ensureCapacityLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureCapacity()
}

// Stats 返回当前统计快照。
func (s *Service) Stats() Stats {
	s.mu.Lock()
	items, bytes := len(s.items), s.curMem
	s.mu.Unlock()
	return Stats{
		Hits:        s.hits.Load(),
		Misses:      s.misses.Load(),
		Evictions:   s.evictions.Load(),
		Expirations: s.expirations.Load(),
		Items:       items,
		Bytes:       bytes,
	}
}

func (s *Service) lookup(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	el, ok := s.items[key]
	if !ok {
		return nil, false
	}
	en := el.Value.(*entry)
	if !en.expiresAt.IsZero() && time.Now().After(en.expiresAt) {
		s.removeElement(el)
		s.expirations.Add(1)
		return nil, false
	}
	s.ll.MoveToFront(el)
	return en.value, true
}

func (s *Service) ensureCapacity() {
	if s.memLimit > 0 {
		switch {
		case s.curMem > s.memLimit:
			s.evictToBytes(int64(float64(s.memLimit) * 0.7))
		case s.curMem > int64(float64(s.memLimit)*0.8):
			s.evictBatch(s.batchCount())
		}
	}
	if s.maxItems > 0 {
		for len(s.items) > s.maxItems {
			if !s.evictOldest() {
				break
			}
		}
	}
}

func (s *Service) batchCount() int {
	n := len(s.items) / 10
	if n < 1 {
		n = 1
	}
	if n > 100 {
		n = 100
	}
	return n
}

func (s *Service) evictBatch(n int) {
	for i := 0; i < n; i++ {
		if !s.evictOldest() {
			return
		}
	}
}

func (s *Service) evictToBytes(target int64) {
	for s.curMem > target {
		if !s.evictOldest() {
			return
		}
	}
}

func (s *Service) evictOldest() bool {
	el := s.ll.Back()
	if el == nil {
		return false
	}
	s.removeElement(el)
	s.evictions.Add(1)
	return true
}

// removeElement 调用方需持有 mu。
func (s *Service) removeElement(el *list.Element) {
	en := el.Value.(*entry)
	s.ll.Remove(el)
	delete(s.items, en.key)
	s.curMem -= en.size
}

func (s *Service) gcLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.sweepExpired()
		case <-s.stop:
			return
		}
	}
}

func (s *Service) sweepExpired() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []*list.Element
	for _, el := range s.items {
		en := el.Value.(*entry)
		if !en.expiresAt.IsZero() && now.After(en.expiresAt) {
			expired = append(expired, el)
		}
	}
	for _, el := range expired {
		s.removeElement(el)
		s.expirations.Add(1)
	}
}
