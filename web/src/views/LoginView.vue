<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import AnimatedCharacters from "@/components/AnimatedCharacters.vue";
import { fetchAuthStatus, login, resetPassword } from "@/api/auth";
import { useAuthStore } from "@/stores/auth";
import { confirm, showConfirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";

const router = useRouter();
const route = useRoute();
const auth = useAuthStore();

const loading = ref(false);
const showPassword = ref(false);
const isTyping = ref(false);
const passwordFocused = ref(false);
const passwordValue = ref("");
const errors = reactive({ username: "", password: "" });

const loginData = reactive({
  username: "",
  password: "",
  remember: false,
});

const isPasswordGuardMode = computed(() => passwordFocused.value);

watch(
  () => loginData.password,
  (val) => {
    passwordValue.value = val || "";
  },
);

function validate(): boolean {
  errors.username = loginData.username.trim() ? "" : "请输入用户名";
  errors.password = loginData.password ? "" : "请输入密码";
  return !errors.username && !errors.password;
}

function focusPassword() {
  document.getElementById("login-password")?.focus();
}

async function handleLogin() {
  if (loading.value || !validate()) return;
  loading.value = true;
  try {
    const result = await login({
      username: loginData.username.trim(),
      password: loginData.password,
      remember: loginData.remember,
    });
    await auth.applyLogin(result);
    toast.success("登录成功");
    await router.push(typeof route.query.redirect === "string" ? route.query.redirect : "/admin");
  } catch (e) {
    toast.error(e instanceof Error ? e.message : "登录失败，请检查用户名和密码");
  } finally {
    loading.value = false;
  }
}

function formatCountdown(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const minutes = Math.floor(total / 60);
  const rest = total % 60;
  return `${String(minutes).padStart(2, "0")}:${String(rest).padStart(2, "0")}`;
}

async function handleForgotPassword() {
  try {
    await confirm({
      title: "重置密码确认",
      icon: "question",
      danger: false,
      size: "md",
      message:
        "系统会在容器日志中输出一枚临时管理员密码。\n\n" +
        "• 完成改密后，新密码会替换原密码，临时密码立即失效。\n" +
        "• 未改密前，临时密码 10 分钟内有效，且不影响原密码。",
      confirmText: "确定重置",
      cancelText: "取消",
    });
  } catch {
    return;
  }

  loading.value = true;
  try {
    const data = await resetPassword();
    const remaining =
      typeof data.remaining_seconds === "number"
        ? `\n有效期剩余 ${formatCountdown(data.remaining_seconds)}`
        : "";
    await showConfirm({
      title: data.reused ? "临时密码仍有效" : "临时密码已生成",
      icon: "info",
      danger: false,
      showCancel: false,
      confirmText: "我知道了",
      message:
        (data.reused
          ? "临时密码仍在有效期内，请查看容器日志获取临时密码。"
          : "已生成临时密码，请查看容器控制台日志获取临时密码。") +
        remaining +
        "\n\n宿主机终端执行（请将 litepan 替换为实际容器名）：\n  docker logs litepan",
    });
  } catch (e) {
    toast.error(e instanceof Error ? e.message : "重置失败，无法连接到服务器");
  } finally {
    loading.value = false;
  }
}

onMounted(async () => {
  try {
    const status = await fetchAuthStatus();
    if (status.is_admin) {
      await auth.applyStatus(status);
      await router.replace("/admin");
    }
  } catch {
    /* 未登录，继续显示登录页 */
  }
});
</script>

<template>
  <div class="login-container">
    <div class="left-panel">
      <div class="brand-row">
        <img src="/static/img/logo.png" alt="MyPan" class="brand-logo" />
      </div>
      <div class="characters-area">
        <AnimatedCharacters
          :is-typing="isTyping"
          :show-password="showPassword"
          :password-length="passwordValue.length"
          :is-password-guard-mode="isPasswordGuardMode"
        />
      </div>
      <div class="decor-blur1" />
      <div class="decor-blur2" />
      <div class="decor-grid" />
    </div>

    <div class="right-panel">
      <div class="form-card">
        <p class="form-tag">管理员登录</p>
        <div class="mobile-logo"><span>MyPan 控制台</span></div>

        <div class="form-header">
          <h1 class="form-title">欢迎回来</h1>
          <p class="form-subtitle">轻量级多网盘聚合管理系统</p>
        </div>

        <form class="login-form" @submit.prevent="handleLogin">
          <div class="field-label">用户名</div>
          <div class="field-block">
            <input
              v-model="loginData.username"
              class="login-input"
              placeholder="请输入用户名"
              autocomplete="username"
              @focus="isTyping = true"
              @blur="isTyping = false"
              @keyup.enter="focusPassword"
            />
            <p v-if="errors.username" class="field-error">{{ errors.username }}</p>
          </div>

          <div class="field-label">密码</div>
          <div class="field-block">
            <div class="input-wrapper input-password-wrapper">
              <input
                id="login-password"
                v-model="loginData.password"
                :type="showPassword ? 'text' : 'password'"
                class="login-input"
                placeholder="请输入密码"
                autocomplete="current-password"
                @focus="passwordFocused = true"
                @blur="passwordFocused = false"
                @keyup.enter="handleLogin"
                @click="passwordFocused = true"
              />
              <span class="eye-toggle" @click.stop="showPassword = !showPassword">
                <svg
                  v-if="showPassword"
                  viewBox="0 0 24 24"
                  width="18"
                  height="18"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                >
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
                <svg
                  v-else
                  viewBox="0 0 24 24"
                  width="18"
                  height="18"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                >
                  <path
                    d="M17.94 17.94A10.07 10.07 0 0112 20c-7 0-11-8-11-8a18.45 18.45 0 015.06-5.94M9.9 4.24A9.12 9.12 0 0112 4c7 0 11 8 11 8a18.5 18.5 0 01-2.16 3.19m-6.72-1.07a3 3 0 11-4.24-4.24"
                  />
                  <line x1="1" y1="1" x2="23" y2="23" />
                </svg>
              </span>
            </div>
            <p v-if="errors.password" class="field-error">{{ errors.password }}</p>
          </div>

          <div class="login-options">
            <label class="remember-row">
              <input v-model="loginData.remember" type="checkbox" />
              <span>保持登录（30天）</span>
            </label>
            <a href="#" class="forgot-link" @click.prevent="handleForgotPassword">忘记密码？</a>
          </div>

          <button type="submit" class="submit-btn" :disabled="loading">
            {{ loading ? "登录中..." : "登录" }}
          </button>
        </form>

        <p class="footer-hint">首次使用请查阅文档了解初始配置</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-container {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 1fr 1fr;
}

@media (max-width: 1024px) {
  .login-container {
    grid-template-columns: 1fr;
  }
}

.left-panel {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 48px;
  background: linear-gradient(145deg, #0f172a 0%, #1e3a8a 50%, #1e40af 100%);
  overflow: hidden;
}

@media (max-width: 1024px) {
  .left-panel {
    display: none;
  }
}

.brand-row {
  position: relative;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 10px;
}

.brand-logo {
  height: 34px;
  width: auto;
  display: block;
}

.characters-area {
  position: relative;
  z-index: 20;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  height: 500px;
}

.decor-blur1,
.decor-blur2,
.decor-grid {
  position: absolute;
  pointer-events: none;
}

.decor-blur1 {
  top: 15%;
  right: 10%;
  width: 300px;
  height: 300px;
  background: rgba(59, 130, 246, 0.25);
  border-radius: 50%;
  filter: blur(80px);
  z-index: 0;
}

.decor-blur2 {
  bottom: 10%;
  left: 5%;
  width: 400px;
  height: 400px;
  background: rgba(30, 64, 175, 0.3);
  border-radius: 50%;
  filter: blur(100px);
  z-index: 0;
}

.decor-grid {
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.03) 1px, transparent 1px);
  background-size: 40px 40px;
  z-index: 1;
}

