package offlinedownload

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	gotorrent "github.com/anacrolix/torrent"
	torrentmetainfo "github.com/anacrolix/torrent/metainfo"
	torrentstorage "github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"litepan/internal/domain"
	"litepan/pkg/speedsmoother"
	"litepan/pkg/strutil"
	"litepan/pkg/timeutil"
)

const (
	builtinMagnetMetadataTimeout        = 10 * time.Minute
	builtinMagnetCompletionSyncInterval = 5 * time.Second
)

const builtinMagnetMetainfoName = "_metadata.torrent"
const builtinMagnetCompletionName = ".torrent.bolt.db"
const builtinMagnetResumeMarkerName = ".torrent.resume-ok"

var errBuiltinMagnetAlreadyActive = errors.New("相同磁力资源正在另一个任务中下载，请等待其交棒后重试")

type builtinMagnetRuntime struct {
	mu        sync.Mutex
	client    *gotorrent.Client
	nodesFile string
}

var builtinDefaultMagnetTrackers = []string{
	"udp://tracker.publictracker.xyz:6969/announce",
	"udp://open.tracker.cl:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"http://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker2.dler.org:80/announce",
	"udp://tracker.wildkat.net:6969/announce",
	"udp://tracker.tryhackx.org:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://tracker.qu.ax:6969/announce",
	"udp://tracker.linvk.com:6969/announce",
	"udp://tracker.farted.net:6969/announce",
	"udp://tracker.ducks.party:1984/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://tracker.auctor.tv:6969/announce",
	"udp://tracker-udp.gbitt.info:80/announce",
	"udp://tr4ck3r.duckdns.org:6969/announce",
	"udp://torrentclub.online:54123/announce",
	"udp://torrentclub.online:1984/announce",
	"udp://t.overflow.biz:6969/announce",
	// 保留优化前使用的两个 tracker，避免旧资源的 peer 发现回退。
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.openbittorrent.com:6969/announce",
}

func builtinURLSchemes() []string {
	return []string{"http", "https", "magnet"}
}

func executeBuiltinTaskByType(ctx context.Context, s *Service, taskID string) {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok || task.ProviderKind != ProviderBuiltin {
		s.mu.Unlock()
		return
	}
	executorType := strings.TrimSpace(task.ExecutorType)
	s.mu.Unlock()

	switch executorType {
	case ExecutorURLMagnet:
		s.executeBuiltinMagnetDownload(ctx, taskID)
	default:
		s.executeBuiltinURLDownload(ctx, taskID)
	}
}

func builtinExecutorType(raw string) (string, error) {
	u, err := parseBuiltinURL(raw)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "http", "https":
		return ExecutorURLHTTP, nil
	case "magnet":
		return ExecutorURLMagnet, nil
	default:
		return "", domain.Errorf(domain.CodeValidation, "内置下载器当前只支持 HTTP/HTTPS/Magnet：%s", raw)
	}
}

func parseBuiltinURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" {
		return nil, domain.Errorf(domain.CodeValidation, "离线下载链接格式不正确：%s", raw)
	}
	return u, nil
}

func addDefaultMagnetTrackers(spec *gotorrent.TorrentSpec) (added, total int) {
	if spec == nil {
		return 0, 0
	}
	seen := make(map[string]struct{})
	for _, tier := range spec.Trackers {
		for _, tracker := range tier {
			normalized := normalizeMagnetTracker(tracker)
			if normalized != "" {
				seen[normalized] = struct{}{}
			}
		}
	}
	for _, tracker := range builtinDefaultMagnetTrackers {
		normalized := normalizeMagnetTracker(tracker)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		// 独立成 tier，不改变链接自带 tracker 的优先级和分组。
		spec.Trackers = append(spec.Trackers, []string{strings.TrimSpace(tracker)})
		seen[normalized] = struct{}{}
		added++
	}
	return added, len(seen)
}

