package telegram

import "testing"

// 新认出来的分享域名：归一化后 Kind、指纹前缀、主域都要对。
//
// 样本都是**实测抓到的真实形态**（2026-10-01 抓 vip115hot 频道一页，
// 百度 28 条、迅雷 8 条、夸克 28 条、115 4 条）。
func TestNormalizeShareNewHosts(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantKind string
		wantHash string
		wantRaw  string
	}{
		{
			name:     "百度（pwd= 形态，实测最常见）",
			raw:      "https://pan.baidu.com/s/16plZjiZ-JxyQFdruAb65-w?pwd=Yu88",
			wantKind: ResourceKindShareBaidu,
			wantHash: "baidu:16plZjiZ-JxyQFdruAb65-w",
			wantRaw:  "https://pan.baidu.com/s/16plZjiZ-JxyQFdruAb65-w?password=Yu88",
		},
		{
			name:     "迅雷",
			raw:      "https://pan.xunlei.com/s/VP0NGiPRx5CjNtWw1dgm7RAZA1?pwd=u5xn",
			wantKind: ResourceKindShareXunlei,
			wantHash: "xunlei:VP0NGiPRx5CjNtWw1dgm7RAZA1",
			wantRaw:  "https://pan.xunlei.com/s/VP0NGiPRx5CjNtWw1dgm7RAZA1?password=u5xn",
		},
		{
			name:     "阿里云盘（新域 alipan.com）",
			raw:      "https://www.alipan.com/s/abc123XYZ",
			wantKind: ResourceKindShareAliyun,
			wantHash: "aliyun:abc123XYZ",
			wantRaw:  "https://www.alipan.com/s/abc123XYZ",
		},
		{
			name:     "阿里云盘（旧域 aliyundrive.com）",
			raw:      "https://www.aliyundrive.com/s/old123",
			wantKind: ResourceKindShareAliyun,
			wantHash: "aliyun:old123",
			wantRaw:  "https://www.alipan.com/s/old123",
		},
		{
			name:     "UC 网盘（不带 /s/ 段）",
			raw:      "https://drive.uc.cn/s/abcdef123456",
			wantKind: ResourceKindShareUC,
			wantHash: "uc:abcdef123456",
			wantRaw:  "https://drive.uc.cn/s/abcdef123456",
		},
		{
			name:     "123 网盘",
			raw:      "https://www.123pan.com/s/abcd-efgh",
			wantKind: ResourceKindShare123,
			wantHash: "123:abcd-efgh",
			wantRaw:  "https://www.123pan.com/s/abcd-efgh",
		},
		{
			name:     "天翼云盘",
			raw:      "https://cloud.189.cn/t/AbCdEf123",
			wantKind: ResourceKindShare189,
			wantHash: "189:AbCdEf123",
			wantRaw:  "https://cloud.189.cn/t/AbCdEf123",
		},
		{
			name:     "蓝奏云（按主机分配，且没有 /s/ 段）",
			raw:      "https://www.lanzoui.com/iAbCdEf",
			wantKind: ResourceKindShareLanzou,
			wantHash: "lanzou:iAbCdEf",
			wantRaw:  "https://www.lanzou.com/iAbCdEf",
		},
		{
			name:     "Google Drive",
			raw:      "https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQr/view?usp=sharing",
			wantKind: ResourceKindShareGDrive,
			wantHash: "gdrive:1AbCdEfGhIjKlMnOpQr",
			wantRaw:  "https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQr/view",
		},
		{
			// 1drv.ms 的短链是不可辨识的随机段，指纹退化成整条路径。
			name:     "OneDrive 短链",
			raw:      "https://1drv.ms/u/s!AbCdEfGhIjKlMn",
			wantKind: ResourceKindShareOneDrive,
			wantHash: "onedrive:u/s!AbCdEfGhIjKlMn",
			wantRaw:  "https://1drv.ms/u/s!AbCdEfGhIjKlMn",
		},
		{
			name:     "PikPak",
			raw:      "https://mypikpak.com/s/VNabcdefghijklm",
			wantKind: ResourceKindSharePikPak,
			wantHash: "pikpak:VNabcdefghijklm",
			wantRaw:  "https://mypikpak.com/s/VNabcdefghijklm",
		},
		{
			name:     "MEGA",
			raw:      "https://mega.nz/file/AbCdEfGh#keypartignored",
			wantKind: ResourceKindShareMega,
			wantHash: "mega:AbCdEfGh",
			wantRaw:  "https://mega.nz/file/AbCdEfGh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := normalizeShare(tc.raw, SourceText, "")
			if !ok {
				t.Fatalf("没认出来：%s", tc.raw)
			}
			if ref.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", ref.Kind, tc.wantKind)
			}
			if ref.InfoHash != tc.wantHash {
				t.Errorf("InfoHash = %q, want %q", ref.InfoHash, tc.wantHash)
			}
			if ref.Raw != tc.wantRaw {
				t.Errorf("Raw = %q, want %q", ref.Raw, tc.wantRaw)
			}
		})
	}
}

