package tgsubscribe

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/offlinedownload"
)

func TestKindSchemes(t *testing.T) {
	cases := map[string][]string{
		KindMagnet:     {"magnet"},
		KindED2K:       {"ed2k"},
		KindHTTP:       {"http", "https"},
		KindShare115:   nil,
		KindShareQuark: nil,
		"":             nil,
		"unknown":      nil,
	}
	for kind, want := range cases {
		got := kindSchemes(kind)
		if len(got) != len(want) {
			t.Errorf("kindSchemes(%q) = %v, want %v", kind, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("kindSchemes(%q) = %v, want %v", kind, got, want)
				break
			}
		}
	}
}

// 各网盘声明的 scheme 都是实测值（drivers/*/offline_download.go 的能力声明）。
func capsFor(schemes []string) offlinedownload.Capabilities {
	return offlinedownload.Capabilities{
		Supported:           true,
		SupportsURLs:        true,
		URLSchemes:          schemes,
		BuiltinEnabled:      true,
		BuiltinSupportsURLs: true,
		BuiltinURLSchemes:   []string{"http", "https", "magnet"},
	}
}

var (
	// 115：http/https/ftp/magnet/ed2k，且有内置下载器兜底。
	caps115 = capsFor([]string{"http", "https", "ftp", "magnet", "ed2k"})
	// 光鸭：有 magnet 与直链，**没有 ed2k**。
	capsGuangya = capsFor([]string{"http", "https", "ftp", "thunder", "magnet"})
	// 123：只有直链，没有磁力，也没有 ed2k。
	caps123 = capsFor([]string{"http", "https"})
	// 账号被删 / 驱动异常：拿不到任何能力。
	capsEmpty = offlinedownload.Capabilities{}
)

func TestChooseProviderAuto(t *testing.T) {
	cases := []struct {
		name     string
		caps     offlinedownload.Capabilities
		kind     string
		wantProv string
		wantOK   bool
	}{
		{"115 磁力走原生", caps115, KindMagnet, offlinedownload.ProviderNative, true},
		{"115 ed2k 走原生", caps115, KindED2K, offlinedownload.ProviderNative, true},
		{"115 直链走原生", caps115, KindHTTP, offlinedownload.ProviderNative, true},
		// 光鸭没有 ed2k，内置也不吃 —— 明确不可投，而不是失败后重试 5 次。
		{"光鸭 ed2k 不可投", capsGuangya, KindED2K, "", false},
		{"光鸭 磁力走原生", capsGuangya, KindMagnet, offlinedownload.ProviderNative, true},
		// 123 没有磁力 → 降级到内置。
		{"123 磁力降级内置", caps123, KindMagnet, offlinedownload.ProviderBuiltin, true},
		{"123 ed2k 不可投", caps123, KindED2K, "", false},
		{"123 直链走原生", caps123, KindHTTP, offlinedownload.ProviderNative, true},
		{"无能力 ed2k 不可投", capsEmpty, KindED2K, "", false},
		{"分享链不走离线通道", caps115, KindShare115, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _, err := chooseProvider(tc.caps, "", tc.kind)
			if tc.wantOK && err != nil {
				t.Fatalf("应当可投递，却报错: %v", err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatalf("应当不可投递，却选了 %q", prov)
				}
				return
			}
			if prov != tc.wantProv {
				t.Errorf("通道 = %q, want %q", prov, tc.wantProv)
			}
		})
	}
}

// 降级文案只在真的降级时出现，且要带上资源类型的中文名。
func TestChooseProviderDegradeReason(t *testing.T) {
	prov, reason, err := chooseProvider(caps123, "", KindMagnet)
	if err != nil || prov != offlinedownload.ProviderBuiltin {
		t.Fatalf("prov=%q err=%v", prov, err)
	}
	if !strings.Contains(reason, "磁力链接") {
		t.Errorf("降级文案应带类型名，得到 %q", reason)
	}

	// 走原生时不该有降级文案 —— 早先这条文案在 Deliver 里又写了一遍，
	// 结果任何走内置的推送都带上「网盘不支持磁力」，即使并没有降级。
	if _, reason, _ := chooseProvider(caps115, "", KindMagnet); reason != "" {
		t.Errorf("走原生时不该有降级文案，得到 %q", reason)
	}
}

