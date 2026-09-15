package cloud189

import (
	"context"
	"net/http"
	"strings"

	"litepan/internal/domain"
)

const familyItemCacheLimit = 4096

func (d *Driver) isFamily() bool {
	return strings.EqualFold(strings.TrimSpace(d.add.SpaceType), "family")
}

func (d *Driver) currentFamilyID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.familyID
}

func (d *Driver) uploadSpaceKey() string {
	if d.isFamily() {
		return "family:" + d.currentFamilyID()
	}
	return "personal"
}

func (d *Driver) ensureFamilyID(ctx context.Context) error {
	if d.currentFamilyID() != "" {
		return nil
	}
	var resp familyListResp
	if err := d.apiRequest(ctx, http.MethodGet, apiURL+"/family/manage/getFamilyList.action", nil, &resp); err != nil {
		return err
	}
	if len(resp.FamilyInfoResp) == 0 {
		return domain.Errorf(domain.CodeValidation, "当前天翼账号没有可用的家庭云")
	}

	d.mu.Lock()
	loginName := d.loginName
	d.mu.Unlock()
	selected := ""
	for _, info := range resp.FamilyInfoResp {
		remark := strings.TrimSpace(info.RemarkName)
		if remark != "" && strings.Contains(loginName, remark) {
			selected = info.FamilyID.String()
			break
		}
	}
	if selected == "" {
		for _, info := range resp.FamilyInfoResp {
			if info.UseFlag != 0 {
				selected = info.FamilyID.String()
				break
			}
		}
	}
	if selected == "" {
		selected = resp.FamilyInfoResp[0].FamilyID.String()
	}
	if selected == "" {
		return domain.Errorf(domain.CodeDriverError, "天翼云盘未返回家庭云ID")
	}
	d.mu.Lock()
	d.familyID = selected
	d.mu.Unlock()
	return nil
}

func (d *Driver) rememberItems(items []domain.FileItem) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.itemCache == nil {
		d.itemCache = make(map[string]domain.FileItem)
	}
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, exists := d.itemCache[item.ID]; !exists {
			d.itemOrder = append(d.itemOrder, item.ID)
		}
		d.itemCache[item.ID] = item
	}
	for len(d.itemOrder) > familyItemCacheLimit {
		id := d.itemOrder[0]
		d.itemOrder = d.itemOrder[1:]
		delete(d.itemCache, id)
	}
}

func (d *Driver) cachedItem(id string) (domain.FileItem, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, ok := d.itemCache[id]
	return item, ok
}

func (d *Driver) forgetItems(ids []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range ids {
		delete(d.itemCache, id)
	}
}

func (d *Driver) findFamilyItem(ctx context.Context, fileID string) (*domain.FileItem, error) {
	queue := []string{d.rootID()}
	visited := map[string]struct{}{}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if _, ok := visited[parent]; ok {
			continue
		}
		visited[parent] = struct{}{}
		items, err := d.ListFiles(ctx, parent)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ID == fileID {
				found := item
				return &found, nil
			}
			if item.IsDir {
				queue = append(queue, item.ID)
			}
		}
	}
	return nil, domain.Errf(domain.CodeNotFound)
}
