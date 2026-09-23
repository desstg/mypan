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
// 管理端表格的共用样式。必须全局引：它定义的是 .admin-table / .admin-panel-table-wrap /
// .admin-table__action-btn 这类被十几个组件直接写在模板里的类，而其中大多数组件
// （TG 那几个、AdminTableActionBtn 本尊）都没有自己 import 它。按需 import 的写法
// 会造成「先逛过任务管理页样式就在、直接刷新到 TG 页样式就没了」这种跟访问顺序
// 有关的隐性故障 —— 表现是表头居中、行没有分隔线、操作按钮变成浏览器默认灰按钮
// 且图标塌成 0×0 看不见。
import "./styles/admin-table.css";
// 番号模块的卡片样式。同样必须全局引：.jav-card / .jav-chip / .jav-magnet 这些类
// 由 JavMovieCard 这个子组件渲染，而它自己不 import 样式（只有三个面板组件引了）。
// 按需 import 会让「直接深链到番号页」时卡片是裸的 —— 与上面 admin-table 那次
// 是同一类问题，这里按那条教训直接放全局。
import "./styles/jav.css";
import "./styles/confirm-modal.css";
import "./styles/dust-removal.css";
import "./styles/skins/brutal.css";

initTheme();

createApp(App).use(createPinia()).use(router).mount("#app");
