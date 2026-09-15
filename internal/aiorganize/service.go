package aiorganize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/httpx"
	"litepan/internal/mediaorganize/recognition"
	"litepan/internal/settings"
)

const (
	promptVersion = "v1"
	cacheTTL      = 24 * time.Hour
	maxCacheItems = 256
	maxChunkWorks = 20
	maxChunkBytes = 28 * 1024
)

type cacheEntry struct {
	result    recognition.WorkResult
	expiresAt time.Time
}

type Service struct {
	settings *settings.Service
	http     *http.Client

	mu        sync.Mutex
	cache     map[string]cacheEntry
	protocols map[string]modelProtocol
}

func New(settingsSvc *settings.Service) *Service {
	return &Service{
		settings:  settingsSvc,
		http:      httpx.NewClient(httpx.ClientOptions{Timeout: 60 * time.Second}),
		cache:     make(map[string]cacheEntry),
		protocols: make(map[string]modelProtocol),
	}
}

func (s *Service) Available() bool {
	if s == nil || s.settings == nil || !s.settings.Bool(settings.KeyAIOrganizeEnabled) {
		return false
	}
	return validateConfig(s.runtimeConfig(), true) == nil
}

func (s *Service) Enhance(ctx context.Context, req recognition.BatchRequest) (recognition.BatchResult, error) {
	return s.EnhanceWithProgress(ctx, req, nil)
}

func (s *Service) EnhanceWithProgress(
	ctx context.Context,
	req recognition.BatchRequest,
	progress recognition.ProgressFunc,
) (recognition.BatchResult, error) {
	if !s.Available() {
		return recognition.BatchResult{}, recognition.ErrUnavailable
	}
	if len(req.Works) == 0 {
		return recognition.BatchResult{Items: []recognition.WorkResult{}}, nil
	}
	cfg := s.runtimeConfig()
	result := recognition.BatchResult{Items: make([]recognition.WorkResult, 0, len(req.Works))}
	pending := make([]recognition.Work, 0, len(req.Works))

	for _, work := range req.Works {
		key := fingerprint(cfg, work)
		if cached, ok := s.getCached(key); ok {
			cached.WorkID = work.WorkID
			result.Items = append(result.Items, cached)
			result.Cached++
			continue
		}
		pending = append(pending, work)
	}
	chunks := splitWorks(pending)
	reportRecognitionProgress(progress, recognition.BatchProgress{
		Total:       len(req.Works),
		Completed:   result.Cached,
		Cached:      result.Cached,
		TotalChunks: len(chunks),
	})

	var firstChunkErr error
	completedChunks := 0
	processedWorks := 0
	for chunkIndex, chunk := range chunks {
		reportRecognitionProgress(progress, recognition.BatchProgress{
			Total:        len(req.Works),
			Completed:    result.Cached + processedWorks,
			Cached:       result.Cached,
			Failed:       result.Failed,
			CurrentChunk: chunkIndex + 1,
			TotalChunks:  len(chunks),
		})
		items, err := s.recognizeChunk(ctx, cfg, chunk)
		processedWorks += len(chunk)
		if err != nil {
			if firstChunkErr == nil {
				firstChunkErr = err
			}
			result.Failed += len(chunk)
			reportRecognitionProgress(progress, recognition.BatchProgress{
				Total:        len(req.Works),
				Completed:    result.Cached + processedWorks,
				Cached:       result.Cached,
				Failed:       result.Failed,
				CurrentChunk: chunkIndex + 1,
				TotalChunks:  len(chunks),
			})
			continue
		}
		completedChunks++
		valid := validateResults(chunk, items)
		for _, item := range valid {
			work, ok := findWork(chunk, item.WorkID)
			if !ok {
				continue
			}
			s.putCached(fingerprint(cfg, work), item)
			result.Items = append(result.Items, item)
		}
		reportRecognitionProgress(progress, recognition.BatchProgress{
			Total:        len(req.Works),
			Completed:    result.Cached + processedWorks,
			Cached:       result.Cached,
			Failed:       result.Failed,
			CurrentChunk: chunkIndex + 1,
			TotalChunks:  len(chunks),
		})
	}
	if completedChunks == 0 && len(pending) > 0 && firstChunkErr != nil && len(result.Items) == 0 {
		return recognition.BatchResult{}, firstChunkErr
	}

	sort.SliceStable(result.Items, func(i, j int) bool {
		return workPosition(req.Works, result.Items[i].WorkID) < workPosition(req.Works, result.Items[j].WorkID)
	})
	return result, nil
}

