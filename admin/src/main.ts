import 'element-plus/dist/index.css'
import './assets/main.css'
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { ElLoading } from 'element-plus'

import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(ElLoading)

// The first navigation resolves the session (the guard loads it), so the shell
// renders with the signed-in user instead of an empty corner. A failed session
// check still lands on the login page.
void router.isReady().then(() => app.mount('#app'))
