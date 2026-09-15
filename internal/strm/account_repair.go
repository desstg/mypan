package strm

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"litepan/internal/domain"
)

const accountRepairSampleSize = 3

var strmPlayURLPattern = regexp.MustCompile(`(?i)(?:https?://[^/]+)?/api/strm/play/(\d+)/([^/]+)/t/([^/]+)/n/([^/?#\s]+)(?:/s/([^/?#\s]+))?`)

type parsedStrmPlayURL struct {
	AccountID int64
	FileKey   string
	FileID    string
	Token     string
	FileName  string
	Signature string
}

type AccountRepairPrecheckInput struct {
	AccountID    int64
	ParentID     string
	Recursive    bool
	OutputFolder string
}

type AccountRepairPrecheckResult struct {
	NeedsPrompt    bool   `json:"needs_prompt"`
	HasHistoryStrm bool   `json:"has_history_strm"`
	OldAccountID   int64  `json:"old_account_id,omitempty"`
	SampleTotal    int    `json:"sample_total"`
	SampleMatched  int    `json:"sample_matched"`
	CanRepair      bool   `json:"can_repair"`
	Message        string `json:"message,omitempty"`
}

type AccountRepairInput struct {
	AccountID    int64
	OutputFolder string
	OldAccountID int64
	ParentID     string
	Recursive    bool
}

