package strm

import (
	"context"
	"errors"
	"testing"

	"litepan/internal/domain"
)

func TestPauseRunningTaskPersistsBeforeCancel(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "运行中", true: "请求已取消"}[alreadyCanceled], func(t *testing.T) {
			s, db := testService(t)
			ctx := context.Background()
			aid, err := db.Accounts.Create(ctx, &domain.Account{Name: "模拟账号", DriverType: "mock", Config: "{}", IsActive: true})
			if err != nil {
				t.Fatal(err)
			}
			id, err := db.StrmTasks.Create(ctx, &domain.StrmTask{Name: "模拟任务", AccountID: aid, Status: domain.StrmStatusRunning})
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			s.running[id] = true
			s.runningAccounts[aid] = struct{}{}
			s.taskCancels[id] = func() {
				st, err := db.StrmTasks.Get(ctx, id)
				if err != nil || st.Status != domain.StrmStatusPaused {
					t.Errorf("取消执行前应已保存暂停状态：%+v，%v", st, err)
				}
				cancel()
			}
			if err := s.PauseTask(runCtx, id, domain.PauseReasonAuthFailure, "认证失效"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(runCtx.Err(), context.Canceled) {
				t.Fatal("未取消执行")
			}
			// 模拟取消后的收尾，不能把暂停状态覆盖回正常。
			if err := s.finalizeScanPersist(id, scanPatchAfterRun(context.Canceled, ScanResult{})); err != nil {
				t.Fatal(err)
			}
			st, err := db.StrmTasks.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if st.Status != domain.StrmStatusPaused || st.PausedReason != string(domain.PauseReasonAuthFailure) || st.ErrorMessage != "认证失效" {
				t.Fatalf("暂停状态不正确：%+v", st)
			}
			if s.running[id] {
				t.Fatal("运行标记未清理")
			}
		})
	}
}
