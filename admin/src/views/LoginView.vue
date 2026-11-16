<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, login } from '@/api/session'

const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

const route = useRoute()
const router = useRouter()

async function submit() {
  if (busy.value) return
  error.value = ''
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名与密码'
    return
  }
  busy.value = true
  try {
    await login(username.value.trim(), password.value)
    const next = typeof route.query.next === 'string' ? route.query.next : '/'
    await router.replace(next)
  } catch (err) {
    // A wrong password, a disabled account and a rate-limited call all land
    // here with the reason the server gave; the message carries the report id.
    error.value = err instanceof ApiError ? `${err.reason}：${err.message}` : String(err)
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  document.title = '登录 · 映刻运营后台'
})
</script>

<template>
  <main class="login">
    <section class="brand-pane" aria-hidden="true">
      <div class="brand-top">
        <span class="logo"><span class="mark">映</span>映刻 OpenDuanju</span>
        <h1>把采集、处理、审核与发布收在一条线上</h1>
        <p>
          内容从来源汇入本地候选，素材全部就绪后进入审核，通过审核才可发布。每一步有留痕，权限由服务端判定。
        </p>
        <ul>
          <li>来源与通道可查看与启停，版本冲突不覆盖他人修改</li>
          <li>素材任务按集显示阶段与失败原因，可重试或取消</li>
          <li>审计记录可追溯到人、对象版本与一次请求</li>
        </ul>
      </div>
      <span class="brand-foot">首版 · 仅内部运营使用</span>
    </section>

    <section class="form-pane">
      <form class="form" @submit.prevent="submit">
        <h2>登录运营后台</h2>
        <p class="hint">使用分配给你的账号登录，权限由角色决定。</p>

        <p v-if="error" class="alert" role="alert">{{ error }}</p>

        <label class="field">
          <span class="label">用户名 <span class="req" aria-hidden="true">*</span></span>
          <input
            v-model="username"
            autocomplete="username"
            placeholder="请输入用户名"
            aria-required="true"
          />
        </label>

        <label class="field">
          <span class="label">密码 <span class="req" aria-hidden="true">*</span></span>
          <input
            v-model="password"
            type="password"
            autocomplete="current-password"
            placeholder="请输入密码"
            aria-required="true"
          />
          <span class="help">8～128 位；连续失败会按频率限制</span>
        </label>

        <button class="submit" type="submit" :disabled="busy" :aria-busy="busy">
          {{ busy ? '登录中…' : '登录' }}
        </button>

        <p class="legal">
          首次部署请先在服务器执行一次性初始化命令创建管理员，系统不内置默认口令。
        </p>
      </form>
    </section>
  </main>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 1.1fr 1fr;
  background: #0f1117;
}
.brand-pane {
  position: relative;
  padding: var(--sp-7) var(--sp-7) var(--sp-6);
  color: #fff;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  overflow: hidden;
}
.brand-pane::before {
  content: '';
  position: absolute;
  inset: -20% -10% auto auto;
  width: 620px;
  height: 620px;
  background: radial-gradient(circle, rgba(226, 58, 78, 0.45), transparent 62%);
}
.brand-top {
  position: relative;
}
.logo {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-2);
  font-size: var(--fs-22);
  font-weight: 700;
}
.mark {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  font-size: 14px;
  font-weight: 800;
  background: linear-gradient(135deg, var(--brand), #ff7a5c);
}
h1 {
  margin-top: var(--sp-6);
  font-size: var(--fs-34);
  line-height: 1.25;
  letter-spacing: -0.4px;
  max-width: 12em;
}
.brand-top p {
  margin-top: var(--sp-3);
  color: #aeb6c4;
  font-size: var(--fs-14);
  max-width: 30em;
}
ul {
  list-style: none;
  padding: 0;
  margin: var(--sp-6) 0 0;
  display: grid;
  gap: var(--sp-2);
  color: #cfd6e2;
  font-size: var(--fs-13);
}
li::before {
  content: '—';
  color: var(--brand);
  margin-right: var(--sp-2);
}
.brand-foot {
  position: relative;
  color: #6b7486;
  font-size: var(--fs-12);
}
.form-pane {
  background: var(--surface);
  display: grid;
  place-items: center;
  padding: var(--sp-5);
}
.form {
  width: min(360px, 100%);
  display: grid;
  gap: var(--sp-4);
}
.form h2 {
  font-size: var(--fs-22);
}
.hint {
  color: var(--ink-600);
  font-size: var(--fs-13);
}
.alert {
  padding: var(--sp-3);
  border-radius: var(--r-md);
  background: var(--danger-weak);
  color: var(--danger);
  font-size: var(--fs-13);
}
.field {
  display: grid;
  gap: var(--sp-1);
}
.label {
  font-size: var(--fs-13);
  color: var(--ink-700);
  font-weight: 500;
}
.req {
  color: var(--brand);
}
input {
  height: 40px;
  padding: 0 var(--sp-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--r-md);
  background: var(--surface);
  font-size: var(--fs-14);
}
input:focus {
  border-color: var(--brand);
  outline: none;
  box-shadow: 0 0 0 3px var(--brand-weak);
}
.help {
  font-size: var(--fs-12);
  color: var(--ink-400);
}
.submit {
  height: 42px;
  border: 0;
  border-radius: var(--r-md);
  background: var(--brand);
  color: #fff;
  font-size: var(--fs-14);
  font-weight: 600;
}
.submit:hover:not(:disabled) {
  background: var(--brand-strong);
}
.submit:disabled {
  opacity: 0.8;
  cursor: progress;
}
.legal {
  font-size: var(--fs-12);
  color: var(--ink-400);
  text-align: center;
}

@media (max-width: 900px) {
  .login {
    grid-template-columns: 1fr;
  }
  .brand-pane {
    padding: var(--sp-5);
  }
  .brand-pane ul,
  .brand-foot {
    display: none;
  }
  h1 {
    font-size: var(--fs-22);
    margin-top: var(--sp-4);
  }
}
</style>
