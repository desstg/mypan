// Package mutation 标记「这次文件变更是不是本程序自己写出来的」。
//
// # 为什么要有它
//
// STRM 任务的开跑判据里有一条「账号脏了就跑」（`strm.Service.shouldRun` 里那个
// `dirtyAccounts`）。它本来是为「用户往网盘里放了东西，赶紧生成 .strm」设计的，
// 判据是 `OnFileMutated` 收到任何非 move 的 FileMutated 就标脏。
//
// 但那个事件有两个来源：**用户的操作**（上传、新建目录、删除）与本程序自己的
// 写盘（番号侧车回写、推送落盘、转存）。后者会在扫描刚跑完之后立刻把账号重新标脏，
// 于是下一轮 30 秒的调度 tick 又把它扫一遍 —— 而这次扫描看到的正是它自己刚写的那些
// 文件，扫完不出任何变更，白白打一遍网盘接口。
//
// # 三条通道（同一个文件，三个包各自调用）
//
// 谁写入谁标记，不接受「靠某个 id 是 0 来推断」这类隐式判据：
//
//	strm   —— 元数据同步上传（internal/strm/metadata_reconcile.go）
//	jav    —— 侧车回写（internal/jav/sidecar.go）与推送建目录
//	tgsubscribe —— 投递落盘（internal/tgsubscribe/pusher.go）
//
// # 只挡「标脏」，不挡「缓存失效」
//
// `strm.OnFileMutated` 开头那句 `invalidateMutatedDirCache` 仍然照常跑 ——
// 那是这个事件原本存在的理由（目录改名/移动后让路径映射失效），跟这次标记无关。
// 标脏只是它顺带做的一件事，也正是要挡的那件。
//
// ⚠️ 别把这里做成「没有标记就当用户操作」之外的别的语义：默认（没标记）必须等于
// 旧行为，否则会在别处静默改变扫描节奏。
package mutation

import "context"

// internalKey 是私有类型，避免与其它包塞进同一个 ctx 的键撞车。
type internalKey struct{}

// Internal 标记「这次文件变更由本程序自己发起」，返回派生后的 ctx。
//
// 用法：把它交给真正执行写盘的那个调用（`files.UploadLocal(ctx, ...)` 等），
// 而不是在后台 goroutine 的入口 —— 标记只在这次调用里有效。
func Internal(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, true)
}

// IsInternal 报告这次变更是否带了上面的标记。
//
// ⚠️ **默认必须是 false**（没标记 = 当用户操作），这样第三方驱动、手工调用的
// 写路径、以及将来新增的写入口都会自动退回旧行为：该标脏就标脏。
func IsInternal(ctx context.Context) bool {
	marked, _ := ctx.Value(internalKey{}).(bool)
	return marked
}