func normalizeMagnetTracker(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

func builtinMagnetDHTNodesFile(root string) string {
	return filepath.Join(root, "_dht_nodes.dat")
}

func builtinMagnetMetainfoFile(baseDir string) string {
	return filepath.Join(baseDir, builtinMagnetMetainfoName)
}

func newBuiltinMagnetClientConfig(baseDir string, limiter *rate.Limiter, listenPort int) *gotorrent.ClientConfig {
	cfg := gotorrent.NewDefaultClientConfig()
	cfg.DataDir = baseDir
	// 每个任务都会传入自己的持久化存储；共享客户端的默认存储只作兜底，
	// 使用内存完成度表，避免在临时目录根部再创建一份永远用不到的数据库。
	cfg.DefaultStorage = torrentstorage.NewFileOpts(torrentstorage.NewFileClientOpts{
		ClientBaseDir:   baseDir,
		PieceCompletion: torrentstorage.NewMapPieceCompletion(),
	})
	// TCP/uTP 共用该端口，DHT 复用 UDP socket；其余网络策略沿用库默认值。
	cfg.ListenPort = listenPort
	if limiter != nil {
		cfg.DownloadRateLimiter = limiter
	}
	return cfg
}

func restoreBuiltinMagnetMetainfo(file string, spec *gotorrent.TorrentSpec) bool {
	if spec == nil {
		return false
	}
	mi, err := torrentmetainfo.LoadFromFile(file)
	if err != nil {
		return false
	}
	cached, err := gotorrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil || !sameTorrentSpecIdentity(spec, cached) {
		return false
	}
	spec.InfoBytes = cached.InfoBytes
	spec.PieceLayers = cached.PieceLayers
	return true
}

func sameTorrentSpecIdentity(left, right *gotorrent.TorrentSpec) bool {
	if left == nil || right == nil {
		return false
	}
	zero := torrentmetainfo.Hash{}
	matched := false
	if left.InfoHash != zero {
		if right.InfoHash != left.InfoHash {
			return false
		}
		matched = true
	}
	if left.InfoHashV2.Ok {
		if !right.InfoHashV2.Ok || right.InfoHashV2.Value != left.InfoHashV2.Value {
			return false
		}
		matched = true
	}
	return matched
}

func saveBuiltinMagnetMetainfo(file string, mi torrentmetainfo.MetaInfo) (err error) {
	if len(mi.InfoBytes) == 0 {
		return errors.New("磁力元数据为空")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".metadata-*.torrent")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err = tmp.Chmod(0o640); err == nil {
		err = mi.Write(tmp)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmpName, file)
}

func addBuiltinMagnetTorrent(client *gotorrent.Client, spec *gotorrent.TorrentSpec, baseDir string) (*gotorrent.Torrent, error) {
	taskStorage := torrentstorage.NewFile(baseDir)
	spec.Storage = taskStorage
	tor, isNew, err := client.AddTorrentSpec(spec)
	if err != nil {
		if !isNew {
			_ = taskStorage.Close()
		}
		return nil, err
	}
	if !isNew {
		_ = taskStorage.Close()
		return nil, errBuiltinMagnetAlreadyActive
	}
	return tor, nil
}

func syncBuiltinMagnetCompletion(baseDir string) error {
	file, err := os.OpenFile(filepath.Join(baseDir, builtinMagnetCompletionName), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func markBuiltinMagnetResumeClean(baseDir string) error {
	file, err := os.OpenFile(filepath.Join(baseDir, builtinMagnetResumeMarkerName), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err = file.WriteString("ok\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func consumeBuiltinMagnetResumeClean(baseDir string) bool {
	marker := filepath.Join(baseDir, builtinMagnetResumeMarkerName)
	if _, err := os.Stat(marker); err != nil {
		return false
	}
	_ = os.Remove(marker)
	return true
}

func hasBuiltinMagnetPayload(baseDir string) bool {
	found := false
	_ = filepath.WalkDir(baseDir, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || found || entry.IsDir() {
			return nil
		}
		switch entry.Name() {
		case builtinMagnetMetainfoName, builtinMagnetCompletionName, builtinMagnetResumeMarkerName:
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".metadata-") {
			return nil
		}
		found = true
		return nil
	})
	return found
}

func shouldVerifyBuiltinMagnetResume(
	persisted, completed, total, pieceLength int64,
	metainfoRestored, cleanResume, hasPayload bool,
) bool {
	if metainfoRestored && !cleanResume && hasPayload {
		return true
	}
	if persisted <= completed || persisted <= 0 {
		return false
	}
	if total > 0 && persisted > total {
		persisted = total
	}
	tolerance := 4 * int64(1024*1024)
	if pieceLength > 0 && pieceLength*2 > tolerance {
		tolerance = pieceLength * 2
	}
	return persisted-completed > tolerance
}

func waitBuiltinMagnetVerificationSettled(ctx context.Context, tor *gotorrent.Torrent) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		settled := true
		for i := 0; i < tor.NumPieces(); i++ {
			state := tor.Piece(i).State()
			if state.Hashing || state.QueuedForHash || state.Marking {
				settled = false
				break
			}
		}
		if settled {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func restoreBuiltinDHTNodes(client *gotorrent.Client, nodesFile string) error {
	for _, server := range client.DhtServers() {
		wrapper, ok := server.(gotorrent.AnacrolixDhtServerWrapper)
		if !ok || wrapper.Server == nil {
			continue
		}
		_, err := wrapper.AddNodesFromFile(nodesFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func persistBuiltinDHTNodes(client *gotorrent.Client, nodesFile string) error {
	if err := os.MkdirAll(filepath.Dir(nodesFile), 0o755); err != nil {
		return err
	}
	nodes := make([]krpc.NodeInfo, 0, 256)
	for _, server := range client.DhtServers() {
		wrapper, ok := server.(gotorrent.AnacrolixDhtServerWrapper)
		if !ok || wrapper.Server == nil {
			continue
		}
		nodes = append(nodes, wrapper.Nodes()...)
	}
	if len(nodes) == 0 {
		return nil
	}
	return dht.WriteNodesToFile(nodes, nodesFile)
}

func (s *Service) builtinMagnetClient(root string) (*gotorrent.Client, error) {
	limiter := s.downloadLimiter.configure(builtinSpeedLimitBytes(s.settings))
	s.magnet.mu.Lock()
	defer s.magnet.mu.Unlock()
	if s.magnet.client != nil {
		return s.magnet.client, nil
	}
	client, err := gotorrent.NewClient(newBuiltinMagnetClientConfig(root, limiter, builtinMagnetListenPort(s.settings)))
	if err != nil {
		return nil, err
	}
	nodesFile := builtinMagnetDHTNodesFile(root)
	if err := restoreBuiltinDHTNodes(client, nodesFile); err != nil && s.log != nil {
		s.log.Warn("恢复磁力 DHT 节点缓存失败", "error", err)
	}
	s.magnet.client = client
	s.magnet.nodesFile = nodesFile
	return client, nil
}

func (s *Service) closeBuiltinMagnetClient() {
	s.magnet.mu.Lock()
	defer s.magnet.mu.Unlock()
	client := s.magnet.client
	if client == nil {
		return
	}
	if err := persistBuiltinDHTNodes(client, s.magnet.nodesFile); err != nil && s.log != nil {
		s.log.Warn("保存磁力 DHT 节点缓存失败", "error", err)
	}
	client.Close()
	s.magnet.client = nil
	s.magnet.nodesFile = ""
}

func collectBuiltinMagnetDiagnostics(client *gotorrent.Client, tor *gotorrent.Torrent, trackerCount int, stage string) *MagnetDiagnostics {
	if client == nil || tor == nil {
		return nil
	}
	var dhtNodes int
	var dhtGoodNodes int
	var dhtOutstanding int
	for _, server := range client.DhtServers() {
		wrapper, ok := server.(gotorrent.AnacrolixDhtServerWrapper)
		if !ok || wrapper.Server == nil {
			continue
		}
		stats := wrapper.Server.Stats()
		dhtNodes += stats.Nodes
		dhtGoodNodes += stats.GoodNodes
		dhtOutstanding += stats.OutstandingTransactions
	}
	stats := tor.Stats()
	return &MagnetDiagnostics{
		Stage:                 strings.TrimSpace(stage),
		TrackerCount:          trackerCount,
		DHTNodes:              dhtNodes,
		DHTGoodNodes:          dhtGoodNodes,
		DHTOutstandingQueries: dhtOutstanding,
		ActivePeers:           stats.ActivePeers,
		PendingPeers:          stats.PendingPeers,
		TotalPeers:            stats.TotalPeers,
		ConnectedSeeders:      stats.ConnectedSeeders,
		HalfOpenPeers:         stats.HalfOpenPeers,
		MetadataReady:         tor.Info() != nil,
		LastSampleAt:          timeutil.UnixFloat(time.Now()),
	}
}

func magnetMetadataWaitingMessage() string {
	return "正在获取磁力文件信息"
}

func magnetMetadataTimeoutError(addedTrackerCount int) error {
	if addedTrackerCount <= 0 {
		return domain.Errorf(domain.CodeInternal, "获取磁力文件信息超时：仍未找到可下载的资源，通常是资源较冷或当前网络无法连接磁力检索网络")
	}
	return domain.Errorf(domain.CodeInternal, "获取磁力文件信息超时：已尝试 %d 个公共查找服务器，仍未找到可下载的资源，通常是资源较冷或当前网络无法连接磁力检索网络", addedTrackerCount)
}

func (s *Service) waitBuiltinMagnetInfo(
	ctx context.Context,
	taskID string,
	client *gotorrent.Client,
	tor *gotorrent.Torrent,
	trackerCount int,
	addedTrackerCount int,
	waitingMessage string,
) error {
	timeout := time.NewTimer(builtinMagnetMetadataTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			s.patchBuiltinTask(taskID, func(task *Task) {
				task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "metadata_timeout")
			})
			return magnetMetadataTimeoutError(addedTrackerCount)
		case <-tor.GotInfo():
			s.patchBuiltinTask(taskID, func(task *Task) {
				task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "metadata_ready")
			})
			return nil
		case <-ticker.C:
			s.downloadLimiter.configure(builtinSpeedLimitBytes(s.settings))
			s.patchBuiltinTask(taskID, func(task *Task) {
				task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "metadata")
				task.Message = waitingMessage
				task.Error = ""
			})
		}
	}
}

func (s *Service) executeBuiltinMagnetDownload(ctx context.Context, taskID string) {
	s.mu.Lock()
	task, ok := s.tasks[taskID]
	if !ok || task.ProviderKind != ProviderBuiltin {
		s.mu.Unlock()
		return
	}
	copy := *task
	s.mu.Unlock()

	root := s.BuiltinTempDir()
	baseDir := filepath.Join(root, copy.TaskID)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return
	}
	cleanResume := consumeBuiltinMagnetResumeClean(baseDir)
	if strings.TrimSpace(copy.LocalTempPath) == "" {
		s.patchBuiltinTask(taskID, func(task *Task) {
			task.LocalTempPath = baseDir
			task.Message = strutil.FirstNonEmpty(task.Message, "正在获取磁力文件信息")
			task.Error = ""
		})
	}
	spec, err := gotorrent.TorrentSpecFromMagnetUri(copy.Source)
	if err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("解析磁力任务失败: %v", err))
		return
	}
	addedTrackerCount, trackerCount := addDefaultMagnetTrackers(spec)
	waitingMessage := magnetMetadataWaitingMessage()
	metainfoFile := builtinMagnetMetainfoFile(baseDir)
	metainfoRestored := restoreBuiltinMagnetMetainfo(metainfoFile, spec)

	client, err := s.builtinMagnetClient(root)
	if err != nil {
		s.failBuiltinTask(taskID, fmt.Sprintf("初始化磁力下载器失败: %v", err))
		return
	}

	tor, err := addBuiltinMagnetTorrent(client, spec, baseDir)
	if err != nil {
		if errors.Is(err, errBuiltinMagnetAlreadyActive) {
			s.failBuiltinTask(taskID, err.Error())
			return
		}
		s.failBuiltinTask(taskID, fmt.Sprintf("添加磁力任务失败: %v", err))
		return
	}
	handoffComplete := false
	defer func() {
		tor.Drop()
		if handoffComplete {
			cleanupBuiltinMagnetArtifacts(baseDir)
			return
		}
		if err := syncBuiltinMagnetCompletion(baseDir); err != nil {
			if s.log != nil {
				s.log.Warn("保存磁力断点失败", "task_id", taskID, "error", err)
			}
			return
		}
		if err := markBuiltinMagnetResumeClean(baseDir); err != nil && s.log != nil {
			s.log.Warn("记录磁力安全断点失败", "task_id", taskID, "error", err)
		}
	}()

	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Status = "running"
		task.Phase = PhaseDownloading
		task.ExecutorType = ExecutorURLMagnet
		task.LocalTempPath = baseDir
		task.Message = waitingMessage
		task.Error = ""
		task.SpeedBytes = 0
		task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "metadata")
	})

	if err := s.waitBuiltinMagnetInfo(ctx, taskID, client, tor, trackerCount, addedTrackerCount, waitingMessage); err != nil {
		s.failBuiltinTask(taskID, err.Error())
		return
	}
	info := tor.Info()
	if info == nil {
		s.failBuiltinTask(taskID, "磁力文件信息获取失败")
		return
	}
	totalBytes := info.TotalLength()
	if totalBytes <= 0 {
		s.failBuiltinTask(taskID, "磁力内容为空")
		return
	}
	completedBytes := tor.BytesCompleted()
	if shouldVerifyBuiltinMagnetResume(
		copy.DownloadedBytes,
		completedBytes,
		totalBytes,
		info.PieceLength,
		metainfoRestored,
		cleanResume,
		hasBuiltinMagnetPayload(baseDir),
	) {
		s.patchBuiltinTask(taskID, func(task *Task) {
			task.Status = "running"
			task.Phase = PhaseVerifying
			task.Size = totalBytes
			task.DownloadedBytes = completedBytes
			task.Progress = clampProgress(calcBuiltinProgress(completedBytes, totalBytes))
			task.SpeedBytes = 0
			task.Message = "正在校验已有数据"
			task.Error = ""
			task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "verifying")
		})
		if err := tor.VerifyDataContext(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.failBuiltinTask(taskID, fmt.Sprintf("校验已有磁力数据失败: %v", err))
			return
		}
		if err := waitBuiltinMagnetVerificationSettled(ctx, tor); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.failBuiltinTask(taskID, fmt.Sprintf("等待磁力数据校验完成失败: %v", err))
			return
		}
		completedBytes = tor.BytesCompleted()
		if err := syncBuiltinMagnetCompletion(baseDir); err != nil && s.log != nil {
			s.log.Warn("保存恢复后的磁力断点失败", "task_id", taskID, "error", err)
		}
	}
	if !metainfoRestored {
		if err := saveBuiltinMagnetMetainfo(metainfoFile, tor.Metainfo()); err != nil && s.log != nil {
			s.log.Warn("保存磁力元数据缓存失败", "task_id", taskID, "error", err)
		}
	}
	finalName := builtinMagnetTaskName(copy.Name, tor.Name(), info.BestName())
	infoHash := strings.ToLower(tor.InfoHash().HexString())

	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Status = "running"
		task.Phase = PhaseDownloading
		task.Name = finalName
		task.InfoHash = infoHash
		task.LocalTempPath = baseDir
		task.Size = totalBytes
		task.DownloadedBytes = completedBytes
		task.Progress = clampProgress(calcBuiltinProgress(completedBytes, totalBytes))
		task.Message = "正在下载"
		task.Error = ""
		task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "downloading")
	})

	tor.DownloadAll()
	speed := speedsmoother.NewDefault()
	lastBytes := tor.BytesCompleted()
	lastCompletionSync := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for !tor.Complete().Bool() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.downloadLimiter.configure(builtinSpeedLimitBytes(s.settings))
			now := time.Now()
			done := tor.BytesCompleted()
			delta := done - lastBytes
			if delta < 0 {
				delta = 0
			}
			displayTotal := totalBytes
			if displayTotal <= 0 {
				displayTotal = done
			}
			progress := clampProgress(calcBuiltinProgress(done, displayTotal))
			phase := PhaseDownloading
			message := "正在下载"
			speedDisplay := speed.Sample(delta, now, "magnet").Display
			if displayTotal > 0 && done >= displayTotal {
				progress = 99
				phase = PhaseVerifying
				message = "校验中"
				speedDisplay = 0
			}
			s.patchBuiltinTask(taskID, func(task *Task) {
				task.Status = "running"
				task.Phase = phase
				task.Name = finalName
				task.InfoHash = infoHash
				task.LocalTempPath = baseDir
				task.Size = displayTotal
				task.DownloadedBytes = done
				task.Progress = progress
				task.SpeedBytes = speedDisplay
				task.Message = message
				task.Error = ""
				if phase == PhaseVerifying {
					task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "verifying")
				} else {
					task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "downloading")
				}
			})
			if now.Sub(lastCompletionSync) >= builtinMagnetCompletionSyncInterval {
				if err := syncBuiltinMagnetCompletion(baseDir); err != nil && s.log != nil {
					s.log.Warn("保存磁力断点失败", "task_id", taskID, "error", err)
				}
				lastCompletionSync = now
			}
			lastBytes = done
		}
	}
	completedBytes = tor.BytesCompleted()
	if completedBytes <= 0 {
		s.failBuiltinTask(taskID, "磁力下载结果为空")
		return
	}
	s.patchBuiltinTask(taskID, func(task *Task) {
		task.Name = finalName
		task.InfoHash = infoHash
		task.LocalTempPath = baseDir
		task.Size = totalBytes
		task.DownloadedBytes = completedBytes
		task.Progress = 100
		task.Phase = PhaseHandoff
		task.SpeedBytes = 0
		task.Message = "下载完成，准备交给上传"
		task.Error = ""
		task.MagnetDiagnostics = collectBuiltinMagnetDiagnostics(client, tor, trackerCount, "handoff")
	})
	handoffComplete = s.handoffBuiltinMagnetResult(ctx, taskID, baseDir, info)
}

