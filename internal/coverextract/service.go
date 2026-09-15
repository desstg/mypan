package coverextract

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"litepan/internal/domain"
	"litepan/internal/driver"
	filecore "litepan/internal/file"
	"litepan/internal/mediaorganize/rules"
	"litepan/internal/playback"
)

const (
	maxFiles      = 20
	maxFrames     = 50
	maxImageBytes = int64(200 << 20)
	// defaultReadMax 是单次提取读取量的基础上限；newSource 会按文件大小
	// 提升为 max(256MB, 文件大小)，避免深位置取帧（MKV 无 Cues）被截断。
	defaultReadMax = int64(256 << 20)
	// MaxPosterBytes 限制前端 Canvas 合成后上传的海报大小。
	MaxPosterBytes = int64(8 << 20)
)

var supportedExt = map[string]struct{}{`.mp4`: {}, `.mkv`: {}, `.mov`: {}, `.webm`: {}}

type Options struct {
	DataDir    string
	ListenAddr string
	Files      *filecore.Service
	Playback   *playback.Service
	Log        *slog.Logger
}

type Service struct {
	mu         sync.Mutex
	files      map[string]*SessionFile
	frames     map[string]*imageEntry
	tokens     map[string]*sourceToken
	imageLen   int64
	sem        chan struct{}
	downloadMu sync.Mutex
	opts       Options
	log        *slog.Logger
}

type SessionFile struct {
	ID             string  `json:"id"`
	AccountID      int64   `json:"account_id"`
	FileID         string  `json:"file_id"`
	ParentID       string  `json:"parent_id"`
	TargetParentID string  `json:"target_parent_id"`
	TargetPath     string  `json:"target_path"`
	Name           string  `json:"name"`
	Size           int64   `json:"size"`
	Status         string  `json:"status"`
	Error          string  `json:"error,omitempty"`
	DurationMS     int64   `json:"duration_ms,omitempty"`
	Frames         []Frame `json:"frames"`
	TouchedAt      int64   `json:"touched_at"`
}

type DirectoryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Frame struct {
	ID     string `json:"id"`
	TimeMS int64  `json:"time_ms"`
}

type imageEntry struct {
	Data      []byte
	CreatedAt time.Time
}

type sourceToken struct {
	AccountID int64
	FileID    string
	ExpiresAt time.Time
	Read      int64
	MaxRead   int64
}

type ExtractRequest struct {
	SessionFileID string `json:"session_file_id"`
	Mode          string `json:"mode"`
	TimestampMS   int64  `json:"timestamp_ms"`
	// Approx：逐帧候选提取标记（非精确）。候选图允许落在最近关键帧，
	// 优先关键帧极速路径、失败快速放弃，避免限速网盘上拖长等待。
	Approx bool `json:"approx"`
}

type SaveRequest struct {
	SessionFileID string `json:"session_file_id"`
	FrameID       string `json:"frame_id"`
	Overwrite     bool   `json:"overwrite"`
}

type SaveResult struct {
	OK       bool   `json:"ok"`
	Conflict bool   `json:"conflict,omitempty"`
	Filename string `json:"filename"`
}

func New(opts Options) (*Service, error) {
	if opts.Files == nil || opts.Playback == nil || strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("视频海报生成服务配置不完整")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{files: map[string]*SessionFile{}, frames: map[string]*imageEntry{}, tokens: map[string]*sourceToken{}, sem: make(chan struct{}, 1), opts: opts, log: log}, nil
}

func (s *Service) Add(ctx context.Context, accountID int64, fileID, parentID string, directoryChain []DirectoryRef) (*SessionFile, error) {
	item, err := s.opts.Files.Info(ctx, accountID, fileID)
	if err != nil {
		return nil, err
	}
	if item.IsDir {
		return nil, domain.Errorf(domain.CodeValidation, "目录不能提取封面")
	}
	if !IsSupported(item.Name) {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持 MP4、MKV、MOV 和 WebM 视频")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	for _, existing := range s.files {
		if existing.AccountID == accountID && existing.FileID == fileID {
			existing.TouchedAt = time.Now().Unix()
			return cloneFile(existing), nil
		}
	}
	if len(s.files) >= maxFiles {
		return nil, domain.Errorf(domain.CodeValidation, "视频海报生成列表最多保留 %d 个视频", maxFiles)
	}
	targetParentID, targetPath := defaultTarget(parentID, directoryChain)
	f := &SessionFile{ID: uuid.NewString(), AccountID: accountID, FileID: fileID, ParentID: parentID, TargetParentID: targetParentID, TargetPath: targetPath, Name: item.Name, Size: item.Size, Status: "queued", Frames: []Frame{}, TouchedAt: time.Now().Unix()}
	s.files[f.ID] = f
	return cloneFile(f), nil
}

