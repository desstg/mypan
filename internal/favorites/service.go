package favorites

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const fileName = "litepan_favorites.json"

type Crumb struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Item struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Crumbs []Crumb `json:"crumbs"`
}

type AccountState struct {
	Open  bool   `json:"open"`
	Items []Item `json:"items"`
}

type snapshot struct {
	Version  int                     `json:"version"`
	Accounts map[string]AccountState `json:"accounts"`
}

type Service struct {
	path string
	log  *slog.Logger
	mu   sync.Mutex
}

func NewService(dbPath string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		path: filepath.Join(filepath.Dir(dbPath), fileName),
		log:  log,
	}
}

func (s *Service) Get(ctx context.Context, accountID int64) (AccountState, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readUnlocked()
	if err != nil {
		return AccountState{}, err
	}
	return cloneAccountState(data.Accounts[accountKey(accountID)]), nil
}

func (s *Service) Put(ctx context.Context, accountID int64, state AccountState) (AccountState, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readUnlocked()
	if err != nil {
		return AccountState{}, err
	}
	clean := sanitizeAccountState(state)
	data.Accounts[accountKey(accountID)] = clean
	if err := s.writeUnlocked(data); err != nil {
		return AccountState{}, err
	}
	return cloneAccountState(clean), nil
}

// Delete 删除指定账号的全部收藏；账号无收藏时不改写文件。
func (s *Service) Delete(ctx context.Context, accountID int64) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.readUnlocked()
	if err != nil {
		return err
	}
	key := accountKey(accountID)
	if _, ok := data.Accounts[key]; !ok {
		return nil
	}
	delete(data.Accounts, key)
	return s.writeUnlocked(data)
}

func (s *Service) readUnlocked() (snapshot, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot{Version: 1, Accounts: map[string]AccountState{}}, nil
		}
		return snapshot{}, fmt.Errorf("read favorites file: %w", err)
	}

	var data snapshot
	if err := json.Unmarshal(raw, &data); err != nil {
		backupPath, moveErr := s.moveCorruptedFileUnlocked()
		if moveErr != nil {
			s.log.Error("收藏夹文件解析失败，转移损坏文件失败", "path", s.path, "err", err, "move_err", moveErr)
			return snapshot{}, fmt.Errorf("favorites file corrupted: %w; move corrupted file: %v", err, moveErr)
		}
		s.log.Error("收藏夹文件解析失败，已转移损坏文件", "path", s.path, "backup", backupPath, "err", err)
		return snapshot{}, fmt.Errorf("收藏夹文件已损坏，已转移到 %s，请检查或手动恢复", backupPath)
	}
	if data.Version <= 0 {
		data.Version = 1
	}
	if data.Accounts == nil {
		data.Accounts = map[string]AccountState{}
	}
	for key, state := range data.Accounts {
		data.Accounts[key] = sanitizeAccountState(state)
	}
	return data, nil
}

func (s *Service) moveCorruptedFileUnlocked() (string, error) {
	base := s.path + ".corrupt-" + time.Now().Format("20060102-150405")
	target := base
	for i := 1; ; i++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		target = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.Rename(s.path, target); err != nil {
		return "", fmt.Errorf("rename corrupted favorites file: %w", err)
	}
	return target, nil
}

func (s *Service) writeUnlocked(data snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create favorites dir: %w", err)
	}
	data.Version = 1
	if data.Accounts == nil {
		data.Accounts = map[string]AccountState{}
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal favorites: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write favorites tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename favorites file: %w", err)
	}
	return nil
}

func sanitizeAccountState(state AccountState) AccountState {
	out := AccountState{
		Open:  state.Open,
		Items: make([]Item, 0, len(state.Items)),
	}
	seen := make(map[string]struct{}, len(state.Items))
	for _, item := range state.Items {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		if id == "" || name == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		crumbs := make([]Crumb, 0, len(item.Crumbs))
		for _, crumb := range item.Crumbs {
			crumbName := strings.TrimSpace(crumb.Name)
			if crumbName == "" {
				continue
			}
			crumbs = append(crumbs, Crumb{
				ID:   strings.TrimSpace(crumb.ID),
				Name: crumbName,
			})
		}
		if len(crumbs) == 0 {
			continue
		}
		out.Items = append(out.Items, Item{
			ID:     id,
			Name:   name,
			Crumbs: crumbs,
		})
		seen[id] = struct{}{}
	}
	return out
}

func cloneAccountState(state AccountState) AccountState {
	out := AccountState{
		Open:  state.Open,
		Items: make([]Item, 0, len(state.Items)),
	}
	for _, item := range state.Items {
		cloned := Item{
			ID:     item.ID,
			Name:   item.Name,
			Crumbs: make([]Crumb, len(item.Crumbs)),
		}
		copy(cloned.Crumbs, item.Crumbs)
		out.Items = append(out.Items, cloned)
	}
	return out
}

func accountKey(accountID int64) string {
	return strconv.FormatInt(accountID, 10)
}
