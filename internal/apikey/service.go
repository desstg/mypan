package apikey

import (
	"context"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/strm"
)

const MaxNormalKeys = 10

type StrmKeyView struct {
	Name        string `json:"name"`
	Key         string `json:"key"`
	KeyPreview  string `json:"key_preview"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	System      bool   `json:"system"`
	Deletable   bool   `json:"deletable"`
	Disableable bool   `json:"disableable"`
}

type KeyView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	KeyType    string `json:"key_type"`
	Status     string `json:"status"`
	KeyPrefix  string `json:"key_prefix"`
	KeySuffix  string `json:"key_suffix"`
	KeyPreview string `json:"key_preview"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Key        string `json:"key,omitempty"`
}

type ListResult struct {
	StrmKey  StrmKeyView `json:"strm_key"`
	Keys     []KeyView   `json:"keys"`
	MaxKeys  int         `json:"max_keys"`
	KeyCount int         `json:"key_count"`
}

type CreateInput struct {
	Name        string
	KeyType     string
	ExpiresDays *int
	Status      string
	Note        string
}

type UpdateInput struct {
	Name        *string
	KeyType     *string
	ExpiresDays *int
	Status      *string
	Note        *string
}

type Service struct {
	repo     domain.ApiKeyRepository
	settings *settings.Service
	strm     *strm.Service
	strmDir  string
	secret   []byte
}

type Options struct {
	Repo     domain.ApiKeyRepository
	Settings *settings.Service
	Strm     *strm.Service
	StrmDir  string
	Secret   []byte
}

func New(opts Options) *Service {
	return &Service{
		repo:     opts.Repo,
		settings: opts.Settings,
		strm:     opts.Strm,
		strmDir:  opts.StrmDir,
		secret:   opts.Secret,
	}
}

func (s *Service) List(ctx context.Context) (ListResult, error) {
	var out ListResult
	out.MaxKeys = MaxNormalKeys
	strmKey, err := s.ensureStrmKey(ctx)
	if err != nil {
		return out, err
	}
	out.StrmKey = strmKey
	rows, err := s.repo.List(ctx)
	if err != nil {
		return out, err
	}
	out.Keys = make([]KeyView, 0, len(rows))
	for _, row := range rows {
		out.Keys = append(out.Keys, toKeyView(row, ""))
	}
	out.KeyCount = len(out.Keys)
	return out, nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (KeyView, error) {
	count, err := s.repo.Count(ctx)
	if err != nil {
		return KeyView{}, err
	}
	if count >= MaxNormalKeys {
		return KeyView{}, domain.Errorf(domain.CodeValidation, "最多只能创建 %d 个普通 API Key", MaxNormalKeys)
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}
	keyType, err := normalizeKeyType(in.KeyType)
	if err != nil {
		return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}
	status, err := normalizeStatus(in.Status)
	if err != nil {
		return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}
	expiresAt, err := ParseExpiryDays(in.ExpiresDays)
	if err != nil {
		return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
	}
	raw, err := NewRawKey(PrefixAPI)
	if err != nil {
		return KeyView{}, domain.Wrap(domain.CodeInternal, err)
	}
	prefix, suffix := PrefixSuffix(raw)
	row := &domain.ApiKey{
		Name:      name,
		KeyHash:   Hash(raw),
		KeyPrefix: prefix,
		KeySuffix: suffix,
		KeyType:   keyType,
		Status:    status,
		ExpiresAt: expiresAt,
		Note:      strings.TrimSpace(in.Note),
	}
	id, err := s.repo.Create(ctx, row)
	if err != nil {
		return KeyView{}, err
	}
	created, err := s.repo.Get(ctx, id)
	if err != nil {
		return KeyView{}, err
	}
	return toKeyView(created, raw), nil
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (KeyView, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return KeyView{}, err
	}
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
		}
		row.Name = name
	}
	if in.KeyType != nil {
		keyType, err := normalizeKeyType(*in.KeyType)
		if err != nil {
			return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
		}
		row.KeyType = keyType
	}
	if in.Status != nil {
		status, err := normalizeStatus(*in.Status)
		if err != nil {
			return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
		}
		row.Status = status
	}
	if in.ExpiresDays != nil {
		expiresAt, err := ParseExpiryDays(in.ExpiresDays)
		if err != nil {
			return KeyView{}, domain.Errorf(domain.CodeValidation, "%s", err.Error())
		}
		row.ExpiresAt = expiresAt
	}
	if in.Note != nil {
		row.Note = strings.TrimSpace(*in.Note)
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return KeyView{}, err
	}
	updated, err := s.repo.Get(ctx, id)
	if err != nil {
		return KeyView{}, err
	}
	return toKeyView(updated, ""), nil
}

