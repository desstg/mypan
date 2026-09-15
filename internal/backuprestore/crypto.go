package backuprestore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

var backupMagic = [8]byte{'L', 'I', 'T', 'E', 'P', 'B', '0', '1'}

const (
	defaultChunkSize = 1024 * 1024
	maxHeaderSize    = 64 * 1024
	maxBackupSize    = int64(512 * 1024 * 1024)
	maxPlainSize     = int64(1024 * 1024 * 1024)
)

type aadManifest struct {
	FormatVersion int         `json:"format_version"`
	BackupID      string      `json:"backup_id"`
	AppVersion    string      `json:"app_version"`
	SchemaVersion int         `json:"schema_version"`
	CreatedAt     string      `json:"created_at"`
	Note          string      `json:"note,omitempty"`
	Scope         string      `json:"scope"`
	Components    []string    `json:"components"`
	KDF           KDFManifest `json:"kdf"`
	NoncePrefix   string      `json:"nonce_prefix"`
	ChunkSize     int         `json:"chunk_size"`
}

func (m Manifest) aad() ([]byte, error) {
	return json.Marshal(aadManifest{
		FormatVersion: m.FormatVersion,
		BackupID:      m.BackupID,
		AppVersion:    m.AppVersion,
		SchemaVersion: m.SchemaVersion,
		CreatedAt:     m.CreatedAt,
		Note:          m.Note,
		Scope:         m.Scope,
		Components:    m.Components,
		KDF:           m.KDF,
		NoncePrefix:   m.NoncePrefix,
		ChunkSize:     m.ChunkSize,
	})
}

func newCryptoManifest(m *Manifest) error {
	salt := make([]byte, 16)
	noncePrefix := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(noncePrefix); err != nil {
		return err
	}
	m.KDF = KDFManifest{
		Name:      "argon2id",
		Time:      2,
		MemoryKiB: 64 * 1024,
		Threads:   4,
		Salt:      base64.RawStdEncoding.EncodeToString(salt),
	}
	m.NoncePrefix = base64.RawStdEncoding.EncodeToString(noncePrefix)
	m.ChunkSize = defaultChunkSize
	return nil
}

func validateCryptoManifest(m Manifest) error {
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("不支持的备份格式版本：%d", m.FormatVersion)
	}
	if m.Scope != ScopeSettings && m.Scope != ScopeFull {
		return fmt.Errorf("备份范围无效")
	}
	if !validRecordID(m.BackupID) || strings.TrimSpace(m.AppVersion) == "" || m.SchemaVersion < 0 {
		return fmt.Errorf("备份基本信息无效")
	}
	if _, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err != nil || len([]rune(m.Note)) > maxNoteLength {
		return fmt.Errorf("备份时间或备注无效")
	}
	if len(m.Components) == 0 || len(m.Components) > 16 {
		return fmt.Errorf("备份组件清单无效")
	}
	if m.KDF.Name != "argon2id" || m.KDF.Time < 1 || m.KDF.Time > 10 || m.KDF.MemoryKiB < 8*1024 || m.KDF.MemoryKiB > 256*1024 || m.KDF.Threads < 1 || m.KDF.Threads > 16 {
		return fmt.Errorf("备份密钥派生参数无效")
	}
	salt, err := base64.RawStdEncoding.DecodeString(m.KDF.Salt)
	if err != nil || len(salt) != 16 {
		return fmt.Errorf("备份 salt 无效")
	}
	prefix, err := base64.RawStdEncoding.DecodeString(m.NoncePrefix)
	if err != nil || len(prefix) != 8 {
		return fmt.Errorf("备份 nonce 无效")
	}
	if m.ChunkSize < 64*1024 || m.ChunkSize > 4*1024*1024 {
		return fmt.Errorf("备份分块大小无效")
	}
	if m.PlainSize < 0 || m.PlainSize > maxPlainSize || m.EncryptedSize < 0 || m.EncryptedSize > maxBackupSize {
		return fmt.Errorf("备份文件过大")
	}
	if m.PayloadSHA256 != "" {
		if raw, err := hex.DecodeString(m.PayloadSHA256); err != nil || len(raw) != sha256.Size {
			return fmt.Errorf("备份校验值无效")
		}
	}
	return nil
}

func deriveKey(password string, m Manifest) ([]byte, error) {
	if err := validateCryptoManifest(m); err != nil {
		return nil, err
	}
	salt, _ := base64.RawStdEncoding.DecodeString(m.KDF.Salt)
	return argon2.IDKey([]byte(password), salt, m.KDF.Time, m.KDF.MemoryKiB, m.KDF.Threads, 32), nil
}

func makeGCM(password string, m Manifest) (cipher.AEAD, []byte, error) {
	key, err := deriveKey(password, m)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	aad, err := m.aad()
	if err != nil {
		return nil, nil, err
	}
	return gcm, aad, nil
}

func chunkNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], counter)
	return nonce
}

func chunkAAD(base []byte, counter uint32) []byte {
	out := make([]byte, len(base)+4)
	copy(out, base)
	binary.BigEndian.PutUint32(out[len(base):], counter)
	return out
}