func defaultTarget(parentID string, chain []DirectoryRef) (string, string) {
	usable := make([]DirectoryRef, 0, len(chain))
	rootID := ""
	for _, dir := range chain {
		name := strings.TrimSpace(dir.Name)
		if name == "根目录" {
			rootID = dir.ID
			continue
		}
		if name == "" {
			continue
		}
		usable = append(usable, DirectoryRef{ID: dir.ID, Name: name})
	}
	if len(usable) == 0 {
		return parentID, "/"
	}
	target := len(usable) - 1
	if rules.IsSeasonDirName(usable[target].Name) {
		target--
	}
	if target < 0 {
		return rootID, "/"
	}
	names := make([]string, 0, target+1)
	for _, dir := range usable[:target+1] {
		names = append(names, dir.Name)
	}
	return usable[target].ID, "/" + strings.Join(names, "/")
}

func (s *Service) SetTarget(id, parentID, path string) (*SessionFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[id]
	if f == nil {
		return nil, domain.Errorf(domain.CodeNotFound, "视频不在视频海报生成列表中")
	}
	f.TargetParentID = parentID
	f.TargetPath = path
	f.TouchedAt = time.Now().Unix()
	return cloneFile(f), nil
}

func IsSupported(name string) bool {
	_, ok := supportedExt[strings.ToLower(filepath.Ext(name))]
	return ok
}

func (s *Service) List() []*SessionFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	out := make([]*SessionFile, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, cloneFile(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TouchedAt > out[j].TouchedAt })
	return out
}

func (s *Service) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.files[id]; f != nil {
		for _, frame := range f.Frames {
			s.removeFrameLocked(frame.ID)
		}
		delete(s.files, id)
	}
}

func (s *Service) RemoveFrame(sessionID, frameID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[sessionID]
	if f == nil {
		return domain.Errorf(domain.CodeNotFound, "视频不在视频海报生成列表中")
	}
	kept := f.Frames[:0]
	removed := false
	for _, fr := range f.Frames {
		if fr.ID == frameID {
			s.removeFrameLocked(fr.ID)
			removed = true
			continue
		}
		kept = append(kept, fr)
	}
	if !removed {
		return domain.Errorf(domain.CodeNotFound, "候选画面不存在")
	}
	f.Frames = kept
	f.TouchedAt = time.Now().Unix()
	return nil
}
func (s *Service) Clear() {
	_, _, _ = s.ClearWithStats()
}

// Stats 返回当前内存会话占用（文件数、候选帧数、图片字节数），供清理工具只读统计。
func (s *Service) Stats() (files, frames int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.files), len(s.frames), s.imageLen
}

// ClearWithStats 清空全部会话并返回释放统计（待处理文件、候选帧、图片字节），供清理工具使用。
func (s *Service) ClearWithStats() (files, frames int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files = len(s.files)
	frames = len(s.frames)
	bytes = s.imageLen
	s.files = map[string]*SessionFile{}
	s.frames = map[string]*imageEntry{}
	s.tokens = map[string]*sourceToken{}
	s.imageLen = 0
	return files, frames, bytes
}

func (s *Service) Runtime() map[string]any {
	ffmpeg, ferr := findTool(s.opts.DataDir, "ffmpeg")
	return map[string]any{"ready": ferr == nil, "ffmpeg": ffmpeg, "error": joinToolErrors(ferr), "auto_download_available": supportedDownloadAsset() != nil, "manual_path": filepath.Join(s.opts.DataDir, "tools")}
}

