package upload

import (
	"context"
	"encoding/json"
	"time"
)

const broadcastCoalesceInterval = 120 * time.Millisecond

func (m *Manager) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	m.subMu.Lock()
	m.subs[ch] = struct{}{}
	m.subMu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(ch chan []byte) {
	m.subMu.Lock()
	delete(m.subs, ch)
	m.subMu.Unlock()
	close(ch)
}

func (m *Manager) SnapshotPayload() []byte {
	tasks := m.List(context.Background(), 0)
	payload, _ := json.Marshal(map[string]any{"kind": "snapshot", "tasks": tasks})
	return payload
}

// broadcast 标记发生变化的任务。没有传 taskID 时才退回全量同步，供极少数
// 无法定位具体任务的调用使用；正常进度更新不会再反复序列化全部历史任务。
func (m *Manager) broadcast(taskIDs ...string) {
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.subMu.Unlock()
		return
	}
	if len(taskIDs) == 0 {
		m.broadcastAllDirty = true
	} else {
		for _, taskID := range taskIDs {
			if taskID == "" {
				continue
			}
			delete(m.broadcastDeletedTaskIDs, taskID)
			m.broadcastDirtyTaskIDs[taskID] = struct{}{}
		}
	}
	if m.broadcastPending {
		m.subMu.Unlock()
		return
	}
	m.broadcastPending = true
	m.subMu.Unlock()
	m.flushBroadcast()
}

func (m *Manager) broadcastDeleted(taskIDs ...string) {
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.subMu.Unlock()
		return
	}
	for _, taskID := range taskIDs {
		if taskID == "" {
			continue
		}
		delete(m.broadcastDirtyTaskIDs, taskID)
		m.broadcastDeletedTaskIDs[taskID] = struct{}{}
	}
	if m.broadcastPending {
		m.subMu.Unlock()
		return
	}
	m.broadcastPending = true
	m.subMu.Unlock()
	m.flushBroadcast()
}

func (m *Manager) flushBroadcast() {
	m.subMu.Lock()
	full := m.broadcastAllDirty
	m.broadcastAllDirty = false
	dirty := takeIDSet(m.broadcastDirtyTaskIDs)
	deleted := takeIDSet(m.broadcastDeletedTaskIDs)
	m.subMu.Unlock()

	payload := m.deltaPayload(full, dirty, deleted)
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.broadcastPending = false
		m.subMu.Unlock()
		return
	}
	slowSubscribers := make([]chan []byte, 0)
	for ch := range m.subs {
		select {
		case ch <- payload:
		default:
			slowSubscribers = append(slowSubscribers, ch)
		}
	}
	m.subMu.Unlock()
	if len(slowSubscribers) > 0 {
		// 增量事件不能像旧的全量快照一样直接丢弃，否则被丢的任务
		// 可能永久停在旧状态。仅对积压的订阅者用最新完整快照托底。
		snapshot := m.SnapshotPayload()
		m.subMu.Lock()
		for _, ch := range slowSubscribers {
			if _, ok := m.subs[ch]; !ok {
				continue
			}
			select {
			case ch <- snapshot:
			default:
				// 丢弃最旧的一条待发事件，将能够自洽的快照放到队尾。
				select {
				case <-ch:
				default:
				}
				ch <- snapshot
			}
		}
		m.subMu.Unlock()
	}
	time.AfterFunc(broadcastCoalesceInterval, m.finishBroadcastWindow)
}

func takeIDSet(src map[string]struct{}) []string {
	ids := make([]string, 0, len(src))
	for id := range src {
		ids = append(ids, id)
		delete(src, id)
	}
	return ids
}

func (m *Manager) deltaPayload(full bool, dirty, deleted []string) []byte {
	if full {
		return m.SnapshotPayload()
	}
	tasks := make([]*Task, 0, len(dirty))
	for _, taskID := range dirty {
		if task, ok := m.Get(context.Background(), taskID); ok {
			tasks = append(tasks, task)
		} else {
			deleted = append(deleted, taskID)
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"kind":             "delta",
		"tasks":            tasks,
		"deleted_task_ids": deleted,
	})
	return payload
}

func (m *Manager) finishBroadcastWindow() {
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.broadcastPending = false
		m.broadcastAllDirty = false
		clear(m.broadcastDirtyTaskIDs)
		clear(m.broadcastDeletedTaskIDs)
		m.subMu.Unlock()
		return
	}
	dirty := m.broadcastAllDirty || len(m.broadcastDirtyTaskIDs) > 0 || len(m.broadcastDeletedTaskIDs) > 0
	if !dirty {
		m.broadcastPending = false
		m.subMu.Unlock()
		return
	}
	m.subMu.Unlock()
	m.flushBroadcast()
}
