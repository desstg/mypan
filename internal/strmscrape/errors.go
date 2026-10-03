package strmscrape

import (
	"errors"

	"litepan/internal/domain"
)

var errRootMetaIncomplete = errors.New("根目录缺少 nfo 或海报，无法设为完结")

// errJavWallSettingsUnavailable 表示服务没接设置（只在测试里出现）。
var errJavWallSettingsUnavailable = domain.Errorf(domain.CodeInternal, "设置服务不可用，无法保存番号墙隐藏目录")

// errNotTmdbTask 是 errNotJavTask 的对称面：TMDB 那套（扫盘 → 匹配 TMDB → 写
// nfo / 海报）只能用在 tmdb 影片任务上。
//
// 为什么必须有这道闸：番号任务的 nfo 与图片是**读本地侧车 json** 生成的
// （见 internal/strm 的 generateJavArtifacts）。拿 TMDB 那套去刮番号库，会把
// TMDB 匹配到的片名、简介、海报写进番号目录，**把用户已经整理好的番号元数据覆盖掉**。
// 之前这条路由前端按钮藏起来兜着（AuxToolsManagement.vue 的 isJavTask），
// 后端本身没有守卫 —— 直接打 API 就能触发，属于「静默写坏数据」。
var errNotTmdbTask = domain.Errorf(domain.CodeValidation, "该任务不是 tmdb 影片任务，番号影片请在海报墙上操作")