type AccountRepairResult struct {
	Total   int `json:"total"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

func repairMatchRequired(sampleTotal int) int {
	switch {
	case sampleTotal <= 1:
		return 1
	case sampleTotal == 2:
		return 2
	default:
		return 2
	}
}

func parseStrmPlayURL(line string) (parsedStrmPlayURL, bool) {
	line = strings.TrimSpace(line)
	m := strmPlayURLPattern.FindStringSubmatch(line)
	if len(m) < 5 {
		return parsedStrmPlayURL{}, false
	}
	accountID, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || accountID <= 0 {
		return parsedStrmPlayURL{}, false
	}
	fileID, err := DecodeFileKey(m[2])
	if err != nil || fileID == "" {
		return parsedStrmPlayURL{}, false
	}
	fileName, err := url.PathUnescape(m[4])
	if err != nil || fileName == "" {
		return parsedStrmPlayURL{}, false
	}
	out := parsedStrmPlayURL{
		AccountID: accountID,
		FileKey:   m[2],
		FileID:    fileID,
		Token:     m[3],
		FileName:  fileName,
	}
	if len(m) > 5 {
		out.Signature = m[5]
	}
	return out, true
}

func sampleLocalStrmFiles(root string, limit int) ([]string, error) {
	var all []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			return nil
		}
		all = append(all, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, nil
	}
	if len(all) <= limit {
		return all, nil
	}
	out := make([]string, 0, limit)
	step := float64(len(all)) / float64(limit)
	for i := 0; i < limit; i++ {
		idx := int(float64(i) * step)
		if idx >= len(all) {
			idx = len(all) - 1
		}
		out = append(out, all[idx])
	}
	return out, nil
}

func dominantOldAccountID(samples []parsedStrmPlayURL, currentAccountID int64) int64 {
	counts := make(map[int64]int)
	for _, s := range samples {
		if s.AccountID == currentAccountID {
			continue
		}
		counts[s.AccountID]++
	}
	var bestID int64
	var bestCount int
	for id, n := range counts {
		if n > bestCount {
			bestCount = n
			bestID = id
		}
	}
	return bestID
}

func strmFileNameEqual(want, got string) bool {
	return strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got))
}

func matchRepairSamples(ctx context.Context, files accountRepairFiles, accountID int64, samples []parsedStrmPlayURL) (matched int, err error) {
	if files == nil || len(samples) == 0 {
		return 0, nil
	}
	required := repairMatchRequired(len(samples))
	checked := 0
	for _, sample := range samples {
		checked++
		item, infoErr := files.Info(ctx, accountID, sample.FileID)
		if infoErr != nil {
			if ae, ok := domain.AsAppError(infoErr); ok && ae.Code == domain.CodeNotImplement {
				return 0, domain.Errorf(domain.CodeNotImplement, "当前网盘不支持按文件 ID 校验，无法自动关联")
			}
		} else if item != nil && !item.IsDir && strmFileNameEqual(sample.FileName, item.Name) {
			matched++
		}
		if matched >= required {
			return matched, nil
		}
		if matched+(len(samples)-checked) < required {
			return matched, nil
		}
	}
	return matched, nil
}

type accountRepairFiles interface {
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	Info(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error)
}

func PrecheckAccountRepair(ctx context.Context, files accountRepairFiles, strmDir string, in AccountRepairPrecheckInput) (AccountRepairPrecheckResult, error) {
	var out AccountRepairPrecheckResult
	if in.AccountID <= 0 {
		return out, domain.Errorf(domain.CodeValidation, "请选择账号")
	}
	hasHistoryStrm, samples, oldAccountID, err := probeLocalStrmForRepair(strmDir, in.OutputFolder, in.AccountID)
	if err != nil {
		return out, err
	}
	if !hasHistoryStrm {
		return out, nil
	}
	out.HasHistoryStrm = true
	out.SampleTotal = len(samples)
	if out.SampleTotal == 0 {
		out.Message = "目录中存在 STRM 文件，但无法解析播放链接"
		return out, nil
	}
	if oldAccountID <= 0 {
		return out, nil
	}
	out.OldAccountID = oldAccountID
	out.NeedsPrompt = true
	return out, nil
}

func evaluateAccountRepairMatch(ctx context.Context, files accountRepairFiles, in AccountRepairPrecheckInput, oldAccountID int64, samples []parsedStrmPlayURL) (matched int, canRepair bool, message string, err error) {
	candidates := repairMatchCandidates(oldAccountID, samples)
	if len(candidates) == 0 {
		return 0, false, "未能从抽样中找到指向旧账号的 STRM", nil
	}
	matched, err = matchRepairSamples(ctx, files, in.AccountID, candidates)
	if err != nil {
		return matched, false, "", err
	}
	required := repairMatchRequired(len(candidates))
	if matched >= required {
		return matched, true, fmt.Sprintf("抽样 %d 个 STRM，其中 %d 个与当前账号匹配", len(candidates), matched), nil
	}
	return matched, false, fmt.Sprintf("抽样 %d 个 STRM，仅 %d 个与当前账号匹配，无法关联", len(candidates), matched), nil
}

func repairMatchCandidates(oldAccountID int64, samples []parsedStrmPlayURL) []parsedStrmPlayURL {
	if oldAccountID <= 0 || len(samples) == 0 {
		return nil
	}
	out := make([]parsedStrmPlayURL, 0, len(samples))
	for _, sample := range samples {
		if sample.AccountID == oldAccountID {
			out = append(out, sample)
		}
	}
	return out
}

func probeLocalStrmForRepair(strmDir, outputFolder string, currentAccountID int64) (hasHistoryStrm bool, samples []parsedStrmPlayURL, oldAccountID int64, err error) {
	outputFolder = strings.TrimSpace(outputFolder)
	if outputFolder == "" {
		return false, nil, 0, domain.Errorf(domain.CodeValidation, "请填写输出目录")
	}
	root := TaskOutputDir(strmDir, outputFolder)
	info, statErr := os.Stat(root)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil, 0, nil
		}
		return false, nil, 0, statErr
	}
	if !info.IsDir() {
		return false, nil, 0, nil
	}
	paths, walkErr := sampleLocalStrmFiles(root, accountRepairSampleSize)
	if walkErr != nil {
		return false, nil, 0, walkErr
	}
	if len(paths) == 0 {
		return false, nil, 0, nil
	}
	hasHistoryStrm = true
	samples = make([]parsedStrmPlayURL, 0, len(paths))
	for _, path := range paths {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		line := strings.TrimSpace(strings.Split(string(content), "\n")[0])
		p, ok := parseStrmPlayURL(line)
		if !ok {
			continue
		}
		samples = append(samples, p)
	}
	oldAccountID = dominantOldAccountID(samples, currentAccountID)
	return hasHistoryStrm, samples, oldAccountID, nil
}

func RepairAccountReferences(ctx context.Context, files accountRepairFiles, strmDir, baseURL, token string, signEnabled bool, secret []byte, in AccountRepairInput) (AccountRepairResult, error) {
	var out AccountRepairResult
	if in.AccountID <= 0 || in.OldAccountID <= 0 {
		return out, domain.Errorf(domain.CodeValidation, "账号参数无效")
	}
	if in.AccountID == in.OldAccountID {
		return out, domain.Errorf(domain.CodeValidation, "新旧账号相同，无需修复")
	}
	outputFolder := strings.TrimSpace(in.OutputFolder)
	if outputFolder == "" {
		return out, domain.Errorf(domain.CodeValidation, "请填写输出目录")
	}
	_, samples, oldAccountID, err := probeLocalStrmForRepair(strmDir, outputFolder, in.AccountID)
	if err != nil {
		return out, err
	}
	if oldAccountID != in.OldAccountID {
		return out, domain.Errorf(domain.CodeValidation, "历史 STRM 账号与请求不一致，无法修复")
	}
	_, canRepair, msg, err := evaluateAccountRepairMatch(ctx, files, AccountRepairPrecheckInput{
		AccountID:    in.AccountID,
		ParentID:     in.ParentID,
		Recursive:    in.Recursive,
		OutputFolder: outputFolder,
	}, oldAccountID, samples)
	if err != nil {
		return out, err
	}
	if !canRepair {
		if msg == "" {
			msg = "当前账号与历史 STRM 不匹配，无法修复"
		}
		return out, domain.Errorf(domain.CodeValidation, "%s", msg)
	}
	root := TaskOutputDir(strmDir, outputFolder)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".strm") {
			return nil
		}
		out.Total++
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			out.Failed++
			return nil
		}
		lines := strings.Split(string(content), "\n")
		if len(lines) == 0 {
			out.Skipped++
			return nil
		}
		parsed, ok := parseStrmPlayURL(strings.TrimSpace(lines[0]))
		if !ok || parsed.AccountID != in.OldAccountID {
			out.Skipped++
			return nil
		}
		newURL := BuildPlayURL(baseURL, in.AccountID, parsed.FileID, parsed.FileName, token, signEnabled, secret)
		lines[0] = newURL
		outText := strings.Join(lines, "\n")
		if !strings.HasSuffix(outText, "\n") {
			outText += "\n"
		}
		if writeErr := os.WriteFile(path, []byte(outText), 0o644); writeErr != nil {
			out.Failed++
			return nil
		}
		out.Updated++
		return nil
	})
	return out, err
}
