package upload

import (
	"context"
	"strconv"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/pkg/speedsmoother"
)

func TestCalcProgress(t *testing.T) {
	tests := []struct {
		uploaded, total int64
		want            int
	}{
		{0, 100, 0},
		{50, 100, 50},
		{99, 100, 99},
		{100, 100, 100},
		{200, 100, 100},
		{999, 1000, 99},
	}
	for _, tc := range tests {
		if got := calcProgress(tc.uploaded, tc.total); got != tc.want {
			t.Fatalf("calcProgress(%d, %d) = %d, want %d", tc.uploaded, tc.total, got, tc.want)
		}
	}
}

func TestUploadEntryName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"file.txt", "file.txt"},
		{"MyFolder/file.txt", "file.txt"},
		{"MyFolder/sub/file.txt", "file.txt"},
		{`MyFolder\sub\file.txt`, "file.txt"},
		{"  spaced/name.txt  ", "name.txt"},
	}
	for _, tc := range tests {
		if got := uploadEntryName(tc.in); got != tc.want {
			t.Fatalf("uploadEntryName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUpdateProgressMonotonic(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-progress"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:        id,
			UploadedBytes: 5000,
			TotalBytes:    10000,
			Progress:      50,
			Status:        StatusRunning,
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	m.updateProgress(id, 0, 10000, "正在计算文件校验值")

	got, _ := m.Get(context.Background(), id)
	if got.UploadedBytes != 5000 || got.Progress != 50 {
		t.Fatalf("progress regressed: uploaded=%d progress=%d", got.UploadedBytes, got.Progress)
	}
}

func TestShouldEmitProgressByTime(t *testing.T) {
	base := time.Now()
	st := &taskState{
		lastEmit:     base.Add(-300 * time.Millisecond),
		lastProgress: 10,
		lastMessage:  "uploading",
		Task:         Task{UploadedBytes: 1000},
	}
	if !shouldEmitProgress(st, 10, 2000, 10000, "uploading", base) {
		t.Fatal("expected emit when uploaded bytes increased after progress interval")
	}
	st.lastEmit = base.Add(-50 * time.Millisecond)
	if shouldEmitProgress(st, 10, 2000, 10000, "uploading", base) {
		t.Fatal("expected skip when inside progress interval")
	}
}

func TestTranslateError(t *testing.T) {
	if got := translateError("Server disconnected without response"); got != "服务器连接已断开" {
		t.Fatalf("got %q", got)
	}
	if got := translateError("custom"); got != "custom" {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateProgressUsesSpeedSmoother(t *testing.T) {
	now := time.Now()
	st := &taskState{
		lastMessage: "正在上传到115网盘，分片（1/1）",
	}
	var speed float64
	for i := 1; i <= 4; i++ {
		sample := st.speed.Sample(int64(i<<20), now, speedsmoother.PhaseKey(st.lastMessage))
		speed = sample.Display
		now = now.Add(300 * time.Millisecond)
	}
	if speed <= 0 {
		t.Fatal("expected smoothed speed")
	}
	if speed > float64(4<<20)/0.3 {
		t.Fatalf("speed too high: %.0f", speed)
	}
}

func TestUpdateProgressHighFrequencySpeedBounded(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-hf"
	const total = 128 << 20
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:     id,
			TotalBytes: total,
			Status:     StatusRunning,
		},
		lastMessage: "正在上传到115网盘，分片（1/1）",
		lastEmit:    time.Now(),
		runDone:     make(chan struct{}),
	}
	m.mu.Unlock()

	start := time.Now()
	step := int64(32 << 10)
	for uploaded := step; uploaded <= 32<<20; uploaded += step {
		m.updateProgress(id, uploaded, total, "正在上传到115网盘，分片（1/1）")
	}
	time.Sleep(300 * time.Millisecond)
	m.updateProgress(id, 32<<20+step, total, "正在上传到115网盘，分片（1/1）")
	elapsed := time.Since(start)
	got, _ := m.Get(context.Background(), id)
	if got.SpeedBytesPerSecond <= 0 {
		t.Fatal("expected speed to be calculated")
	}
	maxReasonable := float64(32<<20) / elapsed.Seconds() * 2
	if got.SpeedBytesPerSecond > maxReasonable {
		t.Fatalf("speed %.0f B/s exceeds 2x wall rate %.0f", got.SpeedBytesPerSecond, maxReasonable)
	}
}

func TestUpdateProgressSlicePhaseDoesNotSpike(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-slice"
	const total = 30 * (4 << 20)
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:     id,
			TotalBytes: total,
			Status:     StatusRunning,
		},
		lastEmit: time.Now(),
		runDone:  make(chan struct{}),
	}
	m.mu.Unlock()

	var last float64
	for i := 1; i <= 10; i++ {
		uploaded := int64(i) * (4 << 20)
		msg := "正在上传到123网盘，分片（" + strconv.Itoa(i) + "/30）"
		m.updateProgress(id, uploaded, total, msg)
		got, _ := m.Get(context.Background(), id)
		if got.SpeedBytesPerSecond > 0 {
			if last > 0 && got.SpeedBytesPerSecond > last*3 {
				t.Fatalf("slice speed spike at %d: %.0f -> %.0f", i, last, got.SpeedBytesPerSecond)
			}
			last = got.SpeedBytesPerSecond
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUpdateDownloadProgressDebouncesPersist(t *testing.T) {
	repo := &failingUploadTaskRepo{rows: make(map[string]*domain.UploadTaskRecord)}
	m := NewManager(Options{Repo: repo, DataDir: t.TempDir()})
	const id = "task-download"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:     id,
			Status:     StatusRunning,
			Phase:      PhaseDownloading,
			TotalBytes: 1000,
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	m.updateDownloadProgress(id, 100, 1000, "正在从源盘下载", 10)
	m.updateDownloadProgress(id, 200, 1000, "正在从源盘下载", 20)

	repo.mu.Lock()
	if repo.upserts != 1 {
		repo.mu.Unlock()
		t.Fatalf("upserts=%d, want 1", repo.upserts)
	}
	if got := repo.rows[id]; got == nil || got.DownloadedBytes != 100 {
		repo.mu.Unlock()
		t.Fatalf("persisted downloaded=%v, want 100", got)
	}
	repo.mu.Unlock()

	got, ok := m.Get(context.Background(), id)
	if !ok {
		t.Fatal("task not found")
	}
	if got.DownloadedBytes != 200 || got.Progress != 20 {
		t.Fatalf("memory progress=%d downloaded=%d", got.Progress, got.DownloadedBytes)
	}

	m.mu.Lock()
	m.tasks[id].lastEmit = time.Now().Add(-downloadPersistInterval)
	m.mu.Unlock()
	m.updateDownloadProgress(id, 300, 1000, "正在从源盘下载", 30)

	repo.mu.Lock()
	if repo.upserts != 2 {
		repo.mu.Unlock()
		t.Fatalf("upserts=%d, want 2 after debounce window", repo.upserts)
	}
	if got := repo.rows[id]; got == nil || got.DownloadedBytes != 300 {
		repo.mu.Unlock()
		t.Fatalf("persisted downloaded=%v, want 300", got)
	}
	repo.mu.Unlock()

	m.updateDownloadProgress(id, 400, 1000, "正在从源盘下载", 40)
	m.finishCrossTransferDownloadError(context.Background(), id, "boom")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.upserts != 3 {
		t.Fatalf("upserts=%d, want 3 after final state persist", repo.upserts)
	}
	rec := repo.rows[id]
	if rec == nil {
		t.Fatal("persisted record missing")
	}
	if rec.Status != StatusFailed {
		t.Fatalf("status=%q want failed", rec.Status)
	}
	if rec.DownloadedBytes != 400 {
		t.Fatalf("downloaded=%d want 400", rec.DownloadedBytes)
	}
}