func reportRecognitionProgress(progress recognition.ProgressFunc, state recognition.BatchProgress) {
	if progress != nil {
		progress(state)
	}
}

func (s *Service) Test(ctx context.Context, in UpdateRequest) error {
	if s == nil {
		return domain.Errorf(domain.CodeInternal, "AI 辅助增强服务未就绪")
	}
	stored, found := s.storedConfigForTest(in.ID)
	if strings.TrimSpace(in.ID) != "" && !found {
		return domain.Errorf(domain.CodeNotFound, "AI 模型配置已不存在，请刷新后重试")
	}
	cfg := Config{
		BaseURL: strings.TrimSpace(in.BaseURL),
		APIKey:  strings.TrimSpace(in.APIKey),
		Model:   strings.TrimSpace(in.Model),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = stored.BaseURL
	}
	if cfg.Model == "" {
		cfg.Model = stored.Model
	}
	if cfg.APIKey == "" || isAPIKeyMask(cfg.APIKey, stored.APIKey) {
		cfg.APIKey = stored.APIKey
	}
	if err := validateConfig(cfg, true); err != nil {
		return err
	}
	content, err := s.chat(ctx, cfg, []chatMessage{
		{Role: "system", Content: "你是连接测试。只返回 JSON 对象。"},
		{Role: "user", Content: `返回 {"ok":true}。`},
	})
	if err != nil {
		return err
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := decodeJSONObject(content, &out); err != nil || !out.OK {
		return domain.Errorf(domain.CodeDriverError, "模型已响应，但没有按要求返回 JSON")
	}
	return nil
}

func (s *Service) recognizeChunk(ctx context.Context, cfg Config, works []recognition.Work) ([]recognition.WorkResult, error) {
	payload, _ := json.Marshal(struct {
		Works []recognition.Work `json:"works"`
	}{Works: sampleWorks(works)})
	messages := []chatMessage{
		{Role: "system", Content: recognitionSystemPrompt},
		{Role: "user", Content: string(payload)},
	}
	raw, err := s.chat(ctx, cfg, messages)
	if err != nil {
		return nil, err
	}
	items, err := parseRecognitionResponse(raw)
	if err == nil {
		return items, nil
	}

	repair, repairErr := s.chat(ctx, cfg, []chatMessage{
		{Role: "system", Content: recognitionRepairPrompt},
		{Role: "user", Content: raw},
	})
	if repairErr != nil {
		return nil, repairErr
	}
	items, err = parseRecognitionResponse(repair)
	if err != nil {
		return nil, domain.Errorf(domain.CodeDriverError, "模型返回格式不正确")
	}
	return items, nil
}

func parseRecognitionResponse(raw string) ([]recognition.WorkResult, error) {
	var out struct {
		Items []recognition.WorkResult `json:"items"`
	}
	if err := decodeJSONObject(raw, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		return nil, errors.New("missing items")
	}
	return out.Items, nil
}

func decodeJSONObject(raw string, out any) error {
	text := strings.TrimSpace(raw)
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return errors.New("json object not found")
	}
	dec := json.NewDecoder(strings.NewReader(text[start : end+1]))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func splitWorks(works []recognition.Work) [][]recognition.Work {
	chunks := make([][]recognition.Work, 0)
	current := make([]recognition.Work, 0, maxChunkWorks)
	size := 0
	for _, work := range works {
		encoded, _ := json.Marshal(sampleWork(work))
		if len(current) > 0 && (len(current) >= maxChunkWorks || size+len(encoded) > maxChunkBytes) {
			chunks = append(chunks, current)
			current = make([]recognition.Work, 0, maxChunkWorks)
			size = 0
		}
		current = append(current, work)
		size += len(encoded)
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func sampleWorks(works []recognition.Work) []recognition.Work {
	out := make([]recognition.Work, len(works))
	for i, work := range works {
		out[i] = sampleWork(work)
	}
	return out
}

func sampleWork(work recognition.Work) recognition.Work {
	const side = 60
	if len(work.Files) <= side*2 {
		return work
	}
	work.Files = append(append([]recognition.File(nil), work.Files[:side]...), work.Files[len(work.Files)-side:]...)
	return work
}

func validateResults(works []recognition.Work, items []recognition.WorkResult) []recognition.WorkResult {
	allowedWorks := make(map[string]recognition.Work, len(works))
	for _, work := range works {
		allowedWorks[work.WorkID] = work
	}
	seenWorks := make(map[string]struct{}, len(items))
	out := make([]recognition.WorkResult, 0, len(items))
	for _, item := range items {
		work, ok := allowedWorks[item.WorkID]
		if !ok {
			continue
		}
		if _, duplicate := seenWorks[item.WorkID]; duplicate {
			continue
		}
		seenWorks[item.WorkID] = struct{}{}
		item.Title = strings.TrimSpace(item.Title)
		item.OriginalTitle = strings.TrimSpace(item.OriginalTitle)
		item.MediaType = strings.ToLower(strings.TrimSpace(item.MediaType))
		if !item.Recognized || item.Title == "" || len([]rune(item.Title)) > 200 {
			item = recognition.WorkResult{WorkID: item.WorkID, Recognized: false}
			out = append(out, item)
			continue
		}
		if item.MediaType != "movie" && item.MediaType != "tv" {
			item.MediaType = work.MediaTypeHint
			if item.MediaType != "movie" && item.MediaType != "tv" {
				item.MediaType = "movie"
			}
		}
		if item.Year != nil && (*item.Year < 1870 || *item.Year > time.Now().Year()+3) {
			item.Year = nil
		}
		if item.Season != nil && (*item.Season < 0 || *item.Season > 100) {
			item.Season = nil
		}
		allowedFiles := make(map[string]struct{}, len(work.Files))
		for _, file := range work.Files {
			allowedFiles[file.SourceID] = struct{}{}
		}
		seenFiles := make(map[string]struct{}, len(item.Files))
		files := make([]recognition.FileResult, 0, len(item.Files))
		for _, file := range item.Files {
			if _, ok := allowedFiles[file.SourceID]; !ok {
				continue
			}
			if _, duplicate := seenFiles[file.SourceID]; duplicate {
				continue
			}
			if file.Episode != nil && (*file.Episode < 0 || *file.Episode > 100000) {
				continue
			}
			file.Kind = strings.ToLower(strings.TrimSpace(file.Kind))
			if file.Kind != "episode" && file.Kind != "movie" && file.Kind != "extra" {
				file.Kind = ""
			}
			seenFiles[file.SourceID] = struct{}{}
			files = append(files, file)
		}
		item.Files = files
		out = append(out, item)
	}
	return out
}

func fingerprint(cfg Config, work recognition.Work) string {
	work.WorkID = ""
	data, _ := json.Marshal(struct {
		Version string           `json:"version"`
		BaseURL string           `json:"base_url"`
		Model   string           `json:"model"`
		Work    recognition.Work `json:"work"`
	}{Version: promptVersion, BaseURL: cfg.BaseURL, Model: cfg.Model, Work: work})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) getCached(key string) (recognition.WorkResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(s.cache, key)
		return recognition.WorkResult{}, false
	}
	return entry.result, true
}

func (s *Service) putCached(key string, result recognition.WorkResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= maxCacheItems {
		for oldKey, entry := range s.cache {
			if time.Now().After(entry.expiresAt) {
				delete(s.cache, oldKey)
			}
		}
	}
	if len(s.cache) >= maxCacheItems {
		for oldKey := range s.cache {
			delete(s.cache, oldKey)
			break
		}
	}
	s.cache[key] = cacheEntry{result: result, expiresAt: time.Now().Add(cacheTTL)}
}

func findWork(works []recognition.Work, id string) (recognition.Work, bool) {
	for _, work := range works {
		if work.WorkID == id {
			return work, true
		}
	}
	return recognition.Work{}, false
}

func workPosition(works []recognition.Work, id string) int {
	for i, work := range works {
		if work.WorkID == id {
			return i
		}
	}
	return len(works)
}

var _ recognition.Enhancer = (*Service)(nil)
var _ recognition.ProgressEnhancer = (*Service)(nil)
