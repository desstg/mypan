-- 把「列表入库冲掉详情字段」这件事再堵一处：summary / review。
--
-- 背景：jav_movies 的 upsert 里，javbus_cover / preview_video_url / raw_json 三个字段
-- 早就写了 `CASE WHEN excluded.x <> '' THEN … ELSE 保留原值`（注释写明了「榜单刷新时
-- 上游给不出它」），而 summary / review 漏了。这两列只在**详情接口**里有值，
-- 榜单与影库同步那些列表入库带的是空串。
--
-- 本次同时把历史数据里「列空、raw_json 里却有值」的那几条补回来
-- （实测真库 17 条；绝大多数片的简介在 raw_json 里也是空的 —— 上游本来就没给）。
--
-- 写法上两道闸门缺一不可：
--   * json_valid(raw_json) —— 真库里有一批行的 raw_json 是空串/半截（实测 6670 行），
--     直接 json_extract 会整条语句报 "malformed JSON" 而**不做任何更新**；
--   * COALESCE(...,'') <> '' —— 键存在但值是空串时不该覆盖（真库大量如此）。
UPDATE jav_movies
SET summary = COALESCE(json_extract(raw_json, '$.summary'), '')
WHERE (summary IS NULL OR summary = '')
  AND json_valid(raw_json)
  AND COALESCE(json_extract(raw_json, '$.summary'), '') <> '';

UPDATE jav_movies
SET review = COALESCE(json_extract(raw_json, '$.review'), '')
WHERE (review IS NULL OR review = '')
  AND json_valid(raw_json)
  AND COALESCE(json_extract(raw_json, '$.review'), '') <> '';
