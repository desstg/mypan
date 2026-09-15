package backuprestore

import "time"

const (
	FormatVersion = 1
	ScopeSettings = "settings"
	ScopeFull     = "full"

	StateIdle            = "idle"
	StateWaitingRestart  = "waiting_restart"
	StateRestoreSuccess  = "restore_success"
	StateRestoreRollback = "restore_rollback"
)

type KDFManifest struct {
	Name      string `json:"name"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	Salt      string `json:"salt"`
}

// Manifest 是 .lpb 公开头，不包含凭据和设置值。
type Manifest struct {
	FormatVersion int         `json:"format_version"`
	BackupID      string      `json:"backup_id"`
	AppVersion    string      `json:"app_version"`
	SchemaVersion int         `json:"schema_version"`
	CreatedAt     string      `json:"created_at"`
	Note          string      `json:"note,omitempty"`
	Scope         string      `json:"scope"`
	Components    []string    `json:"components"`
	AccountCount  int         `json:"account_count,omitempty"`
	TaskCount     int         `json:"task_count,omitempty"`
	KDF           KDFManifest `json:"kdf"`
	NoncePrefix   string      `json:"nonce_prefix"`
	ChunkSize     int         `json:"chunk_size"`
	PlainSize     int64       `json:"plain_size"`
	EncryptedSize int64       `json:"encrypted_size"`
	PayloadSHA256 string      `json:"payload_sha256"`
}

type Record struct {
	ID            string   `json:"id"`
	BackupID      string   `json:"backup_id"`
	AppVersion    string   `json:"app_version"`
	SchemaVersion int      `json:"schema_version"`
	CreatedAt     string   `json:"created_at"`
	Note          string   `json:"note"`
	Scope         string   `json:"scope"`
	Components    []string `json:"components"`
	AccountCount  int      `json:"account_count"`
	TaskCount     int      `json:"task_count"`
	Size          int64    `json:"size"`
}

type CreateRequest struct {
	Note            string `json:"note"`
	Password        string `json:"password"`
	IncludeAccounts bool   `json:"include_accounts"`
}

type RestoreRequest struct {
	Password     string `json:"password"`
	RestoreAdmin bool   `json:"restore_admin"`
}

type Summary struct {
	Record        Record `json:"record"`
	AccountCount  int    `json:"account_count"`
	TaskCount     int    `json:"task_count"`
	RestoreAdmin  bool   `json:"restore_admin"`
	NeedsRestart  bool   `json:"needs_restart"`
	SecretFromEnv bool   `json:"secret_from_env"`
}

type Status struct {
	State        string `json:"state"`
	Message      string `json:"message,omitempty"`
	BackupID     string `json:"backup_id,omitempty"`
	BackupNote   string `json:"backup_note,omitempty"`
	Scope        string `json:"scope,omitempty"`
	RestoreAdmin bool   `json:"restore_admin,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type payloadFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type payloadManifest struct {
	FormatVersion int           `json:"format_version"`
	Scope         string        `json:"scope"`
	SchemaVersion int           `json:"schema_version"`
	AccountCount  int           `json:"account_count"`
	TaskCount     int           `json:"task_count"`
	Files         []payloadFile `json:"files"`
}

type pendingPlan struct {
	Version       int    `json:"version"`
	ID            string `json:"id"`
	SourceID      string `json:"source_id"`
	BackupNote    string `json:"backup_note,omitempty"`
	Scope         string `json:"scope"`
	RestoreAdmin  bool   `json:"restore_admin"`
	StageDir      string `json:"stage_dir"`
	ReplaceSecret bool   `json:"replace_secret"`
	ReplaceFavs   bool   `json:"replace_favorites"`
	CreatedAt     string `json:"created_at"`
}

type restoreResult struct {
	State        string `json:"state"`
	Message      string `json:"message"`
	BackupID     string `json:"backup_id"`
	BackupNote   string `json:"backup_note,omitempty"`
	Scope        string `json:"scope"`
	RestoreAdmin bool   `json:"restore_admin"`
	UpdatedAt    string `json:"updated_at"`
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }
