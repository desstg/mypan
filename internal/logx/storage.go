package logx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	writeQueueSize       = 4096
	writeBatchSize       = 32
	writeFlushWait       = time.Second
	reverseReadBlockSize = 64 * 1024
	statsCacheTTL        = 30 * time.Second
)

// Storage 负责异步按天落盘与检索。
type Storage struct {
	dir      string
	queue    chan Entry
	stop     chan struct{}
	done     chan struct{}
	fallback string

	statsMu    sync.Mutex
	statsCache statsSnapshot
}

type statsSnapshot struct {
	minLevel  int
	ackAfter  string
	value     Stats
	expiresAt time.Time
	valid     bool
}

// OpenStorage 创建日志目录并启动后台写入协程。
func OpenStorage(dir string) (*Storage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	s := &Storage{
		dir:      dir,
		queue:    make(chan Entry, writeQueueSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		fallback: filepath.Join(dir, "_fallback.log"),
	}
	go s.writerLoop()
	return s, nil
}

func (s *Storage) Enqueue(e Entry) {
	select {
	case s.queue <- e:
	default:
		_ = s.appendFallback([]Entry{e})
	}
}

func (s *Storage) Close(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	close(s.stop)
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Storage) writerLoop() {
	defer close(s.done)
	batch := make([]Entry, 0, writeBatchSize)
	flush := time.NewTimer(writeFlushWait)
	defer flush.Stop()

	for {
		select {
		case <-s.stop:
			s.drainQueue(&batch)
			if len(batch) > 0 {
				_ = s.writeBatch(batch)
			}
			return
		case e := <-s.queue:
			batch = append(batch, e)
			if len(batch) >= writeBatchSize {
				_ = s.writeBatch(batch)
				batch = batch[:0]
				if !flush.Stop() {
					<-flush.C
				}
				flush.Reset(writeFlushWait)
			}
		case <-flush.C:
			if len(batch) > 0 {
				_ = s.writeBatch(batch)
				batch = batch[:0]
			}
			flush.Reset(writeFlushWait)
		}
	}
}

func (s *Storage) drainQueue(batch *[]Entry) {
	for {
		select {
		case e := <-s.queue:
			*batch = append(*batch, e)
		default:
			return
		}
	}
}

func (s *Storage) writeBatch(batch []Entry) error {
	if len(batch) == 0 {
		return nil
	}
	grouped := map[string][]byte{}
	for _, e := range batch {
		n := normalizeEntry(e)
		line, err := json.Marshal(n)
		if err != nil {
			continue
		}
		date := time.Now().Format("2006-01-02")
		if len(n.Timestamp) >= 10 {
			date = n.Timestamp[:10]
		}
		grouped[date] = append(grouped[date], append(line, '\n')...)
	}
	for date, data := range grouped {
		path := filepath.Join(s.dir, date+".log")
		if err := appendFile(path, data); err != nil {
			_ = s.appendFallback(batch)
			return err
		}
	}
	return nil
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (s *Storage) appendFallback(batch []Entry) error {
	var buf strings.Builder
	for _, e := range batch {
		n := normalizeEntry(e)
		line, err := json.Marshal(n)
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return appendFile(s.fallback, []byte(buf.String()))
}

func normalizeEntry(e Entry) Entry {
	if e.Timestamp == "" {
		e.Timestamp = time.Now().Format(time.RFC3339)
	}
	if e.Module == "" {
		e.Module = string(ModuleSystem)
	}
	if e.Level == 0 {
		e.Level = LevelInfo
	}
	if e.Details == nil {
		e.Details = nil
	}
	return e
}

func (s *Storage) listFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		if ent.Name() == "_fallback.log" {
			continue
		}
		files = append(files, ent.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

// Query 检索落盘日志（新→旧，带 limit/offset）。
func (s *Storage) Query(f QueryFilter) ([]Entry, error) {
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	matched := make([]Entry, 0, limit)
	skipped := 0
	for _, name := range files {
		path := filepath.Join(s.dir, name)
		stopped, err := scanLinesReverse(path, func(line []byte) bool {
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil || !matchFilter(e, f) {
				return true
			}
			if skipped < offset {
				skipped++
				return true
			}
			matched = append(matched, e)
			return len(matched) < limit
		})
		if err != nil {
			continue
		}
		if stopped {
			break
		}
	}
	return matched, nil
}

func matchFilter(e Entry, f QueryFilter) bool {
	if f.MinLevel != nil && e.Level < *f.MinLevel {
		return false
	}
	if f.Level != nil && e.Level != *f.Level {
		return false
	}
	if f.Module != "" {
		groupMods := ModulesInGroup(f.Module)
		if len(groupMods) > 1 || groupMods[0] != f.Module {
			found := false
			for _, m := range groupMods {
				if e.Module == m {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		} else if e.Module != f.Module {
			return false
		}
	}
	if f.StartTime != "" && e.Timestamp < f.StartTime {
		return false
	}
	if f.EndTime != "" && e.Timestamp > f.EndTime {
		return false
	}
	if f.Keyword != "" {
		kw := strings.ToLower(f.Keyword)
		detailBlob := ""
		if e.Level >= LevelError {
			detailBlob = fmt.Sprint(e.Details)
		}
		blob := strings.ToLower(strings.Join([]string{
			e.Message,
			detailBlob,
			fmt.Sprint(e.AccountID),
			fmt.Sprint(e.DriverName),
		}, " "))
		if !strings.Contains(blob, kw) {
			return false
		}
	}
	return true
}

// scanLinesReverse 从文件末尾按行读取；visit 返回 false 时立即停止，不再扫描更早内容。
func scanLinesReverse(path string, visit func([]byte) bool) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}

	position := info.Size()
	var carry []byte
	for position > 0 {
		start := position - reverseReadBlockSize
		if start < 0 {
			start = 0
		}
		readSize := int(position - start)
		chunk := make([]byte, readSize)
		n, readErr := f.ReadAt(chunk, start)
		if readErr != nil && readErr != io.EOF {
			return false, readErr
		}
		chunk = append(chunk[:n], carry...)
		parts := bytes.Split(chunk, []byte{'\n'})
		firstComplete := 0
		if start > 0 {
			carry = append(carry[:0], parts[0]...)
			firstComplete = 1
		} else {
			carry = nil
		}
		for i := len(parts) - 1; i >= firstComplete; i-- {
			line := bytes.TrimSpace(parts[i])
			if len(line) == 0 {
				continue
			}
			if !visit(line) {
				return true, nil
			}
		}
		position = start
	}
	return false, nil
}

// StatsFiltered 统计全部落盘日志，并按最低级别过滤后台可见范围。
func (s *Storage) StatsFiltered(minLevel int, ackAfter string) Stats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.statsCache.valid &&
		s.statsCache.minLevel == minLevel &&
		s.statsCache.ackAfter == ackAfter &&
		time.Now().Before(s.statsCache.expiresAt) {
		return cloneStats(s.statsCache.value)
	}
	st := s.computeStatsFiltered(minLevel, ackAfter)
	s.statsCache = statsSnapshot{
		minLevel:  minLevel,
		ackAfter:  ackAfter,
		value:     cloneStats(st),
		expiresAt: time.Now().Add(statsCacheTTL),
		valid:     true,
	}
	return st
}

func (s *Storage) computeStatsFiltered(minLevel int, ackAfter string) Stats {
	files, err := s.listFiles()
	if err != nil {
		return Stats{ByLevel: map[string]int{}, ByModule: map[string]int{}}
	}
	st := Stats{
		ByLevel:  map[string]int{},
		ByModule: map[string]int{},
	}
	cutoff := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	st.LastAcknowledgedErrorAt = ackAfter
	for _, name := range files {
		path := filepath.Join(s.dir, name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var e Entry
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				continue
			}
			if e.Level < minLevel {
				continue
			}
			st.Total++
			st.ByLevel[LevelName(e.Level)]++
			st.ByModule[e.Module]++
			if e.Level >= LevelError {
				st.RecentErrorsTotal++
				if e.Timestamp >= cutoff {
					st.RecentErrors++
					if st.LastRecentErrorAt == "" || e.Timestamp > st.LastRecentErrorAt {
						st.LastRecentErrorAt = e.Timestamp
					}
					if ackAfter == "" || e.Timestamp > ackAfter {
						st.RecentUnacknowledgedErrors++
					}
				}
			}
		}
		_ = f.Close()
	}
	return st
}

func cloneStats(st Stats) Stats {
	clone := st
	clone.ByLevel = make(map[string]int, len(st.ByLevel))
	for key, value := range st.ByLevel {
		clone.ByLevel[key] = value
	}
	clone.ByModule = make(map[string]int, len(st.ByModule))
	for key, value := range st.ByModule {
		clone.ByModule[key] = value
	}
	return clone
}

func (s *Storage) invalidateStats() {
	s.statsMu.Lock()
	s.statsCache.valid = false
	s.statsMu.Unlock()
}

func (s *Storage) InvalidateStatsCache() {
	s.invalidateStats()
}

// CleanupOldLogs 删除早于保留期的按天日志文件；保留期按自然日计算，包含今天。
func (s *Storage) CleanupOldLogs(retentionDays int) (int, error) {
	defer s.invalidateStats()
	if retentionDays < 1 {
		retentionDays = 1
	}
	files, err := s.listFiles()
	if err != nil {
		return 0, err
	}
	threshold := time.Now().Local().Truncate(24*time.Hour).AddDate(0, 0, -(retentionDays - 1))
	deleted := 0
	for _, name := range files {
		datePart := strings.TrimSuffix(name, ".log")
		day, err := time.ParseInLocation("2006-01-02", datePart, time.Local)
		if err != nil {
			continue
		}
		if !day.Before(threshold) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// CleanupOutsideToday 删除今天之外的按天日志文件，保留当天日志。
func (s *Storage) CleanupOutsideToday() (int, error) {
	defer s.invalidateStats()
	files, err := s.listFiles()
	if err != nil {
		return 0, err
	}
	today := time.Now().Local().Format("2006-01-02")
	deleted := 0
	for _, name := range files {
		if strings.TrimSuffix(name, ".log") == today {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// ClearAllLogs 删除全部按天日志文件和 fallback 日志文件。
func (s *Storage) ClearAllLogs() (int, error) {
	defer s.invalidateStats()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, ent.Name())); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
