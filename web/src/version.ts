// 应用品牌与版本号的唯一来源。前端所有展示位置统一引用此处。
export const APP_NAME = "MyPan";
export const APP_VERSION = "v1.0.1";
// 页脚「当前版本」徽标点击后跳转的目标
export const APP_URL = "https://github.com/desstg/mypan";

export const APP_VERSION_BADGE = `${APP_NAME} ${APP_VERSION.replace(/-?Beta$/i, "").trim()}`;