// 提取码写在正文里时也要拼进 URL（不只是 ?pwd= 参数）。
func TestNormalizeSharePasswordFromText(t *testing.T) {
	ref, ok := normalizeShare("https://pan.baidu.com/s/1abcDEF", SourceText, "Yu88")
	if !ok {
		t.Fatal("没认出来")
	}
	if ref.Raw != "https://pan.baidu.com/s/1abcDEF?password=Yu88" {
		t.Errorf("Raw = %q，正文里的提取码没拼上", ref.Raw)
	}
}

// 非分享域一律不认 —— 实测频道里 mooguu.net / 6gtg2.com 这类中转站最多。
func TestNormalizeShareRejectsThirdParty(t *testing.T) {
	for _, raw := range []string{
		"https://mooguu.net/abc",
		"https://www.6gtg2.com/x/1",
		"https://www.da000.vip/abc",
		// 形状对但域名不在白名单里。
		"https://not115.com/s/abc123",
		// 域对但路径不是分享页。
		"https://pan.baidu.com/disk/home",
	} {
		if ref, ok := normalizeShare(raw, SourceText, ""); ok {
			t.Errorf("%s 不该被认成分享：%+v", raw, ref)
		}
	}
}

// 新域名的指纹必须两两不相交 —— 它们共用一个唯一索引，
// 撞键会让不同的分享被当成同一份资源去重掉。
func TestShareFingerprintNamespacesDisjoint(t *testing.T) {
	// 同一段 share code 放在不同网盘下，指纹必须不同。
	const code = "abcd1234"
	seen := map[string]string{}
	for _, spec := range shareSpecs {
		hash := spec.prefix + code
		if prev, dup := seen[hash]; dup {
			t.Fatalf("%s 与 %s 的指纹撞了：%s", prev, spec.kind, hash)
		}
		seen[hash] = spec.kind
	}
}

// IsShareHost 要认全这些新域名（频道的「体检」靠它区分
// 「确实是分享链」与「第三方中转站」）。
func TestIsShareHostCoversNewDomains(t *testing.T) {
	yes := []string{
		"pan.baidu.com", "pan.xunlei.com", "www.alipan.com", "drive.uc.cn",
		"www.123pan.com", "cloud.189.cn", "mypikpak.com", "www.lanzou.com",
		"drive.google.com", "1drv.ms", "mega.nz",
	}
	for _, host := range yes {
		if !IsShareHost(host) {
			t.Errorf("IsShareHost(%q) = false", host)
		}
	}
	no := []string{"mooguu.net", "www.6gtg2.com", "t.me", "example.com"}
	for _, host := range no {
		if IsShareHost(host) {
			t.Errorf("IsShareHost(%q) = true，它不是分享域", host)
		}
	}
}
