package upload

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBroadcastCoalescesBurstUpdates(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-sse"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:   id,
			FileName: "demo.bin",
			Status:   StatusPending,
			Message:  "等待上传",
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	m.broadcast(id)
	first := waitPayload(t, ch, 100*time.Millisecond)
	if !strings.Contains(string(first), "等待上传") {
		t.Fatalf("first payload=%s", first)
	}

	m.mu.Lock()
	m.tasks[id].Message = "正在上传到网盘"
	m.mu.Unlock()
	m.broadcast(id)
	m.mu.Lock()
	m.tasks[id].Message = "上传已暂停"
	m.mu.Unlock()
	m.broadcast(id)

	select {
	case payload := <-ch:
		t.Fatalf("unexpected immediate payload: %s", payload)
	case <-time.After(50 * time.Millisecond):
	}

	second := waitPayload(t, ch, 250*time.Millisecond)
	text := string(second)
	if !strings.Contains(text, "上传已暂停") {
		t.Fatalf("second payload=%s", text)
	}
	if strings.Contains(text, "正在上传到网盘") {
		t.Fatalf("coalesced payload should keep latest state only: %s", text)
	}

	select {
	case payload := <-ch:
		t.Fatalf("unexpected extra payload: %s", payload)
	case <-time.After(180 * time.Millisecond):
	}
}

func TestBroadcastOnlyContainsChangedTasks(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	m.mu.Lock()
	for i := 0; i < 10_000; i++ {
		id := "task-" + string(rune(i+1000))
		m.tasks[id] = &taskState{Task: Task{TaskID: id, FileName: "demo.bin", Status: StatusPending}}
	}
	changedID := "changed"
	m.tasks[changedID] = &taskState{Task: Task{TaskID: changedID, FileName: "changed.bin", Status: StatusRunning}}
	m.mu.Unlock()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	m.broadcast(changedID)
	payload := waitPayload(t, ch, 100*time.Millisecond)
	var event struct {
		Kind  string `json:"kind"`
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "delta" || len(event.Tasks) != 1 || event.Tasks[0].TaskID != changedID {
		t.Fatalf("unexpected event: kind=%q tasks=%d", event.Kind, len(event.Tasks))
	}
	if len(payload) > 2048 {
		t.Fatalf("delta payload too large: %d", len(payload))
	}
}

func TestBroadcastFallsBackToSnapshotForSlowSubscriber(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-slow-subscriber"
	m.mu.Lock()
	m.tasks[id] = &taskState{Task: Task{TaskID: id, FileName: "demo.bin", Status: StatusRunning}}
	m.mu.Unlock()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)
	for i := 0; i < cap(ch); i++ {
		ch <- []byte(`{"kind":"delta"}`)
	}
	m.broadcast(id)

	foundSnapshot := false
	for i := 0; i < cap(ch); i++ {
		payload := waitPayload(t, ch, 100*time.Millisecond)
		var event struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "snapshot" {
			foundSnapshot = true
		}
	}
	if !foundSnapshot {
		t.Fatal("slow subscriber did not receive a recovery snapshot")
	}
}

func waitPayload(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(timeout):
		t.Fatalf("wait payload timeout: %s", timeout)
		return nil
	}
}