.right-panel {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px;
  background:
    linear-gradient(to right, rgba(30, 58, 138, 0.18) 0%, transparent 18%),
    radial-gradient(circle at 20% 0%, rgba(241, 245, 255, 0.9), transparent 35%),
    radial-gradient(circle at 90% 80%, rgba(219, 234, 254, 0.9), transparent 40%),
    linear-gradient(160deg, #f8fafc 0%, #eef2ff 52%, #eff6ff 100%);
}

.form-card {
  width: 100%;
  max-width: 430px;
  border-radius: 24px;
  background: rgba(255, 255, 255, 0.86);
  border: 1px solid rgba(148, 163, 184, 0.24);
  box-shadow: 0 24px 50px rgba(30, 41, 59, 0.12);
  backdrop-filter: blur(14px);
  padding: 36px 32px 30px;
}

.form-tag {
  margin: 0 0 16px;
  text-align: center;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.14em;
  color: #1e40af;
}

.mobile-logo {
  display: none;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 700;
  color: #0f172a;
  margin-bottom: 24px;
}

@media (max-width: 1024px) {
  .mobile-logo {
    display: flex;
  }
}

.form-header {
  text-align: center;
  margin-bottom: 28px;
}

.form-title {
  font-size: 28px;
  font-weight: 700;
  letter-spacing: -0.03em;
  color: #0b1220;
  margin: 0 0 8px;
  line-height: 1.3;
}

.form-subtitle {
  font-size: 14px;
  color: #64748b;
  margin: 0;
  line-height: 1.6;
}

.field-block {
  margin-bottom: 20px;
}

.field-label {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 6px;
  letter-spacing: 0.3px;
  text-transform: uppercase;
}

.field-error {
  margin: 4px 0 0;
  font-size: 13px;
  color: #dc2626;
}

.input-wrapper {
  position: relative;
  width: 100%;
}

.login-input {
  width: 100%;
  height: 50px;
  padding: 0 15px;
  font-size: 14px;
  color: #111827;
  background: rgba(248, 250, 252, 0.95);
  border: 1px solid #d8dee8;
  border-radius: 14px;
  outline: none;
  box-sizing: border-box;
  transition: border-color 0.2s, box-shadow 0.2s, background 0.2s;
}

.login-input::placeholder {
  color: #9aa4b2;
}

.login-input:hover {
  border-color: #3b82f6;
  background: #fff;
}

.login-input:focus {
  border-color: #1e40af;
  background: #fff;
  box-shadow: 0 0 0 4px rgba(59, 130, 246, 0.15);
}

.input-password-wrapper .login-input {
  padding-right: 44px;
}

.eye-toggle {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  color: #64748b;
  cursor: pointer;
  display: flex;
  align-items: center;
  transition: color 0.2s;
  z-index: 1;
}

.eye-toggle:hover {
  color: #1e40af;
}

.login-options {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  margin-bottom: 20px;
}

.remember-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: #475569;
  cursor: pointer;
}

.forgot-link {
  color: #4c74df;
  font-size: 14px;
  text-decoration: none;
  transition: color 0.2s;
}

.forgot-link:hover {
  color: #1e40af;
}

.submit-btn {
  width: 100%;
  height: 52px;
  font-size: 15px;
  font-weight: 600;
  border-radius: 14px;
  border: none;
  color: #fff;
  letter-spacing: 0.5px;
  cursor: pointer;
  background: linear-gradient(135deg, #1e40af 0%, #4c74df 55%, #02a6f0 100%);
  box-shadow: 0 14px 26px rgba(30, 64, 175, 0.24);
  transition: transform 0.2s, box-shadow 0.2s, opacity 0.2s;
}

.submit-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 16px 28px rgba(30, 64, 175, 0.32);
}

.submit-btn:disabled {
  opacity: 0.72;
  cursor: not-allowed;
}

.footer-hint {
  text-align: center;
  font-size: 12px;
  color: #64748b;
  margin: 20px 6px 0;
  line-height: 1.6;
}
</style>
