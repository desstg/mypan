package strm

import "testing"

func TestTaskFileOperationLock(t *testing.T) {
	svc := NewService(ServiceOptions{})
	release, ok := svc.TryBeginTaskFileOperation(7)
	if !ok || !svc.IsTaskFileOperationBusy(7) {
		t.Fatal("首次加锁应成功并显示任务忙碌")
	}
	if _, ok := svc.TryBeginTaskFileOperation(7); ok {
		t.Fatal("同一任务不应同时执行扫描、当前目录生成或刮削")
	}
	if _, ok := svc.TryBeginTaskFileOperation(8); !ok {
		t.Fatal("不同任务可以独立加锁")
	}

	release()
	release()
	if svc.IsTaskFileOperationBusy(7) {
		t.Fatal("释放后任务不应继续显示忙碌")
	}
	if releaseAgain, ok := svc.TryBeginTaskFileOperation(7); !ok {
		t.Fatal("释放后应允许再次加锁")
	} else {
		releaseAgain()
	}
}
