package strm

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const scanFailureDetailSep = "\n---detail---\n"

type ScanFailureKind string

const (
	ScanFailureStrm     ScanFailureKind = "strm"
	ScanFailureMetadata ScanFailureKind = "metadata"
)

type ScanFailure struct {
	Kind   ScanFailureKind `json:"kind"`
	Path   string          `json:"path"`
	Reason string          `json:"reason"`
}

type FailureCollector struct {
	mu    sync.Mutex
	items []ScanFailure
	seen  map[string]struct{}
}

func NewFailureCollector() *FailureCollector {
	return &FailureCollector{}
}

func (c *FailureCollector) Add(kind ScanFailureKind, path, reason string) {
	if c == nil || path == "" {
		return
	}
	path = strings.TrimSpace(path)
	reason = strings.TrimSpace(reason)
	if path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	key := string(kind) + "\x00" + path + "\x00" + reason
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	c.items = append(c.items, ScanFailure{
		Kind:   kind,
		Path:   path,
		Reason: reason,
	})
}

func (c *FailureCollector) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *FailureCollector) Items() []ScanFailure {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ScanFailure, len(c.items))
	copy(out, c.items)
	return out
}

func EncodeScanFailureMessage(summary string, failures []ScanFailure) string {
	if len(failures) == 0 {
		return summary
	}
	raw, err := json.Marshal(failures)
	if err != nil {
		return summary
	}
	return summary + scanFailureDetailSep + string(raw)
}

func scanFailureSummary(taskName string, failures []ScanFailure) string {
	return fmt.Sprintf("STRM 任务「%s」扫描完成，共 %d 项未成功处理。", taskName, len(failures))
}