func (s *Service) Extract(ctx context.Context, req ExtractRequest) (*SessionFile, error) {
	s.mu.Lock()
	f := s.files[req.SessionFileID]
	if f == nil {
		s.mu.Unlock()
		return nil, domain.Errorf(domain.CodeNotFound, "视频不在视频海报生成列表中")
	}
	f.Status = "extracting"
	f.Error = ""
	local := *f
	s.mu.Unlock()
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ffmpeg, err := findTool(s.opts.DataDir, "ffmpeg")
	if err != nil {
		return s.fail(req.SessionFileID, err)
	}
	// 读取上限按文件大小动态：深位置取帧（MKV 无 Cues）需读大量字节，
	// 上限取 max(256MB, 文件大小)，既够大文件取帧，又不会给提取无脑放权。
	token, sourceURL, err := s.newSource(local.AccountID, local.FileID, local.Size)
	if err != nil {
		return s.fail(req.SessionFileID, err)
	}
	defer s.dropToken(token)
	// probe 模式只探测并缓存时长，不提取候选画面。
	if req.Mode == "probe" {
		duration := local.DurationMS
		if duration <= 0 {
			duration, err = probeDuration(ctx, ffmpeg, sourceURL)
			if err != nil {
				return s.fail(req.SessionFileID, err)
			}
			s.mu.Lock()
			if ff := s.files[req.SessionFileID]; ff != nil {
				ff.DurationMS = duration
			}
			s.mu.Unlock()
		}
		out := cloneFile(&local)
		out.DurationMS = duration
		out.Status = "done"
		return out, nil
	}
	// 同一待处理视频复用已缓存时长，后续批量或指定时间取帧不再重复探测。
	duration := local.DurationMS
	if duration <= 0 {
		duration, err = probeDuration(ctx, ffmpeg, sourceURL)
		if err != nil {
			return s.fail(req.SessionFileID, err)
		}
	}
	times, err := extractionTimes(req, duration)
	if err != nil {
		return s.fail(req.SessionFileID, err)
	}
	// 精确模式仅限用户指定时间的单帧提取（timestamp 且未标记 Approx）；
	// 候选图（head/random/逐帧 approx）走关键帧极速路径。
	exact := req.Mode == "timestamp" && !req.Approx
	started := time.Now()
	results := extractFrames(ctx, ffmpeg, sourceURL, times, duration, exact)
	succeeded := 0
	var lastExtractErr error
	for i, result := range results {
		if result.Err != nil {
			lastExtractErr = result.Err
			continue
		}
		if len(result.Data) == 0 {
			continue
		}
		s.addFrame(req.SessionFileID, times[i], result.Data)
		succeeded++
	}
	s.log.Debug("视频海报取帧完成",
		"account_id", local.AccountID,
		"file_id", local.FileID,
		"mode", req.Mode,
		"requested", len(times),
		"succeeded", succeeded,
		"elapsed_ms", time.Since(started).Milliseconds(),
	)
	if succeeded == 0 {
		if lastExtractErr == nil {
			lastExtractErr = errors.New("FFmpeg 未返回可用画面")
		}
		return s.fail(req.SessionFileID, fmt.Errorf("未能提取可用画面: %w", lastExtractErr))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f = s.files[req.SessionFileID]
	if f == nil {
		return nil, domain.Errorf(domain.CodeNotFound, "会话已清理")
	}
	f.DurationMS = duration
	f.TouchedAt = time.Now().Unix()
	if len(f.Frames) == 0 {
		f.Status = "failed"
		if lastExtractErr != nil {
			f.Error = "未能提取可用画面: " + lastExtractErr.Error()
		} else {
			f.Error = "未能提取可用画面"
		}
	} else {
		f.Status = "done"
	}
	return cloneFile(f), nil
}

func (s *Service) Image(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	v, ok := s.frames[id]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v.Data...), true
}

func (s *Service) Save(ctx context.Context, req SaveRequest) (SaveResult, error) {
	s.mu.Lock()
	f := s.files[req.SessionFileID]
	img := s.frames[req.FrameID]
	if f == nil || img == nil || !frameBelongsToFile(f, req.FrameID) {
		s.mu.Unlock()
		return SaveResult{}, domain.Errorf(domain.CodeNotFound, "文件或候选图已失效")
	}
	local, data := *f, append([]byte(nil), img.Data...)
	s.mu.Unlock()
	return s.saveData(ctx, local, data, req.Overwrite)
}

