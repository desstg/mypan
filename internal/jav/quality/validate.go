package quality

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 订阅的目标类型与下载模式。
var (
	ValidTargetTypes    = map[string]struct{}{"movie": {}, "online": {}, "actor": {}, "list": {}}
	ValidDownloadModes  = map[string]struct{}{"strict": {}, "upgrade": {}}
	ValidQualities      = map[string]struct{}{"hd": {}, "uhd": {}, "subtitle": {}, "uncensored": {}}
	ValidSubfolderModes = map[string]struct{}{"none": {}, "code": {}, "title": {}}
)

// Input 是订阅表单提交上来的原始数据。
type Input struct {
	TargetType string
	TargetID   string
	TargetURL  string
	TargetName string

	DownloadMode string
	PreDownload  bool

	// IncludeCommentLinks 把评论区分享的链接也纳入候选池。见 domain.JavSubscription。
	IncludeCommentLinks bool

	Qualities         []string
	MinSizeMB         *int
	MaxSizeMB         *int
	MaxFileCount      *int
	ReleaseDateFrom   string
	ReleaseDateTo     string
	ExpiryDays        *int
	Categories        []string
	ExcludeCategories []string

	Enabled bool

	TargetAccountID   int64
	TargetParentID    string
	TargetDisplayPath string
	PushProvider      string
	SubfolderMode     string
}

// Payload 是校验并规范化之后的订阅条件。
type Payload struct {
	TargetType string
	TargetID   string
	TargetURL  string
	TargetKey  string
	TargetName string

	DownloadMode string
	PreDownload  bool

	IncludeCommentLinks bool

	Qualities         []string
	MinSizeMB         int
	HasMinSize        bool
	MaxSizeMB         int
	HasMaxSize        bool
	MaxFileCount      int
	HasMaxFileCount   bool
	ReleaseDateFrom   string
	ReleaseDateTo     string
	ExpiryDays        int
	HasExpiryDays     bool
	Categories        []string
	ExcludeCategories []string

	Enabled bool

	TargetAccountID   int64
	TargetParentID    string
	TargetDisplayPath string
	PushProvider      string
	SubfolderMode     string
}

// CanonicalTargetKey 生成订阅目标的规范化键。
//
// 一律小写：JAVDB 的影片 id 是大小写敏感的 base62，但订阅去重不该因此
// 认为 ZY5eq 与 zy5eq 是两条不同的订阅 —— 后者根本不存在，出现的可能性
// 只来自用户手抄或 URL 大小写被改过。冲突的代价（重复订阅、重复推送）
// 远高于此。
func CanonicalTargetKey(targetType, targetID, targetURL string) string {
	value := strings.TrimSpace(targetID)
	if value == "" {
		value = strings.TrimSpace(targetURL)
	}
	return strings.ToLower(strings.TrimSpace(targetType)) + ":" + strings.ToLower(value)
}