func encryptPayload(payloadPath, bodyPath, password string, m *Manifest) error {
	if err := newCryptoManifest(m); err != nil {
		return err
	}
	gcm, aad, err := makeGCM(password, *m)
	if err != nil {
		return err
	}
	prefix, _ := base64.RawStdEncoding.DecodeString(m.NoncePrefix)
	src, err := os.Open(payloadPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(bodyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = dst.Close()
		if !ok {
			_ = os.Remove(bodyPath)
		}
	}()

	buf := make([]byte, m.ChunkSize)
	h := sha256.New()
	var plainSize, encryptedSize int64
	for counter := uint32(0); ; counter++ {
		n, readErr := io.ReadFull(src, buf)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if counter == ^uint32(0) {
			return fmt.Errorf("备份分块数过多")
		}
		sealed := gcm.Seal(nil, chunkNonce(prefix, counter), buf[:n], chunkAAD(aad, counter))
		var frame [4]byte
		binary.BigEndian.PutUint32(frame[:], uint32(len(sealed)))
		if _, err := dst.Write(frame[:]); err != nil {
			return err
		}
		if _, err := dst.Write(sealed); err != nil {
			return err
		}
		_, _ = h.Write(frame[:])
		_, _ = h.Write(sealed)
		plainSize += int64(n)
		encryptedSize += int64(len(frame) + len(sealed))
		if plainSize > maxPlainSize || encryptedSize > maxBackupSize {
			return fmt.Errorf("备份文件过大")
		}
		if errors.Is(readErr, io.ErrUnexpectedEOF) {
			break
		}
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	m.PlainSize = plainSize
	m.EncryptedSize = encryptedSize
	m.PayloadSHA256 = hex.EncodeToString(h.Sum(nil))
	ok = true
	return nil
}

func assembleBackup(destination, bodyPath string, m Manifest) error {
	header, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(header) > maxHeaderSize {
		return fmt.Errorf("备份头过大")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	if _, err := out.Write(backupMagic[:]); err != nil {
		return err
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(header)))
	if _, err := out.Write(size[:]); err != nil {
		return err
	}
	if _, err := out.Write(header); err != nil {
		return err
	}
	body, err := os.Open(bodyPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, body)
	closeErr := body.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func readBackupHeader(file *os.File) (Manifest, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, 0, err
	}
	magic := make([]byte, len(backupMagic))
	if _, err := io.ReadFull(file, magic); err != nil || !bytes.Equal(magic, backupMagic[:]) {
		return Manifest{}, 0, fmt.Errorf("不是 MyPan 备份文件")
	}
	var size [4]byte
	if _, err := io.ReadFull(file, size[:]); err != nil {
		return Manifest{}, 0, fmt.Errorf("备份头不完整")
	}
	headerSize := binary.BigEndian.Uint32(size[:])
	if headerSize == 0 || headerSize > maxHeaderSize {
		return Manifest{}, 0, fmt.Errorf("备份头大小无效")
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return Manifest{}, 0, fmt.Errorf("备份头不完整")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(header))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, 0, fmt.Errorf("备份头解析失败：%w", err)
	}
	if err := validateCryptoManifest(m); err != nil {
		return Manifest{}, 0, err
	}
	if m.PlainSize <= 0 || m.EncryptedSize <= 0 || m.PayloadSHA256 == "" {
		return Manifest{}, 0, fmt.Errorf("备份载荷信息无效")
	}
	return m, int64(len(backupMagic) + len(size) + len(header)), nil
}

func verifyEncryptedBody(file *os.File, bodyOffset int64, m Manifest) error {
	if _, err := file.Seek(bodyOffset, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, maxBackupSize+1))
	if err != nil {
		return err
	}
	if n != m.EncryptedSize || n > maxBackupSize {
		return fmt.Errorf("备份载荷大小不匹配")
	}
	if !equalHexDigest(h, m.PayloadSHA256) {
		return fmt.Errorf("备份载荷校验失败")
	}
	return nil
}

func equalHexDigest(h hash.Hash, expected string) bool {
	raw, err := hex.DecodeString(expected)
	return err == nil && bytes.Equal(h.Sum(nil), raw)
}

func decryptPayload(backupPath, destination, password string) (Manifest, error) {
	file, err := os.Open(backupPath)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	m, bodyOffset, err := readBackupHeader(file)
	if err != nil {
		return Manifest{}, err
	}
	if err := verifyEncryptedBody(file, bodyOffset, m); err != nil {
		return Manifest{}, err
	}
	gcm, aad, err := makeGCM(password, m)
	if err != nil {
		return Manifest{}, err
	}
	prefix, _ := base64.RawStdEncoding.DecodeString(m.NoncePrefix)
	if _, err := file.Seek(bodyOffset, io.SeekStart); err != nil {
		return Manifest{}, err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Manifest{}, err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(destination)
		}
	}()
	var plainSize int64
	for counter := uint32(0); plainSize < m.PlainSize; counter++ {
		var frame [4]byte
		if _, err := io.ReadFull(file, frame[:]); err != nil {
			return Manifest{}, fmt.Errorf("备份载荷不完整")
		}
		frameSize := int(binary.BigEndian.Uint32(frame[:]))
		if frameSize < gcm.Overhead() || frameSize > m.ChunkSize+gcm.Overhead() {
			return Manifest{}, fmt.Errorf("备份分块大小无效")
		}
		sealed := make([]byte, frameSize)
		if _, err := io.ReadFull(file, sealed); err != nil {
			return Manifest{}, fmt.Errorf("备份载荷不完整")
		}
		plain, err := gcm.Open(nil, chunkNonce(prefix, counter), sealed, chunkAAD(aad, counter))
		if err != nil {
			return Manifest{}, fmt.Errorf("密码错误或备份已损坏")
		}
		plainSize += int64(len(plain))
		if plainSize > m.PlainSize || plainSize > maxPlainSize {
			return Manifest{}, fmt.Errorf("备份解密大小异常")
		}
		if _, err := out.Write(plain); err != nil {
			return Manifest{}, err
		}
	}
	if plainSize != m.PlainSize {
		return Manifest{}, fmt.Errorf("备份解密大小不匹配")
	}
	if err := out.Sync(); err != nil {
		return Manifest{}, err
	}
	if err := out.Close(); err != nil {
		return Manifest{}, err
	}
	ok = true
	return m, nil
}
