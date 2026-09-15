package backuprestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"litepan/internal/domain"
	"litepan/internal/store"
)

const (
	settingsPayloadVersion = 1
	maxNoteLength          = 200
	maxPasswordBytes       = 256
)

var neverRestoreConfigKeys = map[string]struct{}{
	"admin_temp_password_hash":          {},
	"admin_temp_password_expires_at":    {},
	"admin_temp_password_last_reset_at": {},
	"admin_session_generation":          {},
	"log_error_ack_at":                  {},
	"strm_token":                        {},
}

var adminCredentialKeys = []string{"admin_username", "admin_password"}

type settingsPayload struct {
	Version int               `json:"version"`
	Configs map[string]string `json:"configs"`
}

type Options struct {
	DataDir   string
	DBPath    string
	Version   string
	DB        *store.DB
	Configs   domain.ConfigRepository
	Secret    []byte
	Log       *slog.Logger
	OnRestart func()
}

type Service struct {
	dataDir    string
	dbPath     string
	backupsDir string
	restoreDir string
	version    string
	db         *store.DB
	configs    domain.ConfigRepository
	secret     []byte
	log        *slog.Logger
	onRestart  func()
	mu         sync.Mutex
}

func New(opts Options) (*Service, error) {
	if opts.DB == nil || opts.Configs == nil {
		return nil, fmt.Errorf("backup restore dependencies are incomplete")
	}
	if len(opts.Secret) < 16 {
		return nil, fmt.Errorf("backup restore secret is invalid")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	s := &Service{
		dataDir:    opts.DataDir,
		dbPath:     opts.DBPath,
		backupsDir: filepath.Join(opts.DataDir, "backups"),
		restoreDir: filepath.Join(opts.DataDir, "restore"),
		version:    strings.TrimSpace(opts.Version),
		db:         opts.DB,
		configs:    opts.Configs,
		secret:     append([]byte(nil), opts.Secret...),
		log:        opts.Log,
		onRestart:  opts.OnRestart,
	}
	if err := s.prepareDirs(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) prepareDirs() error {
	for _, dir := range []string{s.backupsDir, filepath.Join(s.backupsDir, ".tmp"), filepath.Join(s.restoreDir, "staging")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create backup directory: %w", err)
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Record, error) {
	if err := validateCreateRequest(req); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.NewString()
	workDir, err := os.MkdirTemp(filepath.Join(s.backupsDir, ".tmp"), "create-")
	if err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	defer os.RemoveAll(workDir)

	schemaVersion, err := s.db.SchemaVersion(ctx)
	if err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	scope := ScopeSettings
	components := []string{"settings"}
	payload := payloadManifest{FormatVersion: FormatVersion, Scope: scope, SchemaVersion: schemaVersion}
	var sources []archiveSource
	if req.IncludeAccounts {
		scope = ScopeFull
		components = []string{"settings", "accounts", "credentials", "tasks", "api_keys", "favorites", "secret_key"}
		payload.Scope = scope
		sources, payload, err = s.buildFullSources(ctx, workDir, payload)
	} else {
		sources, err = s.buildSettingsSources(ctx)
	}
	if err != nil {
		return Record{}, err
	}

	archivePath := filepath.Join(workDir, "payload.tar")
	if err := buildPayloadArchive(archivePath, payload, sources); err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	manifest := Manifest{
		FormatVersion: FormatVersion,
		BackupID:      id,
		AppVersion:    s.version,
		SchemaVersion: schemaVersion,
		CreatedAt:     nowText(),
		Note:          strings.TrimSpace(req.Note),
		Scope:         scope,
		Components:    components,
		AccountCount:  payload.AccountCount,
		TaskCount:     payload.TaskCount,
	}
	bodyPath := filepath.Join(workDir, "payload.enc")
	if err := encryptPayload(archivePath, bodyPath, req.Password, &manifest); err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	tempOutput := filepath.Join(workDir, "backup.lpb")
	if err := assembleBackup(tempOutput, bodyPath, manifest); err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	finalPath := s.backupPath(id)
	if err := os.Rename(tempOutput, finalPath); err != nil {
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	record, err := recordFromPath(finalPath, id)
	if err != nil {
		_ = os.Remove(finalPath)
		return Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("已创建加密备份", "backup_id", id, "scope", scope, "size", record.Size)
	return record, nil
}

func (s *Service) buildSettingsSources(ctx context.Context) ([]archiveSource, error) {
	configs, err := s.configs.All(ctx)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	filtered := filterSettingsConfigs(configs)
	body, err := json.Marshal(settingsPayload{Version: settingsPayloadVersion, Configs: filtered})
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	return []archiveSource{{Name: "settings/configs.json", Data: body}}, nil
}

func (s *Service) buildFullSources(ctx context.Context, workDir string, payload payloadManifest) ([]archiveSource, payloadManifest, error) {
	rawPath := filepath.Join(workDir, "database.raw.db")
	if err := s.db.SnapshotTo(ctx, rawPath); err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	rawDB, err := store.Open(ctx, store.Options{Path: rawPath})
	if err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	closeRaw := true
	defer func() {
		if closeRaw {
			_ = rawDB.Close()
		}
	}()
	if err := rawDB.Migrate(ctx); err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	if err := rawDB.SanitizePortableBackup(ctx); err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	counts, err := rawDB.BackupCounts(ctx)
	if err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	portablePath := filepath.Join(workDir, "litepan.db")
	if err := rawDB.SnapshotTo(ctx, portablePath); err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	if err := rawDB.Close(); err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	closeRaw = false
	payload.AccountCount = counts.Accounts
	payload.TaskCount = counts.Tasks

	favorites, err := readFavoritesOrEmpty(filepath.Join(filepath.Dir(s.dbPath), "litepan_favorites.json"))
	if err != nil {
		return nil, payload, domain.Wrap(domain.CodeInternal, err)
	}
	return []archiveSource{
		{Name: "database/litepan.db", Path: portablePath},
		{Name: "data/secret.key", Data: append([]byte(nil), s.secret...)},
		{Name: "data/litepan_favorites.json", Data: favorites},
	}, payload, nil
}

func (s *Service) List() ([]Record, error) {
	entries, err := os.ReadDir(s.backupsDir)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".lpb") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !validRecordID(id) {
			continue
		}
		record, err := recordFromPath(filepath.Join(s.backupsDir, entry.Name()), id)
		if err != nil {
			s.log.Warn("忽略无法读取的备份文件", "file", entry.Name(), "err", err)
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt > records[j].CreatedAt
	})
	return records, nil
}

func (s *Service) Import(ctx context.Context, filename, password string, source io.Reader) (Summary, error) {
	if !strings.EqualFold(filepath.Ext(strings.TrimSpace(filename)), ".lpb") {
		return Summary{}, domain.Errorf(domain.CodeValidation, "请选择 .lpb 备份文件")
	}
	if err := validatePassword(password); err != nil {
		return Summary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	workDir, err := os.MkdirTemp(filepath.Join(s.backupsDir, ".tmp"), "import-")
	if err != nil {
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	defer os.RemoveAll(workDir)
	incoming := filepath.Join(workDir, "incoming.lpb")
	if err := copyLimitedFile(incoming, source, maxBackupSize); err != nil {
		return Summary{}, err
	}
	extractDir := filepath.Join(workDir, "extract")
	manifest, payload, err := s.inspectBackup(ctx, incoming, password, extractDir)
	if err != nil {
		return Summary{}, err
	}
	id := uuid.NewString()
	finalPath := s.backupPath(id)
	if err := os.Rename(incoming, finalPath); err != nil {
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	record, err := recordFromManifest(finalPath, id, manifest)
	if err != nil {
		_ = os.Remove(finalPath)
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("已导入并校验加密备份", "backup_id", id, "scope", manifest.Scope)
	return Summary{
		Record:        record,
		AccountCount:  payload.AccountCount,
		TaskCount:     payload.TaskCount,
		NeedsRestart:  true,
		SecretFromEnv: strings.TrimSpace(os.Getenv("LITEPAN_SECRET_KEY")) != "",
	}, nil
}

func (s *Service) Delete(id string) error {
	if !validRecordID(id) {
		return domain.Errorf(domain.CodeNotFound, "备份不存在")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if plan, ok := s.readPending(); ok && plan.SourceID == id {
		return domain.Errorf(domain.CodeValidation, "该备份已准备恢复，请先取消待恢复状态")
	}
	if err := os.Remove(s.backupPath(id)); err != nil {
		if os.IsNotExist(err) {
			return domain.Errorf(domain.CodeNotFound, "备份不存在")
		}
		return domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("已删除备份", "backup_id", id)
	return nil
}

func (s *Service) Open(id string) (*os.File, Record, error) {
	if !validRecordID(id) {
		return nil, Record{}, domain.Errorf(domain.CodeNotFound, "备份不存在")
	}
	path := s.backupPath(id)
	record, err := recordFromPath(path, id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Record{}, domain.Errorf(domain.CodeNotFound, "备份不存在")
		}
		return nil, Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Record{}, domain.Wrap(domain.CodeInternal, err)
	}
	return file, record, nil
}

func (s *Service) PrepareRestore(ctx context.Context, id string, req RestoreRequest) (Summary, error) {
	if !validRecordID(id) {
		return Summary{}, domain.Errorf(domain.CodeNotFound, "备份不存在")
	}
	if err := validatePassword(req.Password); err != nil {
		return Summary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.readPending(); ok {
		return Summary{}, domain.Errorf(domain.CodeValidation, "已有一个备份等待重启恢复，请先重启或取消")
	}
	record, err := recordFromPath(s.backupPath(id), id)
	if err != nil {
		if os.IsNotExist(err) {
			return Summary{}, domain.Errorf(domain.CodeNotFound, "备份不存在")
		}
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	stageID := uuid.NewString()
	stageDir := filepath.Join(s.restoreDir, "staging", stageID)
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stageDir)
		}
	}()
	manifest, payload, err := s.inspectBackup(ctx, s.backupPath(id), req.Password, stageDir)
	if err != nil {
		return Summary{}, err
	}
	if err := s.prepareStagedDatabase(ctx, manifest.Scope, stageDir, req.RestoreAdmin); err != nil {
		return Summary{}, err
	}
	plan := pendingPlan{
		Version:       1,
		ID:            stageID,
		SourceID:      id,
		BackupNote:    manifest.Note,
		Scope:         manifest.Scope,
		RestoreAdmin:  req.RestoreAdmin,
		StageDir:      stageID,
		ReplaceSecret: manifest.Scope == ScopeFull,
		ReplaceFavs:   manifest.Scope == ScopeFull,
		CreatedAt:     nowText(),
	}
	if err := writeJSONAtomic(s.pendingPath(), plan); err != nil {
		return Summary{}, domain.Wrap(domain.CodeInternal, err)
	}
	_ = os.Remove(s.resultPath())
	ok = true
	s.log.Warn("备份恢复已准备，等待重启", "backup_id", id, "scope", manifest.Scope, "restore_admin", req.RestoreAdmin)
	return Summary{
		Record:        record,
		AccountCount:  payload.AccountCount,
		TaskCount:     payload.TaskCount,
		RestoreAdmin:  req.RestoreAdmin,
		NeedsRestart:  true,
		SecretFromEnv: strings.TrimSpace(os.Getenv("LITEPAN_SECRET_KEY")) != "",
	}, nil
}

func (s *Service) prepareStagedDatabase(ctx context.Context, scope, stageDir string, restoreAdmin bool) error {
	dbPath := filepath.Join(stageDir, "database", "litepan.db")
	if scope == ScopeSettings {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
		if err := s.db.SnapshotTo(ctx, dbPath); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
	}
	staged, err := store.Open(ctx, store.Options{Path: dbPath})
	if err != nil {
		return domain.Errorf(domain.CodeValidation, "备份数据库无法打开：%v", err)
	}
	defer staged.Close()
	if err := staged.Migrate(ctx); err != nil {
		return domain.Errorf(domain.CodeValidation, "备份数据库迁移失败：%v", err)
	}
	if scope == ScopeSettings {
		payload, err := readSettingsPayload(filepath.Join(stageDir, "settings", "configs.json"))
		if err != nil {
			return err
		}
		values := filterSettingsConfigs(payload.Configs)
		if !restoreAdmin {
			for _, key := range adminCredentialKeys {
				delete(values, key)
			}
		} else {
			values["admin_session_generation"] = randomHex(16)
		}
		if err := staged.ReplaceConfigs(ctx, nil, values); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
	} else {
		remove := []string{
			"admin_temp_password_hash",
			"admin_temp_password_expires_at",
			"admin_temp_password_last_reset_at",
			"admin_session_generation",
		}
		values := map[string]string{}
		if restoreAdmin {
			values["admin_session_generation"] = randomHex(16)
		} else {
			current, err := s.configs.All(ctx)
			if err != nil {
				return domain.Wrap(domain.CodeInternal, err)
			}
			remove = append(remove, adminCredentialKeys...)
			for _, key := range adminCredentialKeys {
				if value, exists := current[key]; exists {
					values[key] = value
				}
			}
			if generation, exists := current["admin_session_generation"]; exists {
				values["admin_session_generation"] = generation
			}
		}
		if err := staged.ReplaceConfigs(ctx, remove, values); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
	}
	if err := staged.IntegrityCheck(ctx); err != nil {
		return domain.Errorf(domain.CodeValidation, "备份数据库完整性校验失败：%v", err)
	}
	return nil
}

func (s *Service) inspectBackup(ctx context.Context, path, password, extractDir string) (Manifest, payloadManifest, error) {
	if err := os.MkdirAll(extractDir, 0o700); err != nil {
		return Manifest{}, payloadManifest{}, domain.Wrap(domain.CodeInternal, err)
	}
	archivePath := filepath.Join(extractDir, ".payload.tar")
	manifest, err := decryptPayload(path, archivePath, password)
	if err != nil {
		return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "%v", err)
	}
	defer os.Remove(archivePath)
	supported, err := store.SupportedSchemaVersion()
	if err != nil {
		return Manifest{}, payloadManifest{}, domain.Wrap(domain.CodeInternal, err)
	}
	if manifest.SchemaVersion > supported {
		return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份 schema 版本 %d 高于当前程序支持的 %d，请先升级 MyPan", manifest.SchemaVersion, supported)
	}
	payload, err := extractPayloadArchive(archivePath, extractDir)
	if err != nil {
		return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "%v", err)
	}
	if payload.Scope != manifest.Scope || payload.SchemaVersion != manifest.SchemaVersion {
		return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份公开清单与加密载荷不一致")
	}
	files := make(map[string]bool, len(payload.Files))
	for _, file := range payload.Files {
		files[file.Name] = true
	}
	if manifest.Scope == ScopeSettings {
		if len(files) != 1 || !files["settings/configs.json"] {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "设置备份载荷不完整")
		}
		if _, err := readSettingsPayload(filepath.Join(extractDir, "settings", "configs.json")); err != nil {
			return Manifest{}, payloadManifest{}, err
		}
	} else {
		for _, required := range []string{"database/litepan.db", "data/secret.key", "data/litepan_favorites.json"} {
			if !files[required] {
				return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "完整备份缺少组件：%s", required)
			}
		}
		secret, err := os.ReadFile(filepath.Join(extractDir, "data", "secret.key"))
		if err != nil || len(secret) < 16 || len(secret) > 4096 {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份 secret.key 无效")
		}
		if _, err := readFavoritesOrEmpty(filepath.Join(extractDir, "data", "litepan_favorites.json")); err != nil {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份收藏夹数据无效：%v", err)
		}
		db, err := store.Open(ctx, store.Options{Path: filepath.Join(extractDir, "database", "litepan.db")})
		if err != nil {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份数据库无法打开：%v", err)
		}
		version, versionErr := db.SchemaVersion(ctx)
		integrityErr := db.IntegrityCheck(ctx)
		closeErr := db.Close()
		if versionErr != nil || version != manifest.SchemaVersion {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份数据库 schema 版本不一致")
		}
		if integrityErr != nil || closeErr != nil {
			return Manifest{}, payloadManifest{}, domain.Errorf(domain.CodeValidation, "备份数据库完整性校验失败")
		}
	}
	return manifest, payload, nil
}

func (s *Service) CancelPending() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.readPending()
	if !ok {
		return nil
	}
	if validRecordID(plan.StageDir) {
		_ = os.RemoveAll(filepath.Join(s.restoreDir, "staging", plan.StageDir))
	}
	if err := os.Remove(s.pendingPath()); err != nil && !os.IsNotExist(err) {
		return domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("已取消待重启的备份恢复", "backup_id", plan.SourceID)
	return nil
}

func (s *Service) Status() Status {
	if plan, ok := s.readPending(); ok {
		return Status{
			State:        StateWaitingRestart,
			Message:      "备份已校验并准备完成，重启后执行恢复",
			BackupID:     plan.SourceID,
			BackupNote:   plan.BackupNote,
			Scope:        plan.Scope,
			RestoreAdmin: plan.RestoreAdmin,
			UpdatedAt:    plan.CreatedAt,
		}
	}
	var result restoreResult
	if err := readJSONFile(s.resultPath(), &result); err == nil {
		return Status(result)
	}
	return Status{State: StateIdle}
}

func (s *Service) AcknowledgeStatus() error {
	if err := os.Remove(s.resultPath()); err != nil && !os.IsNotExist(err) {
		return domain.Wrap(domain.CodeInternal, err)
	}
	return nil
}

func (s *Service) RequestRestart() error {
	if _, ok := s.readPending(); !ok {
		return domain.Errorf(domain.CodeValidation, "当前没有等待重启的恢复")
	}
	if s.onRestart == nil {
		return domain.Errorf(domain.CodeNotImplement, "当前部署不支持程序内发起重启，请手动重启 MyPan")
	}
	s.onRestart()
	return nil
}

func (s *Service) backupPath(id string) string { return filepath.Join(s.backupsDir, id+".lpb") }
func (s *Service) pendingPath() string         { return filepath.Join(s.restoreDir, "restore.pending") }
func (s *Service) resultPath() string          { return filepath.Join(s.restoreDir, "restore.result.json") }

func (s *Service) readPending() (pendingPlan, bool) {
	var plan pendingPlan
	if err := readJSONFile(s.pendingPath(), &plan); err != nil {
		return pendingPlan{}, false
	}
	if plan.Version != 1 || !validRecordID(plan.ID) || plan.StageDir != plan.ID || !validRecordID(plan.SourceID) {
		return pendingPlan{}, false
	}
	return plan, true
}

func recordFromPath(path, id string) (Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return Record{}, err
	}
	manifest, _, headerErr := readBackupHeader(file)
	closeErr := file.Close()
	if headerErr != nil {
		return Record{}, headerErr
	}
	if closeErr != nil {
		return Record{}, closeErr
	}
	return recordFromManifest(path, id, manifest)
}

func recordFromManifest(path, id string, manifest Manifest) (Record, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID:            id,
		BackupID:      manifest.BackupID,
		AppVersion:    manifest.AppVersion,
		SchemaVersion: manifest.SchemaVersion,
		CreatedAt:     manifest.CreatedAt,
		Note:          manifest.Note,
		Scope:         manifest.Scope,
		Components:    append([]string(nil), manifest.Components...),
		AccountCount:  manifest.AccountCount,
		TaskCount:     manifest.TaskCount,
		Size:          info.Size(),
	}, nil
}

func filterSettingsConfigs(configs map[string]string) map[string]string {
	filtered := make(map[string]string, len(configs))
	for key, value := range configs {
		if _, excluded := neverRestoreConfigKeys[key]; excluded {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func validateCreateRequest(req CreateRequest) error {
	if len([]rune(strings.TrimSpace(req.Note))) > maxNoteLength {
		return domain.Errorf(domain.CodeValidation, "备份备注不能超过 %d 个字", maxNoteLength)
	}
	return validatePassword(req.Password)
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return domain.Errorf(domain.CodeValidation, "备份密码至少 8 位")
	}
	if len(password) > maxPasswordBytes {
		return domain.Errorf(domain.CodeValidation, "备份密码过长")
	}
	return nil
}

func validRecordID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == strings.ToLower(id)
}

func readSettingsPayload(path string) (settingsPayload, error) {
	file, err := os.Open(path)
	if err != nil {
		return settingsPayload{}, domain.Errorf(domain.CodeValidation, "设置备份载荷缺失")
	}
	defer file.Close()
	var payload settingsPayload
	dec := json.NewDecoder(io.LimitReader(file, 16*1024*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		return settingsPayload{}, domain.Errorf(domain.CodeValidation, "设置备份载荷无效：%v", err)
	}
	if payload.Version != settingsPayloadVersion || payload.Configs == nil || len(payload.Configs) > 1024 {
		return settingsPayload{}, domain.Errorf(domain.CodeValidation, "设置备份版本或数量无效")
	}
	for key, value := range payload.Configs {
		if key == "" || len(key) > 128 || len(value) > 8*1024*1024 {
			return settingsPayload{}, domain.Errorf(domain.CodeValidation, "设置备份包含非法配置项")
		}
	}
	return payload, nil
}

func readFavoritesOrEmpty(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{\"version\":1,\"accounts\":{}}\n"), nil
	}
	if err != nil {
		return nil, err
	}
	var data struct {
		Version  int                        `json:"version"`
		Accounts map[string]json.RawMessage `json:"accounts"`
	}
	if len(body) > 64*1024*1024 || json.Unmarshal(body, &data) != nil || data.Version <= 0 || data.Accounts == nil {
		return nil, fmt.Errorf("favorites data is invalid")
	}
	return body, nil
}

func copyLimitedFile(path string, source io.Reader, limit int64) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	n, err := io.Copy(out, io.LimitReader(source, limit+1))
	if err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	if n > limit {
		return domain.Errorf(domain.CodeValidation, "备份文件超过 %d MB 上限", limit/(1024*1024))
	}
	if err := out.Sync(); err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	if err := out.Close(); err != nil {
		return domain.Wrap(domain.CodeInternal, err)
	}
	ok = true
	return nil
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp-" + randomHex(6)
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readJSONFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 1024*1024))
	dec.DisallowUnknownFields()
	return dec.Decode(destination)
}