// ValidateSubscriptionPayload 校验并规范化订阅输入。
//
// creating=true 时要求 target_id 或 target_url 至少有一个；更新时允许只改条件
// 而不动目标 —— 那两种字段在编辑表单里是只读的，不该因为没回传就报错。
//
// 校验规则逐条对应源码 subscriptions.validate_subscription_payload。
func ValidateSubscriptionPayload(in Input, creating bool) (*Payload, error) {
	targetType := strings.ToLower(strings.TrimSpace(in.TargetType))
	if _, ok := ValidTargetTypes[targetType]; !ok {
		return nil, fmt.Errorf("订阅类型无效")
	}

	name := strings.TrimSpace(in.TargetName)
	if name == "" {
		return nil, fmt.Errorf("订阅名称不能为空")
	}

	targetID := strings.TrimSpace(in.TargetID)
	targetURL := strings.TrimSpace(in.TargetURL)
	if creating && targetID == "" && targetURL == "" {
		return nil, fmt.Errorf("订阅目标不能为空")
	}
	if targetURL != "" && !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		return nil, fmt.Errorf("在线地址必须是 http/https 链接")
	}

	downloadMode := strings.ToLower(strings.TrimSpace(in.DownloadMode))
	if downloadMode == "" {
		downloadMode = "strict"
	}
	if _, ok := ValidDownloadModes[downloadMode]; !ok {
		return nil, fmt.Errorf("下载模式无效")
	}

	qualities, err := normalizeQualities(in.Qualities)
	if err != nil {
		return nil, err
	}

	minSize, hasMin, err := optionalInt("最小文件大小", in.MinSizeMB)
	if err != nil {
		return nil, err
	}
	maxSize, hasMax, err := optionalInt("最大文件大小", in.MaxSizeMB)
	if err != nil {
		return nil, err
	}
	if hasMin && hasMax && minSize > maxSize {
		return nil, fmt.Errorf("最小文件大小不能大于最大文件大小")
	}

	maxFiles, hasMaxFiles, err := optionalInt("最大文件数", in.MaxFileCount)
	if err != nil {
		return nil, err
	}

	expiry, hasExpiry, err := optionalInt("超期天数", in.ExpiryDays)
	if err != nil {
		return nil, err
	}

	from, err := normalizeDate("上映开始日期", in.ReleaseDateFrom)
	if err != nil {
		return nil, err
	}
	to, err := normalizeDate("上映结束日期", in.ReleaseDateTo)
	if err != nil {
		return nil, err
	}
	if from != "" && to != "" && from > to {
		return nil, fmt.Errorf("上映开始日期不能晚于结束日期")
	}

	subfolder := strings.ToLower(strings.TrimSpace(in.SubfolderMode))
	if subfolder == "" {
		subfolder = "code"
	}
	if _, ok := ValidSubfolderModes[subfolder]; !ok {
		return nil, fmt.Errorf("子目录方式无效")
	}

	provider := strings.ToLower(strings.TrimSpace(in.PushProvider))
	if provider == "" {
		provider = "auto"
	}

	return &Payload{
		TargetType: targetType,
		TargetID:   targetID,
		TargetURL:  targetURL,
		TargetKey:  CanonicalTargetKey(targetType, targetID, targetURL),
		TargetName: name,

		DownloadMode: downloadMode,
		PreDownload:  in.PreDownload,

		IncludeCommentLinks: in.IncludeCommentLinks,

		Qualities:         qualities,
		MinSizeMB:         minSize,
		HasMinSize:        hasMin,
		MaxSizeMB:         maxSize,
		HasMaxSize:        hasMax,
		MaxFileCount:      maxFiles,
		HasMaxFileCount:   hasMaxFiles,
		ReleaseDateFrom:   from,
		ReleaseDateTo:     to,
		ExpiryDays:        expiry,
		HasExpiryDays:     hasExpiry,
		Categories:        cleanStrings(in.Categories),
		ExcludeCategories: cleanStrings(in.ExcludeCategories),

		Enabled: in.Enabled,

		TargetAccountID:   in.TargetAccountID,
		TargetParentID:    strings.TrimSpace(in.TargetParentID),
		TargetDisplayPath: strings.TrimSpace(in.TargetDisplayPath),
		PushProvider:      provider,
		SubfolderMode:     subfolder,
	}, nil
}

func normalizeQualities(values []string) ([]string, error) {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		q := strings.ToLower(strings.TrimSpace(v))
		// none 是「不限」的占位，与其它几项互斥，不进最终集合。
		if q == "" || q == "none" {
			continue
		}
		if _, ok := ValidQualities[q]; !ok {
			return nil, fmt.Errorf("质量条件无效：%s", v)
		}
		set[q] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for q := range set {
		out = append(out, q)
	}
	// 排序让「同一组条件」总是得到同一个数组 —— 它会被序列化进 JSON 列，
	// 顺序不稳定时脏判断（比较前后串）会误报「有未保存的改动」。
	sort.Strings(out)
	return out, nil
}

// optionalInt 把表单里的可选数字归一成「值 + 有没有设」。
//
// nil 与 0 都算「没设」：表单清空后提交上来的是 0，而 0 在语义上就是「不限」，
// 与源码 max_file_count 的 0→None 处理一致。把它们统一掉，
// 免得「清空输入框」和「填 0」在下游表现成两件不同的事。
func optionalInt(label string, v *int) (int, bool, error) {
	if v == nil {
		return 0, false, nil
	}
	if *v < 0 {
		return 0, false, fmt.Errorf("%s不能为负数", label)
	}
	if *v == 0 {
		return 0, false, nil
	}
	return *v, true, nil
}

func normalizeDate(label, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return "", fmt.Errorf("%s格式应为 YYYY-MM-DD", label)
	}
	return t.Format("2006-01-02"), nil
}

// cleanStrings 去掉空白与重复，保留首次出现的顺序。
func cleanStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
