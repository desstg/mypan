-- 关注的分享者。
--
-- 「评论区分享」里能点到分享者的名字，看他在本地库里分享过的影片；关注之后
-- 他就落在订阅页的「用户」那一档，不用每次翻回某部片再点进去。
--
-- 键是 JAVDB 的 user_id（就是 jav_reviews.user_id）。**不用用户名当键**：
-- 用户名可以改、也可能重名，而 user_id 是上游给的稳定身份。
-- username 存一份只是为了让列表在不查评论的情况下也能显示名字。
CREATE TABLE IF NOT EXISTS jav_follows (
    user_id    INTEGER PRIMARY KEY,
    username   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