func (s *Service) Toggle(ctx context.Context, id int64) (KeyView, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return KeyView{}, err
	}
	if row.Status == domain.ApiKeyStatusActive {
		row.Status = domain.ApiKeyStatusDisabled
	} else {
		row.Status = domain.ApiKeyStatusActive
	}
	if err := s.repo.Update(ctx, row); err != nil {
		return KeyView{}, err
	}
	updated, err := s.repo.Get(ctx, id)
	if err != nil {
		return KeyView{}, err
	}
	return toKeyView(updated, ""), nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) Validate(ctx context.Context, raw string) (*domain.ApiKey, error) {
	if s == nil || s.repo == nil {
		return nil, domain.Errf(domain.CodeInternal)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, domain.Errorf(domain.CodeAdminAuthRequired, "缺少 API Key")
	}
	row, err := s.repo.GetByHash(ctx, Hash(raw))
	if err != nil {
		return nil, err
	}
	if row.Status != domain.ApiKeyStatusActive {
		return nil, domain.Errorf(domain.CodePermissionDenied, "API Key 已禁用")
	}
	if !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		return nil, domain.Errorf(domain.CodePermissionDenied, "API Key 已过期")
	}
	_ = s.repo.TouchLastUsed(ctx, row.ID, time.Now().UTC())
	return row, nil
}

func (s *Service) ValidateTask(ctx context.Context, raw string) (*domain.ApiKey, error) {
	if !strings.HasPrefix(strings.TrimSpace(raw), PrefixAPI) {
		return nil, domain.Errorf(domain.CodePermissionDenied, "API Key 类型不支持")
	}
	row, err := s.Validate(ctx, raw)
	if err != nil {
		return nil, err
	}
	if row.KeyType != domain.ApiKeyTypeTask {
		return nil, domain.Errorf(domain.CodePermissionDenied, "API Key 权限不足")
	}
	return row, nil
}

type RotateStrmResult struct {
	StrmKey       StrmKeyView              `json:"strm_key"`
	ReplaceResult *strm.ReplaceTokenResult `json:"replace_result,omitempty"`
}

func (s *Service) RotateStrmKey(ctx context.Context, applyToExisting bool) (RotateStrmResult, error) {
	var out RotateStrmResult
	if s.settings == nil {
		return out, domain.Errf(domain.CodeInternal)
	}
	oldToken, err := s.readOrCreateStrmToken(ctx)
	if err != nil {
		return out, err
	}
	newToken, err := NewRawKey(PrefixStrm)
	if err != nil {
		return out, domain.Wrap(domain.CodeInternal, err)
	}
	if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmToken: newToken}); err != nil {
		return out, err
	}
	if applyToExisting && s.strmDir != "" {
		rep, err := strm.ReplaceTokenInFiles(s.strmDir, oldToken, newToken, s.secret)
		if err != nil {
			return out, err
		}
		out.ReplaceResult = &rep
	}
	strmKey, err := s.buildStrmKeyView(newToken)
	if err != nil {
		return out, err
	}
	out.StrmKey = strmKey
	return out, nil
}

func (s *Service) ensureStrmKey(ctx context.Context) (StrmKeyView, error) {
	token, err := s.readOrCreateStrmToken(ctx)
	if err != nil {
		return StrmKeyView{}, err
	}
	return s.buildStrmKeyView(token)
}

func (s *Service) readOrCreateStrmToken(ctx context.Context) (string, error) {
	if s.strm != nil {
		return s.strm.EnsureToken(ctx)
	}
	if s.settings == nil {
		return "", domain.Errf(domain.CodeInternal)
	}
	token := strings.TrimSpace(s.settings.String(settings.KeyStrmToken))
	if token != "" {
		return token, nil
	}
	raw, err := NewRawKey(PrefixStrm)
	if err != nil {
		return "", err
	}
	if err := s.settings.Update(ctx, map[string]string{settings.KeyStrmToken: raw}); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) buildStrmKeyView(token string) (StrmKeyView, error) {
	return StrmKeyView{
		Name:        "STRM 媒体访问",
		Key:         token,
		KeyPreview:  StrmKeyPreview(token),
		Format:      "new",
		Status:      domain.ApiKeyStatusActive,
		System:      true,
		Deletable:   false,
		Disableable: false,
	}, nil
}

func toKeyView(row *domain.ApiKey, raw string) KeyView {
	view := KeyView{
		ID:         row.ID,
		Name:       row.Name,
		KeyType:    row.KeyType,
		Status:     row.Status,
		KeyPrefix:  row.KeyPrefix,
		KeySuffix:  row.KeySuffix,
		KeyPreview: KeyPreview(row.KeyPrefix, row.KeySuffix),
		Note:       row.Note,
	}
	if !row.ExpiresAt.IsZero() {
		view.ExpiresAt = row.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if !row.LastUsedAt.IsZero() {
		view.LastUsedAt = row.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if !row.CreatedAt.IsZero() {
		view.CreatedAt = row.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !row.UpdatedAt.IsZero() {
		view.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if raw != "" {
		view.Key = raw
	}
	return view
}

func normalizeName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", errMsg("名称不能为空")
	}
	if len([]rune(name)) > 40 {
		return "", errMsg("名称不能超过40个字符")
	}
	return name, nil
}

func normalizeKeyType(value string) (string, error) {
	keyType := strings.TrimSpace(value)
	if keyType == "" {
		keyType = domain.ApiKeyTypeTask
	}
	if keyType != domain.ApiKeyTypeTask && keyType != domain.ApiKeyTypeReadonly {
		return "", errMsg("Key 类型不支持")
	}
	return keyType, nil
}

func normalizeStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		status = domain.ApiKeyStatusActive
	}
	if status != domain.ApiKeyStatusActive && status != domain.ApiKeyStatusDisabled {
		return "", errMsg("Key 状态不支持")
	}
	return status, nil
}

type errMsg string

func (e errMsg) Error() string { return string(e) }