// SaveComposed 保存 Canvas 生成的最终海报；候选帧校验会话归属，防客户端绕过取帧流程写入任意目录。
func (s *Service) SaveComposed(ctx context.Context, req SaveRequest, data []byte) (SaveResult, error) {
	if len(data) == 0 || int64(len(data)) > MaxPosterBytes {
		return SaveResult{}, domain.Errorf(domain.CodeValidation, "合成海报大小无效")
	}
	s.mu.Lock()
	f := s.files[req.SessionFileID]
	img := s.frames[req.FrameID]
	if f == nil || img == nil || !frameBelongsToFile(f, req.FrameID) {
		s.mu.Unlock()
		return SaveResult{}, domain.Errorf(domain.CodeNotFound, "文件或候选图已失效")
	}
	local := *f
	s.mu.Unlock()
	return s.saveData(ctx, local, append([]byte(nil), data...), req.Overwrite)
}

func frameBelongsToFile(file *SessionFile, frameID string) bool {
	if file == nil || frameID == "" {
		return false
	}
	for _, frame := range file.Frames {
		if frame.ID == frameID {
			return true
		}
	}
	return false
}

func (s *Service) saveData(ctx context.Context, local SessionFile, data []byte, overwrite bool) (SaveResult, error) {
	items, err := s.opts.Files.List(ctx, local.AccountID, local.TargetParentID, true)
	if err != nil {
		return SaveResult{}, err
	}
	filename := "poster.jpg"
	for _, item := range items {
		if strings.EqualFold(item.Name, filename) && !overwrite {
			return SaveResult{Conflict: true, Filename: filename}, nil
		}
	}
	dir := filepath.Join(s.opts.DataDir, "coverextract")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return SaveResult{}, err
	}
	tmp, err := os.CreateTemp(dir, "cover-*.jpg")
	if err != nil {
		return SaveResult{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return SaveResult{}, err
	}
	policy := "fail"
	if overwrite {
		policy = "overwrite"
	}
	_, err = s.opts.Files.UploadLocal(ctx, local.AccountID, driver.LocalUploadRequest{LocalPath: tmpPath, FileName: filename, ParentID: local.TargetParentID, ConflictPolicy: policy})
	if err != nil {
		return SaveResult{}, err
	}
	return SaveResult{OK: true, Filename: filename}, nil
}

func (s *Service) ServeSource(w http.ResponseWriter, r *http.Request, token string) error {
	if !isLoopback(r.RemoteAddr) {
		return domain.Errorf(domain.CodePermissionDenied, "临时媒体入口仅限本机")
	}
	s.mu.Lock()
	st := s.tokens[token]
	if st == nil || time.Now().After(st.ExpiresAt) {
		delete(s.tokens, token)
		s.mu.Unlock()
		return domain.Errorf(domain.CodePermissionDenied, "临时媒体凭证已失效")
	}
	accountID, fileID := st.AccountID, st.FileID
	s.mu.Unlock()
	cw := &countingWriter{ResponseWriter: w, add: func(n int64) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur := s.tokens[token]
		if cur == nil {
			return false
		}
		cur.Read += n
		return cur.Read <= cur.MaxRead
	}}
	// 取帧固定读取原文件并由本机代理，避免转码流影响定位。
	return s.opts.Playback.ServeHTTP(cw, r, playback.Request{AccountID: accountID, FileID: fileID}, playback.Intent{
		FileName:       "source",
		OriginalFile:   true,
		ForceProxy:     true,
		SkipRangeLimit: true,
	})
}

type countingWriter struct {
	http.ResponseWriter
	add func(int64) bool
}

func (w *countingWriter) Write(p []byte) (int, error) {
	if !w.add(int64(len(p))) {
		return 0, domain.Errorf(domain.CodeValidation, "取帧读取量超过安全上限")
	}
	return w.ResponseWriter.Write(p)
}