func builtinMagnetTaskName(currentName, torrentName, bestName string) string {
	currentName = strings.TrimSpace(currentName)
	if currentName != "" && currentName != "磁力链接任务" {
		return safeBuiltinFileName(currentName)
	}
	return safeBuiltinFileName(strutil.FirstNonEmpty(torrentName, bestName, "磁力任务"))
}

func removeBuiltinTempPath(localPath string) {
	p := filepath.Clean(strings.TrimSpace(localPath))
	if p == "" || p == "." {
		return
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		_ = os.RemoveAll(p)
		return
	}
	_ = os.Remove(p)
	parent := filepath.Dir(p)
	if parent != "" && parent != "." {
		_ = os.Remove(parent)
	}
}

// 下载交棒后，种子元数据和分片完成度数据库已经没有恢复价值。
// 先由 Torrent.Drop 关闭存储，再移除这两个控制文件；真实内容仍由上传任务逐文件清理。
func cleanupBuiltinMagnetArtifacts(baseDir string) {
	baseDir = filepath.Clean(strings.TrimSpace(baseDir))
	if baseDir == "" || baseDir == "." {
		return
	}
	_ = os.Remove(filepath.Join(baseDir, builtinMagnetMetainfoName))
	_ = os.Remove(filepath.Join(baseDir, builtinMagnetCompletionName))
	_ = os.Remove(filepath.Join(baseDir, builtinMagnetResumeMarkerName))
	// 单文件可能已经上传完成；目录为空时顺手回收，非空则由上传任务最后收口。
	_ = os.Remove(baseDir)
}
