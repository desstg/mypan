-- 把「列表入库冲掉详情字段」这件事再堵一处：preview_images_json 与四个标记 + 两个计数。
--
-- 背景（2026-10-05 真机）：用户报「详情页打开有些内容是空的、要等自动获取才显示」。
-- 实测群晖库 8178 部抓过详情的片子里 **425 部的 raw_json 里有剧照（最多 19 张）
-- 而 preview_images_json 是空数组** —— 摘要行（榜单刷新 / 影库同步 / 搜索）不带剧照，
-- 而 upsert 里这一列是**无条件覆盖**，于是详情抓来的被冲成 []。
--
-- 详情页首屏走 DetailLocal（只读本地），assembleDetail 直接把这一列塞进响应，
-- 所以被冲空的片子**剧照那一块整个不渲染**，直到 hydrate / 「重新获取」再抓一次详情。
-- 这与 0041 修 summary/review、443fe11 修 duration/score/tags/type 是同一个 bug 的
-- 同一个位置，这一条把剩下的列补完。
--
-- 同时回填存量。**判据与 0041 逐字同形**（两道闸门缺一不可）：
--   * json_valid(raw_json) —— 真库里有一批行的 raw_json 是空串/半截，
--     直接 json_extract 会整条语句报 malformed JSON 而**不做任何更新**；
--   * json_array_length(...) > 0 —— 键存在但是空数组时不该覆盖。
--
-- ⚠️ **raw_json 里的 preview_images 是 `[{"large_url":…,"thumb_url":…}]`**（上游原样），
-- 而列里存的是**只留 large_url 的字符串数组**（见 javdb.NormalizeMovie）。
-- 所以这里不能直接 json_extract 出来写进去 —— 得用 json_group_array 把 large_url
-- 抽出来重拼，否则读侧（前端按字符串数组用）会拿到一堆对象。
--
-- 顺带把四个标记与两个计数按同一份 raw 对齐（它们的零值语义模糊，光看值分不出
-- 「真没有」与「列表接口没给」，所以只在「列是零值 + raw 里确实是正的」时才写）。
-- has_preview_images 尤其重要：它与 preview_images_json 是一组快照，
-- 半新半旧会出现「标记说有剧照、列里却是空数组」这种自相矛盾的行。

-- ① 剧照：raw 里有图、列是空的 → 从 raw 重拼成「只留 large_url 的字符串数组」
UPDATE jav_movies
SET preview_images_json = (
      SELECT json_group_array(json_extract(value, '$.large_url'))
        FROM json_each(json_extract(raw_json, '$.preview_images'))
       WHERE COALESCE(json_extract(value, '$.large_url'), '') <> ''
    )
WHERE (preview_images_json IS NULL OR preview_images_json IN ('', '[]'))
  AND json_valid(raw_json)
  AND COALESCE(json_array_length(json_extract(raw_json, '$.preview_images')), 0) > 0;

-- ② has_preview_images：列是 0 而 raw 里真有图 → 补成 1
--    （与 ① 同一批行；分开写是因为 ① 的判据是「列空」，而这一列的判据是「列为 0」）
UPDATE jav_movies
SET has_preview_images = 1
WHERE has_preview_images = 0
  AND json_valid(raw_json)
  AND COALESCE(json_array_length(json_extract(raw_json, '$.preview_images')), 0) > 0;

-- ③ 磁链数 / 评论数：列为 0 而 raw 里是正数 → 补上
--    零值语义模糊（真没有 vs 列表没给），所以只在「raw 明确给了正数」时才写。
UPDATE jav_movies
SET magnets_count = CAST(json_extract(raw_json, '$.magnets_count') AS INTEGER)
WHERE magnets_count = 0
  AND json_valid(raw_json)
  AND COALESCE(CAST(json_extract(raw_json, '$.magnets_count') AS INTEGER), 0) > 0;

UPDATE jav_movies
SET reviews_count = CAST(json_extract(raw_json, '$.reviews_count') AS INTEGER)
WHERE reviews_count = 0
  AND json_valid(raw_json)
  AND COALESCE(CAST(json_extract(raw_json, '$.reviews_count') AS INTEGER), 0) > 0;

-- ④ 两个「有内容」的标记：列为 0 而 raw 里标着有 → 补成 1
--    has_preview_video 看 preview_video_url（上游那个字段才是真凭据）
UPDATE jav_movies
SET has_preview_video = 1
WHERE has_preview_video = 0
  AND json_valid(raw_json)
  AND COALESCE(json_extract(raw_json, '$.preview_video_url'), '') <> '';

UPDATE jav_movies
SET has_cnsub = 1
WHERE has_cnsub = 0
  AND json_valid(raw_json)
  AND COALESCE(json_extract(raw_json, '$.has_cnsub'), 0) <> 0;

-- ⑤ can_play：**不回填**。
--    它判的是「这颗资源现在能不能播」，取决于磁链表与播放器状态，不是详情接口给的；
--    raw 里没有这个字段，硬推会猜错。留着让下次详情 / 磁链抓取自己更新。
