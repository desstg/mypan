package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"litepan/internal/adminauth"
	"litepan/internal/config"
	"litepan/internal/logx"
	"litepan/pkg/security"

	// 驱动靠**空导入**注册（`drivers/all.go`）。生产入口在 cmd/litepan/main.go 里
	// 导入，测试二进制不会自动带上 —— 少了它，建账号会报「未知驱动类型：localfs」。
	_ "litepan/drivers"
)

// 真机联调：在**生产装配**（wireCore + wireServices + wireHTTPServer）上，
// 走真实 HTTP 把两条异步改造过的接口跑一遍。
//
// 默认跳过 —— 它要 LITEPAN_LIVE_DATA 指向一个含管理员账号的 data 目录。
// **不会碰那个目录**：库用内存库，STRM 输出目录用 t.TempDir()。
// 只有 `secret.key` 会从 data 目录读（会话签名要用它，否则伪造的 cookie 验不过）。
//
// 跑它（PowerShell）：
//
//	$env:LITEPAN_LIVE_DATA="E:\claude code\LitePan-main\data"
//	go test ./internal/app/ -run TestLiveAsyncEndpoints -v -timeout 10m
//
// 为什么非得真跑：这两条路最要紧的几件事单元测试看不见 ——
//   - 接口是不是**真的立刻返回**（502 的根因就是它同步等）
//   - 后台是不是**真的把活干完了**（.strm 有没有被换掉）
//   - 前端认的那个 `running` 字段有没有写进进度
func TestLiveAsyncEndpoints(t *testing.T) {
	if os.Getenv("LITEPAN_LIVE_DATA") == "" {
		t.Skip("未设置 LITEPAN_LIVE_DATA，跳过真机测试")
	}
	secretDir := os.Getenv("LITEPAN_LIVE_DATA")

	ctx := context.Background()
	logs, err := logx.New(logx.Options{Level: "warn", DisableFile: true})
	if err != nil {
		t.Fatalf("建日志失败：%v", err)
	}

	// 用自己建的临时目录，**不用 t.TempDir()**：FUSE 读缓存的 sqlite 会被后台
	// goroutine 按着不放，`t.TempDir()` 的清理会在收尾时 remove 失败，把整个用例
	// 判成 FAIL —— 明明断言全过了，却因为清理报红（2026-10-09 踩到）。
	//
	// 目录名**必须 ASCII**：LocalFs 驱动的 fileID 就是**绝对路径**，而这个库的
	// `file.Service` 在发请求前会对路径做一次合法化，非 ASCII 会被抹掉 → 执行器
	// 拿着 `listDir` 的结果找不到那个文件（真实执行里 fileID 是 115 那种纯数字，
	// 不会碰到这条）。踩过一次：临时目录默认带中文用户名，动作全判 failed。
	dataDir, err := os.MkdirTemp("", "litepan-live-async-")
	if err != nil {
		t.Fatalf("建临时目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	// secret.key 必须与真库同源 —— 否则我们签出来的 admin_session 验不过。
	if raw, err := os.ReadFile(filepath.Join(secretDir, "secret.key")); err == nil {
		if err := os.WriteFile(filepath.Join(dataDir, "secret.key"), raw, 0o600); err != nil {
			t.Fatalf("复制 secret.key: %v", err)
		}
	} else {
		t.Skipf("读不到 %s/secret.key：%v", secretDir, err)
	}

	cfg := config.Config{
		DataDir:    dataDir,
		DBPath:     filepath.Join(dataDir, "litepan.db"),
		StrmDir:    filepath.Join(dataDir, "strm"),
		ListenAddr: "127.0.0.1:0",
	}
	st, err := openStore(ctx, cfg, logs)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	t.Cleanup(func() { _ = st.db.Close() })

	// 临时库里种一个**哈希过**的管理员口令。
	//
	// 不种的话所有写接口都会被挡（「检测到管理员密码处于非安全状态」）——
	// 真库那份是历史明文口令，`adminauth` 会要求先改密码。这里不是为了绕过安全策略，
	// 而是让临时库处于「已经改过密码」的正常状态（用户在真机上也是这个状态）。
	//
	// ⚠️ 直接写 ConfigRepository 而不是 `settings.Update`：`admin_username` /
	// `admin_password` **不在设置项的注册表里**（走 `settings.Update` 会回
	// 「未知设置项」）—— 它们由 adminauth 自己读写。
	const verifyUser = "verify-admin"
	const verifyPass = "verify-pass-3f9a"
	if err := st.store.Configs.Set(ctx, adminauth.KeyAdminUsername, verifyUser); err != nil {
		t.Fatalf("种管理员用户名失败：%v", err)
	}
	if err := st.store.Configs.Set(ctx, adminauth.KeyAdminPassword, security.HashPassword(verifyPass)); err != nil {
		t.Fatalf("种管理员口令失败：%v", err)
	}

	core, err := wireCore(ctx, cfg, logs, st)
	if err != nil {
		t.Fatalf("wireCore: %v", err)
	}
	svc := wireServices(cfg, logs, st, core)
	server, err := wireHTTPServer(cfg, logs, st, core, svc, func() {})
	if err != nil {
		t.Fatalf("wireHTTPServer: %v", err)
	}
	ts := httptest.NewServer(server.Handler)
	t.Cleanup(ts.Close)
	t.Logf("生产装配已起：%s", ts.URL)

	// ---------- 会话：借 adminauth 的真 Session 类型自己签一个 cookie ----------
	//
	// 不走 /api/auth/login：那要先知道管理员密码，而且真库改过密码、副本里没有。
	// session 是「首 token = **同一把 secret** 的 HMAC」，所以拿 secret 本尊就能签出
	// 一份服务端会认的 cookie —— 真库的 `secret.key` 拷进临时 dataDir 就是为了这个。
	adminUser := st.settings.String("admin_username")
	if adminUser == "" {
		adminUser = "admin"
	}
	cookie := signAdminCookie(t, core.secret, adminUser)
	t.Logf("已用同一把 secret 签出 admin_session（user=%s）", adminUser)
	// ---------- 造一个 LocalFs 账号 + 任务 ----------
	lfRoot := filepath.Join(dataDir, "localfs-root")
	mustMkdir(t, filepath.Join(lfRoot, "movies"))
	for i := 0; i < 3; i++ {
		mustWrite(t, filepath.Join(lfRoot, "movies", fmt.Sprintf("Some.Movie.%d.1080p.H264.mkv", 2020+i)), []byte("\x00\x01\x02"))
	}

	// `config` 在 DTO 里是**字符串**（前端也是把 JSON 序列化成一个字符串发过来的）。
	lfConfig, _ := json.Marshal(map[string]any{"root_path": lfRoot})
	status, body := doJSON(t, ts.URL, "POST", "/api/admin/accounts", map[string]any{
		"name":        "verify-localfs",
		"driver_type": "localfs",
		"config":      string(lfConfig),
	}, cookie)
	if status != 200 {
		t.Fatalf("建 LocalFs 账号失败：%d %s", status, body)
	}
	var acc struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	mustUnmarshal(t, body, &acc)
	t.Logf("LocalFs 账号 id=%d", acc.Data.ID)

	// ⚠️ 账号**必须处于启用态**：`driver.Manager.get` 开头就是 `if !acc.IsActive` →
	// 「账号已停用」，于是**列目录也拿不到东西** —— 计划生成会静默变成「0 个动作」、
	// 执行也会静默跳过，看起来像功能坏了。踩过一轮（执行全 failed 却看不出原因）。
	// 临时库里新账号本来默认启用，这里显式确认一次，免得将来默认值变了又中招。
	accRow, err := st.store.Accounts.Get(ctx, acc.Data.ID)
	if err != nil {
		t.Fatalf("读回 LocalFs 账号失败：%v", err)
	}
	if !accRow.IsActive {
		accRow.IsActive = true
		if err := st.store.Accounts.Update(ctx, accRow); err != nil {
			t.Fatalf("启用 LocalFs 账号失败：%v", err)
		}
	}
	t.Logf("LocalFs 账号已确认启用（is_active=%v）", accRow.IsActive)

	// ---------- 【被测 1】一键替换 base-url 必须立刻返回 ----------
	strmOut := filepath.Join(cfg.StrmDir, "verify-out")
	mustMkdir(t, strmOut)
	oldBase := "http://127.0.0.1:5211"
	newBase := "http://192.168.31.9:5214"
	for i := 0; i < 30; i++ {
		mustWrite(t, filepath.Join(strmOut, fmt.Sprintf("ep%02d.strm", i)),
			[]byte(fmt.Sprintf("%s/api/strm/play/1/abc%d/t/tok/n/ep%02d.mkv\n", oldBase, i, i)))
	}

	t0 := time.Now()
	status, body = doJSON(t, ts.URL, "POST", "/api/admin/strm/replace-base-url",
		map[string]any{"new_base_url": newBase}, cookie)
	elapsed := time.Since(t0)
	t.Logf("replace-base-url → %d，耗时 %v，body=%s", status, elapsed, truncate(body, 200))
	if status != 200 {
		t.Fatalf("replace-base-url 失败：%d %s", status, body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("replace-base-url 没有立刻返回（%v）—— 又变回同步跑了", elapsed)
	}

	// 进度接口要能读到 running（前端据此显示「正在替换…」）。
	var progRaw map[string]any
	status, body = doJSON(t, ts.URL, "GET", "/api/admin/strm/replace-base-url/progress", nil, cookie)
	t.Logf("替换进度 → %d %s", status, truncate(body, 260))
	if status == 200 {
		mustUnmarshal(t, body, &progRaw)
		if _, ok := progRaw["data"].(map[string]any)["running"]; !ok {
			t.Errorf("进度里没有 running 字段：%s", truncate(body, 200))
		}
	}

	// 后台要真的把文件换掉。
	replaced := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(strmOut, "ep00.strm")); err == nil {
			if strings.Contains(string(raw), newBase) {
				replaced = true
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	sample := ""
	if raw, err := os.ReadFile(filepath.Join(strmOut, "ep00.strm")); err == nil {
		sample = strings.TrimSpace(strings.Split(string(raw), "\n")[0])
	}
	if !replaced {
		t.Errorf("后台没把 .strm 换掉；ep00 现在是 %s", sample)
	} else {
		t.Logf("后台已替换：ep00 = %s", sample)
	}

	// ---------- 【被测 2】整理计划必须立刻返回 ----------
	status, body = doJSON(t, ts.URL, "POST", "/api/admin/media-organize/tasks", map[string]any{
		"task_name":           "verify-organize",
		"account_id":          acc.Data.ID,
		"action_type":         "rename",
		"rename_marker":       "off",
		"target_directory":    "/movies",
		"target_directory_id": "movies",
		"target_root":         "/movies",
		"target_root_id":      "movies",
		"media_type":          "auto",
		"recursive":           true,
	}, cookie)
	if status != 200 {
		t.Fatalf("建整理任务失败：%d %s", status, body)
	}
	var moTask struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	mustUnmarshal(t, body, &moTask)
	moID := moTask.Data.ID
	t.Logf("整理任务 id=%s", moID)

	t0 = time.Now()
	status, body = doJSON(t, ts.URL, "POST", fmt.Sprintf("/api/admin/media-organize/tasks/%s/plan", moID), map[string]any{}, cookie)
	elapsed = time.Since(t0)
	t.Logf("/plan → %d，耗时 %v，body=%s", status, elapsed, truncate(body, 200))
	if status != 200 {
		t.Fatalf("/plan 失败：%d %s", status, body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("/plan 没有立刻返回（%v）—— 又变回同步跑了（反代会 502）", elapsed)
	}

	// 进度：running=true 起步，最终落回 false（前端只认这个字段）。
	sawRunning := false
	finished := false
	var lastProgress map[string]any
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, body = doJSON(t, ts.URL, "GET", fmt.Sprintf("/api/admin/media-organize/tasks/%s/progress", moID), nil, cookie)
		if status == 200 {
			var wrap struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal([]byte(body), &wrap); err == nil {
				lastProgress = wrap.Data
				if lastProgress["running"] == true {
					sawRunning = true
				}
				if lastProgress["running"] == false {
					finished = true
					break
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("进度收尾：running=%v stage=%v actions=%v err=%v（中途见过 running=true：%v）",
		lastProgress["running"], lastProgress["stage"], lastProgress["actions"], lastProgress["error"], sawRunning)
	if !finished {
		t.Errorf("进度里的 running 一直没落回 false：%v", lastProgress)
	}
	if _, ok := lastProgress["running"]; !ok {
		t.Errorf("进度里没有 running 字段：%v", lastProgress)
	}

	// 计划本体要拿得到（异步之后前端就是靠它拿结果的）。
	status, body = doJSON(t, ts.URL, "GET", fmt.Sprintf("/api/admin/media-organize/tasks/%s/plan", moID), nil, cookie)
	if status != 200 {
		t.Errorf("GET /plan 失败：%d %s", status, body)
	} else {
		var planWrap struct {
			Data struct {
				Actions []map[string]any `json:"actions"`
			} `json:"data"`
		}
		mustUnmarshal(t, body, &planWrap)
		t.Logf("GET /plan → %d，actions=%d", status, len(planWrap.Data.Actions))
	}

	// ---------- 【被测 3】整理**执行**也必须立刻返回 ----------
	//
	// 执行早就是后台跑的（`startRunner`），所以「任务会不会半路夭折」不是这里要验的。
	// 要验的是**接口立刻返回**：以前它同步等到底，前端 90 秒超时后弹「执行失败」——
	// 把正在跑的任务说死了，而真实结果在 `LastRunResult` 里，得用户自己去翻日志。
	//
	// 计划是**手写**的（不经生成）：生成出来的动作指向哪个目录不好控制，而这里要
	// 验的是「执行这条链路」，一个确定性的改名动作最干净。
	//
	// ⚠️ **`target_parent_id` 必须写**：执行器只认它（`resolveRef(action.TargetParentID)`），
	// 留空的话动作会带着「目标父目录未解析」直接判 failed —— 而且这条**不进日志正文**，
	// 只在 `last_run_result.failed` 上体现，排查时很容易误判成「改名本身坏了」。
	//
	// ⚠️ **用 `ensure_dir` 而不是 `relocate`**：`relocate` 会先过一遍
	// `execOverwriteDeletions`，而它按 `_overwrite_target_id` 删文件 —— 真实执行里
	// 这个键由 `prescanConflicts` 按「目标同名」填上，但我们手写的计划没有它。
	// （第一版就是拿 relocate 试的，后台真去调了删除，报「账号已停用」—— 那是
	// 这个账号确实被停用了，不是代码问题。换成 ensure_dir 后这条链路干净得多：
	// 它只调 CreateFolder，确定性最好。）
	//
	// 顺带一提：`CreateFolder` 走的**不是** file.Service 那条「账号必须启用」的闸，
	// 所以停用账号只影响移动/删除这类走 exec 的操作。
	execDir := filepath.Join(lfRoot, "movies")
	newDirName := "NewFolder-verify"
	execPlan := map[string]any{
		"task_id": moID,
		"actions": []map[string]any{{
			"id":               "a1",
			"kind":             "ensure_dir",
			"source_parent_id": execDir,
			"target_parent_id": execDir,
			"target_name":      newDirName,
			"status":           "pending",
		}},
		"skipped": []map[string]any{},
	}
	planBytes, _ := json.Marshal(execPlan)
	mustWrite(t, filepath.Join(dataDir, "media_organize_plans", moID+".json"), planBytes)

	t0 = time.Now()
	status, body = doJSON(t, ts.URL, "POST", fmt.Sprintf("/api/admin/media-organize/tasks/%s/apply", moID), map[string]any{}, cookie)
	elapsed = time.Since(t0)
	t.Logf("/apply → %d，耗时 %v，body=%s", status, elapsed, truncate(body, 200))
	if status != 200 {
		t.Fatalf("/apply 失败：%d %s", status, body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("/apply 没有立刻返回（%v）—— 又变回同步等跑完了（前端 90 秒超时会报假失败）", elapsed)
	}

	// 后台要真的把动作执行掉：目录被建出来。
	created := false
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(filepath.Join(execDir, newDirName)); err == nil && info.IsDir() {
			created = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !created {
		t.Errorf("后台没执行动作：%s 没被建出来", filepath.Join(execDir, newDirName))
	} else {
		t.Logf("后台已执行：建出目录 %s", newDirName)
	}

	// 结果要落进 LastRunResult（前端从日志面板读它），状态落回 idle。
	var doneTask map[string]any
	status, body = doJSON(t, ts.URL, "GET", fmt.Sprintf("/api/admin/media-organize/tasks/%s/logs", moID), nil, cookie)
	if status == 200 {
		mustUnmarshal(t, body, &doneTask)
		wrap, _ := doneTask["data"].(map[string]any)
		t.Logf("logs → status=%v last_run_result=%v", wrap["status"], truncate(fmt.Sprintf("%v", wrap["last_run_result"]), 160))
		if wrap["status"] != "idle" {
			t.Errorf("执行收尾后状态应当是 idle，got %v", wrap["status"])
		}
		if wrap["last_run_result"] == nil {
			t.Errorf("执行结果应当写进 LastRunResult：%v", wrap)
		}
	} else {
		t.Errorf("读 logs 失败：%d %s", status, body)
	}
}

// signAdminCookie 用给的服务 secret 签一份 adminauth 认的会话 cookie。
//
// 复用 adminauth 的真类型与真 serializer，而不是自己拼 JSON —— 会话结构里任何
// 字段改名（比如以后加 `generation`）都会让自制的 cookie 悄悄失效，而用真类型
// 编译期就会拦住。
func signAdminCookie(t *testing.T, secret []byte, username string) string {
	t.Helper()
	raw, err := json.Marshal(adminauth.Session{
		IsAdmin:   true,
		Username:  username,
		CreatedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	token, err := security.NewTimedSerializer(secret).Dumps(string(raw))
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	return "admin_session=" + token
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustUnmarshal(t *testing.T, body string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("解析响应失败：%v\n%s", err, truncate(body, 300))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func doJSON(t *testing.T, base, method, path string, payload any, cookie string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		rdr = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		t.Fatalf("建请求失败：%v", err)
	}
	req.Header.Set("Origin", base)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

var _ = slog.Default
