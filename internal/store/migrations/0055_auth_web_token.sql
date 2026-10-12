-- 网页版登录态列。
--
-- 背景：123 的**分享转存**与**磁力离线下载**都只有网页接口能做
-- （实测：开放平台的 access_token 调这些接口回 401 cookie token is empty）。
-- 而 auth_states 里已有的 cookie 列是给 115 那种「整条 Cookie 串」用的，
-- 123 要存的是一条 Bearer 令牌 —— 塞进 cookie 列虽然能跑，但语义是错的：
-- 以后谁看到 `cookie` 里是一段 JWT 都会先愣一下。
--
-- 所以单开一列。默认空串，存量账号不受影响。
ALTER TABLE account_auth_states ADD COLUMN web_token TEXT NOT NULL DEFAULT '';