// 这是「修正 3」的核心：显式 pin 了通道时**也必须**过一遍能力校验。
//
// 早先的实现在 pin 时直接返回，于是「pin native + 光鸭账号 + ed2k」被判成可投递，
// 然后在 AddURLs 撞 scheme 白名单失败、退避重试 5 次 —— 正是要消灭的无效重试。
func TestChooseProviderPinnedStillValidates(t *testing.T) {
	cases := []struct {
		name     string
		caps     offlinedownload.Capabilities
		pinned   string
		kind     string
		wantErr  bool
		wantProv string
	}{
		{"pin native + 支持", caps115, offlinedownload.ProviderNative, KindED2K, false, offlinedownload.ProviderNative},
		{"pin native + 光鸭不支持 ed2k", capsGuangya, offlinedownload.ProviderNative, KindED2K, true, ""},
		{"pin native + 123 不支持磁力", caps123, offlinedownload.ProviderNative, KindMagnet, true, ""},
		{"pin builtin + 磁力", capsAny(), offlinedownload.ProviderBuiltin, KindMagnet, false, offlinedownload.ProviderBuiltin},
		// 内置下载器不吃 ed2k，pin 了也不行。
		{"pin builtin + ed2k", capsAny(), offlinedownload.ProviderBuiltin, KindED2K, true, ""},
		{"pin builtin + 直链", capsAny(), offlinedownload.ProviderBuiltin, KindHTTP, false, offlinedownload.ProviderBuiltin},
		{"pin native + 无能力", capsEmpty, offlinedownload.ProviderNative, KindMagnet, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov, _, err := chooseProvider(tc.caps, tc.pinned, tc.kind)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应当报错，却选了 %q", prov)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if prov != tc.wantProv {
				t.Errorf("通道 = %q, want %q", prov, tc.wantProv)
			}
		})
	}
}

func capsAny() offlinedownload.Capabilities {
	return capsFor([]string{"http", "https", "magnet", "ed2k", "ftp"})
}

// 报错文案要能指导操作，不能只说「不支持」。
func TestChooseProviderErrorIsActionable(t *testing.T) {
	_, _, err := chooseProvider(capsGuangya, offlinedownload.ProviderNative, KindED2K)
	if err == nil {
		t.Fatal("应当报错")
	}
	msg := err.Error()
	for _, want := range []string{"ed2k", "自动"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错文案缺少 %q：%s", want, msg)
		}
	}
}

func newDeliverableServiceForTest() *Service {
	s := &Service{}
	s.pusher = &Pusher{svc: s}
	s.deliverers = []Deliverer{s.pusher}
	return s
}

func TestDeliverabilityFor(t *testing.T) {
	ctx := context.Background()
	sub := &domain.TGSubscription{}

	t.Run("静态不支持的类型直接判否", func(t *testing.T) {
		s := newDeliverableServiceForTest()
		// 本轮没有分享转存投递器，delivererFor 返回 nil。
		_, reason, ok := s.deliverabilityFor(ctx, 1, sub, KindShare115)
		if ok {
			t.Fatal("分享链在没有投递器时应判为不可投递")
		}
		if !strings.Contains(reason, "115 分享") {
			t.Errorf("原因应说明类型，得到 %q", reason)
		}
	})

	t.Run("离线服务不可用时一律放行", func(t *testing.T) {
		s := newDeliverableServiceForTest()
		if _, _, ok := s.deliverabilityFor(ctx, 1, sub, KindED2K); !ok {
			t.Fatal("探测不了就应放行，交给推送去报精确错误")
		}
	})

	t.Run("没有投递器的服务判否", func(t *testing.T) {
		s := &Service{}
		if _, _, ok := s.deliverabilityFor(ctx, 1, sub, KindMagnet); ok {
			t.Fatal("没注册投递器时应判否")
		}
	})
}

// 空 Kind 要按磁力处理 —— 迁移前的旧行没有 resource_kind，
// 直接当空类型会让它们被误判成「没有投递器」。
func TestRecordKindDefaultsToMagnet(t *testing.T) {
	if got := recordKind(&domain.TGMatchRecord{}); got != KindMagnet {
		t.Errorf("空 Kind 应折算成磁力，得到 %q", got)
	}
	if got := recordKind(nil); got != KindMagnet {
		t.Errorf("nil 记录应折算成磁力，得到 %q", got)
	}
	if got := recordKind(&domain.TGMatchRecord{ResourceKind: KindED2K}); got != KindED2K {
		t.Errorf("显式 Kind 应原样返回，得到 %q", got)
	}
}

func TestResourceFromRecordCarriesKind(t *testing.T) {
	res := resourceFromRecord(&domain.TGMatchRecord{
		ResourceKind: KindED2K,
		Magnet:       "ed2k://|file|Movie.mkv|100|4d517deece354c11fe7e497999956663|/",
		MagnetHash:   "ed2k:4d517deece354c11fe7e497999956663",
		RawName:      "Movie.mkv",
	})
	if res.Kind != KindED2K {
		t.Errorf("Kind = %q, want %q", res.Kind, KindED2K)
	}
	if res.InfoHash != "ed2k:4d517deece354c11fe7e497999956663" {
		t.Errorf("InfoHash = %q", res.InfoHash)
	}
}
