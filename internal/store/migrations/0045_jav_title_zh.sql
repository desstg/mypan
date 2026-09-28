-- 0045：给番号影片多加一个「中文标题」，以及它的来源。
--
-- 背景：JAVDB 给的大面积是**日文标题**（实测真库 8650 部里 6356 部的 title 含假名），
-- 而 missav / airav 这类站给的是**中文标题**（`最強美貌OL…` ↔ `最强美貌OL…`）。
-- 用户要的是「以后生成 nfo 时能直接取中文标题」，所以单独存一列，不覆盖现有的
-- `title`（那是 JAVDB 的口径，覆盖了就没有回退的余地）。
--
-- 三个列的分工（别再混）：
--   title        现有标题（JAVDB 口径，可能是日文也可能是繁中）
--   origin_title 原名（日文，可能是空 —— 实测 1577 部为空）
--   title_zh     中文标题（**别站补来的**，空 = 没补到）
--
-- 来源单独一列的理由与 summary_source 完全一致：只有真写进去了才更新来源，
-- 否则「这条中文标题是哪来的」会飘到一个空值上，之后想重刷都挑不出该重刷的行。

ALTER TABLE jav_movies ADD COLUMN title_zh TEXT NOT NULL DEFAULT '';
ALTER TABLE jav_movies ADD COLUMN title_zh_source TEXT NOT NULL DEFAULT '';