func (s *Service) newSource(accountID int64, fileID string, fileSize int64) (string, string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(b)
	maxRead := defaultReadMax
	if fileSize > maxRead {
		maxRead = fileSize
	}
	s.mu.Lock()
	s.tokens[token] = &sourceToken{AccountID: accountID, FileID: fileID, ExpiresAt: time.Now().Add(10 * time.Minute), MaxRead: maxRead}
	s.mu.Unlock()
	return token, "http://127.0.0.1" + normalizeListen(s.opts.ListenAddr) + "/api/internal/cover-source/" + token, nil
}
func normalizeListen(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ":5211"
	}
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if _, p, ok := strings.Cut(addr, ":"); ok {
		return ":" + p
	}
	return ":5211"
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
func (s *Service) dropToken(token string) { s.mu.Lock(); delete(s.tokens, token); s.mu.Unlock() }

func extractionTimes(req ExtractRequest, duration int64) ([]int64, error) {
	if duration <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "无法读取视频时长")
	}
	switch req.Mode {
	case "timestamp":
		if req.TimestampMS < 0 || req.TimestampMS >= duration {
			return nil, domain.Errorf(domain.CodeValidation, "时间点必须在视频时长内")
		}
		return []int64{req.TimestampMS}, nil
	case "head":
		return []int64{min(int64(1000), duration-1)}, nil
	case "random", "":
		return randomExtractionTimes(duration, 3)
	default:
		return nil, domain.Errorf(domain.CodeValidation, "不支持的取帧方式")
	}
}

// randomExtractionTimes 避开容易出现片头、片尾字幕的两端，并在三个区段各随机取一帧。
// 这样每次点击都有变化，同时避免纯随机导致三张候选图挤在同一小段。
func randomExtractionTimes(duration int64, count int) ([]int64, error) {
	if duration <= 0 || count <= 0 {
		return nil, domain.Errorf(domain.CodeValidation, "无法计算随机取帧时间")
	}
	start := duration / 10
	span := duration * 8 / 10
	if span < int64(count) {
		start = 0
		span = duration
	}
	out := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		left := start + span*int64(i)/int64(count)
		right := start + span*int64(i+1)/int64(count)
		width := right - left
		offset := int64(0)
		if width > 1 {
			n, err := rand.Int(rand.Reader, big.NewInt(width))
			if err != nil {
				return nil, fmt.Errorf("生成随机取帧时间失败: %w", err)
			}
			offset = n.Int64()
		}
		ts := min(left+offset, duration-1)
		if len(out) == 0 || ts != out[len(out)-1] {
			out = append(out, ts)
		}
	}
	return out, nil
}

var durationPattern = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

func probeDuration(parent context.Context, bin, url string) (int64, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "-hide_banner", "-i", url).CombinedOutput()
	match := durationPattern.FindSubmatch(out)
	if len(match) != 4 {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 800 {
			detail = detail[len(detail)-800:]
		}
		return 0, fmt.Errorf("FFmpeg 未返回有效时长: %s", detail)
	}
	hours, _ := strconv.ParseInt(string(match[1]), 10, 64)
	minutes, _ := strconv.ParseInt(string(match[2]), 10, 64)
	seconds, err := strconv.ParseFloat(string(match[3]), 64)
	if err != nil {
		return 0, errors.New("FFmpeg 时长格式无效")
	}
	return int64((float64(hours*3600+minutes*60) + seconds) * 1000), nil
}

type frameResult struct {
	Data []byte
	Err  error
}

// extractFrames 串行读取同一网盘文件，避免多个 FFmpeg 进程争抢 Range 连接或代理通道。
// 每帧独立返回结果，单帧失败不会丢弃已成功的候选图。
func extractFrames(parent context.Context, bin, url string, times []int64, duration int64, exact bool) []frameResult {
	if len(times) == 0 {
		return nil
	}
	results := make([]frameResult, len(times))
	for i, ts := range times {
		if err := parent.Err(); err != nil {
			results[i].Err = err
			continue
		}
		results[i].Data, results[i].Err = extractOneFrame(parent, bin, url, ts, duration, exact)
	}
	return results
}

// extractOneFrame 先尝试关键帧快速路径，失败时再用普通准确定位回退。
// 指定时间模式优先准确定位，避免用户选定的时间偏移到较早的关键帧。
type frameAttempt struct {
	TimeMS       int64
	KeyframeOnly bool
}

