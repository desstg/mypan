import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import { router } from "./router";
import { initTheme } from "./utils/theme";

import "./assets/iconfont/iconfont.js";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/buttons.css";
import "./styles/file-list.css";
import "./styles/file-toolbar.css";
import "./styles/dropdown-menu.css";
import "./styles/confirm-modal.css";
import "./styles/dust-removal.css";
import "./styles/skins/brutal.css";

initTheme();

createApp(App).use(createPinia()).use(router).mount("#app");