func frameAttempts(ts, duration int64, exact bool) []frameAttempt {
	if exact {
		return []frameAttempt{{TimeMS: ts}}
	}
	// 非精确：全部走关键帧快速路径（含附近位置），失败即放弃该帧。
	// 海报生成固定用夸克驱动本体读取，关键帧极速在原始文件上稳定；
	// 限速网盘上精确 seek 可能拖到 30s 仍失败，快速放弃让前端尽快返回已成功的帧。
	attempts := []frameAttempt{{TimeMS: ts, KeyframeOnly: true}}
	seen := map[int64]struct{}{ts: {}}
	for _, offset := range []int64{2000, -2000} {
		nearby := ts + offset
		if nearby < 0 {
			nearby = 0
		}
		if duration > 0 && nearby >= duration {
			nearby = duration - 1
		}
		if nearby < 0 {
			continue
		}
		if _, ok := seen[nearby]; ok {
			continue
		}
		seen[nearby] = struct{}{}
		attempts = append(attempts, frameAttempt{TimeMS: nearby, KeyframeOnly: true})
	}
	return attempts
}

func extractOneFrame(parent context.Context, bin, url string, ts, duration int64, exact bool) ([]byte, error) {
	var errs []string
	for _, attempt := range frameAttempts(ts, duration, exact) {
		timeout := 15 * time.Second
		if !attempt.KeyframeOnly {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		data, err := runFrameCommand(ctx, bin, url, attempt.TimeMS, attempt.KeyframeOnly)
		cancel()
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
		if parent.Err() != nil {
			return nil, parent.Err()
		}
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func runFrameCommand(ctx context.Context, bin, url string, ts int64, keyframeOnly bool) ([]byte, error) {
	stamp := fmt.Sprintf("%.3f", float64(ts)/1000)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-ss", stamp}
	if keyframeOnly {
		args = append(args, "-noaccurate_seek", "-skip_frame", "nokey")
	}
	args = append(args,
		"-i", url,
		"-map", "0:V:0",
		"-an", "-sn", "-dn",
		"-frames:v", "1",
		"-vf", "scale=w='min(1280,iw)':h='min(1280,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-q:v", "3",
		"-f", "image2pipe", "-c:v", "mjpeg", "pipe:1",
	)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		strategy := "普通"
		if keyframeOnly {
			strategy = "关键帧"
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = detail[len(detail)-500:]
		}
		return nil, fmt.Errorf("%s取帧失败(%s): %s", strategy, stamp, detail)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("FFmpeg 未返回画面(%s)", stamp)
	}
	return stdout.Bytes(), nil
}

func findTool(dataDir, name string) (string, error) {
	local := filepath.Join(dataDir, "tools", name)
	if st, err := os.Stat(local); err == nil && !st.IsDir() {
		return local, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("未找到 %s，请放入 %s", name, local)
	}
	return p, nil
}
func joinToolErrors(errs ...error) string {
	var parts []string
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	return strings.Join(parts, "；")
}

type downloadAsset struct{ Name, SHA256 string }

func supportedDownloadAsset() *downloadAsset {
	assets := map[string]downloadAsset{
		"linux/amd64":  {Name: "ffmpeg-linux-x64.gz", SHA256: "bfe8a8fc511530457b528c48d77b5737527b504a3797a9bc4866aeca69c2dffa"},
		"linux/arm64":  {Name: "ffmpeg-linux-arm64.gz", SHA256: "754a678672298bc68156adff58aa7385a592c2b30b1d0ae8750c45c915c4bac0"},
		"darwin/amd64": {Name: "ffmpeg-darwin-x64.gz", SHA256: "929b375c1182d956c51f7ac25e0b2b0411fb01f6f407aa15c9758efeb4242106"},
		"darwin/arm64": {Name: "ffmpeg-darwin-arm64.gz", SHA256: "8923876afa8db5585022d7860ec7e589af192f441c56793971276d450ed3bbfa"},
	}
	v, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return nil
	}
	return &v
}

func (s *Service) DownloadFFmpeg(ctx context.Context) error {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	asset := supportedDownloadAsset()
	if asset == nil {
		return domain.Errorf(domain.CodeNotImplement, "当前平台不支持自动安装 FFmpeg")
	}
	if _, err := findTool(s.opts.DataDir, "ffmpeg"); err == nil {
		return nil
	}
	urls := []string{
		"https://gitcode.com/gh_mirrors/ff/ffmpeg-static/releases/download/b6.1.1/" + asset.Name,
		"https://github.com/eugeneware/ffmpeg-static/releases/download/b6.1.1/" + asset.Name,
	}
	var last error
	for _, url := range urls {
		if err := s.downloadOne(ctx, url, *asset); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return fmt.Errorf("FFmpeg 下载失败: %w", last)
}

func (s *Service) downloadOne(ctx context.Context, url string, asset downloadAsset) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载源返回 HTTP %d", resp.StatusCode)
	}
	dir := filepath.Join(s.opts.DataDir, "tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	archive, err := os.CreateTemp(dir, "ffmpeg-*.gz")
	if err != nil {
		return err
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(archive, h), io.LimitReader(resp.Body, 128<<20))
	closeErr := archive.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n < 10<<20 {
		return errors.New("下载内容过小，可能不是 FFmpeg 资产")
	}
	if hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return errors.New("FFmpeg SHA-256 校验失败")
	}
	in, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	tmp, err := os.CreateTemp(dir, "ffmpeg-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = io.Copy(tmp, io.LimitReader(gz, 256<<20)); err == nil {
		err = tmp.Sync()
	}
	closeErr = tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	smokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, smokeErr := exec.CommandContext(smokeCtx, tmpPath, "-version").CombinedOutput()
	if smokeErr != nil || !strings.Contains(string(out), "ffmpeg version") {
		return errors.New("FFmpeg 冒烟检查失败")
	}
	return os.Rename(tmpPath, filepath.Join(dir, "ffmpeg"))
}
func (s *Service) fail(id string, err error) (*SessionFile, error) {
	s.mu.Lock()
	if f := s.files[id]; f != nil {
		f.Status = "failed"
		f.Error = err.Error()
		f.TouchedAt = time.Now().Unix()
	}
	s.mu.Unlock()
	return nil, err
}
func (s *Service) addFrame(fileID string, ts int64, data []byte) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[fileID]
	if f == nil {
		return
	}
	for _, fr := range f.Frames {
		if img := s.frames[fr.ID]; img != nil {
			old := sha256.Sum256(img.Data)
			if hex.EncodeToString(old[:]) == digest {
				return
			}
		}
	}
	id := uuid.NewString()
	s.frames[id] = &imageEntry{Data: append([]byte(nil), data...), CreatedAt: time.Now()}
	s.imageLen += int64(len(data))
	f.Frames = append(f.Frames, Frame{ID: id, TimeMS: ts})
	s.trimImagesLocked()
}
func (s *Service) cleanupLocked() {
	cut := time.Now().Add(-2 * time.Hour).Unix()
	for id, f := range s.files {
		if f.TouchedAt < cut {
			for _, fr := range f.Frames {
				s.removeFrameLocked(fr.ID)
			}
			delete(s.files, id)
		}
	}
	for id, t := range s.tokens {
		if time.Now().After(t.ExpiresAt) {
			delete(s.tokens, id)
		}
	}
}
func (s *Service) trimImagesLocked() {
	for len(s.frames) > maxFrames || s.imageLen > maxImageBytes {
		var oldest string
		var at time.Time
		for id, v := range s.frames {
			if oldest == "" || v.CreatedAt.Before(at) {
				oldest = id
				at = v.CreatedAt
			}
		}
		if oldest == "" {
			break
		}
		s.removeFrameLocked(oldest)
		for _, f := range s.files {
			next := f.Frames[:0]
			for _, fr := range f.Frames {
				if fr.ID != oldest {
					next = append(next, fr)
				}
			}
			f.Frames = next
		}
	}
}
func (s *Service) removeFrameLocked(id string) {
	if v := s.frames[id]; v != nil {
		s.imageLen -= int64(len(v.Data))
		delete(s.frames, id)
	}
}
func cloneFile(f *SessionFile) *SessionFile {
	if f == nil {
		return nil
	}
	v := *f
	// API 对空候选集始终返回 []，避免前端读取 length 时遇到 null。
	v.Frames = append([]Frame{}, f.Frames...)
	return &v
}
