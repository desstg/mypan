-- 把「国产」两条规则挪到「有码」**前面**。
--
-- 背景：`有码` 那条 pattern（`^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`）会**先**吃掉带连字符的
-- 国内番号（`MGL-0002` / `MDCM-0001` / `MM-023`…），于是它们进的是「有码」而不是
-- 「国产」—— 而用户库里那批本来就在「国产AV」目录下（是他手工搬的）。
--
-- 实测（全库 8426 个番号，含用户磁盘上 247 个国产目录）：改判 **71 个**，
-- **全部是「有码 → 国产」这一个方向**，且全是国内厂牌；**没有一个是日式番号被抢**
-- （`MDB-082` / `MDS-061` / `MDTM-270` / `MDYD-636` 这些日本 Madonna 仍落在「有码」）。
--
-- 与 0037 的分工：0037 补的是「国产·无连字符」这条**规则本身**（追加在末尾），
-- 这条 0038 调的是**顺序**。两条都要有：只有 0037 时无连字符的能进国产，
-- 但带连字符的仍被「有码」先吃掉。
--
-- ## 三条设计约束（与 0036 / 0037 同一套）
--
--  1. **按「目标目录是不是国产 / 是不是有码」判，不按规则名**。规则名是用户可改的
--     （实测这台机器上就叫「国产」和「有码」，但别的库可能不同）。
--  2. **找不到「有码」那条就什么都不做**（`EXISTS` 守卫）。用户可能把那条删了 ——
--     那说明他不按这套口径分，硬排是替他做决定。
--  3. **幂等**：判据是「国产那两条是否已经排在有码之前」，已经对就不动。
--
-- ## 实现：重排整个数组
--
-- `json_insert` 做不到这件事 —— 它对**已存在的下标是 no-op**（只做「路径不存在才插」），
-- 而这里要的是「把已有元素挪个位置」。所以用 `json_group_array` 重建整个数组。
--
-- 排序键是「band + 原下标」两级：
--   band 0 = 原本就在「有码」之前的、且不是国产的（欧美 / 无码和素人 / 欧美日期型）
--   band 1 = 目标目录是「国产」的（两条，保持它们彼此的相对顺序）
--   band 2 = 「有码」及其之后的一切（有码 / 未匹配 / …）
--
-- ⚠️ **`value` 必须写成 `configs.value`**：`json_each` 自己有一列就叫 `value`，
-- 子查询里写裸 `value` 会**静默**绑到 json_each 那一行上（`json_extract(<数组元素>, …)`
-- → NULL），于是子查询一行都返回不了、`EXISTS` 为假、整条 UPDATE 影响 0 行 —— 不报错。
--
-- ⚠️ `json_group_array(json(v))` 里的 `json(v)` 不能省：省掉会把对象当**字符串**再包一层引号。
UPDATE configs
   SET value = json_set(
           value,
           '$.classify_rules',
           (SELECT json_group_array(json(v)) FROM (
              SELECT je.value AS v,
                     CASE
                       -- 目标目录是「国产」的 → 排在「有码」前面
                       WHEN json_extract(je.value, '$.target_name') = '国产' THEN 1
                       -- 原本就在「有码」之前的、且不是国产 → 更前
                       WHEN CAST(je.key AS INTEGER) < (
                              SELECT MIN(CAST(j2.key AS INTEGER))
                                FROM json_each(json_extract(configs.value, '$.classify_rules')) AS j2
                               WHERE json_extract(j2.value, '$.target_name') = '有码'
                            ) THEN 0
                       -- 「有码」及其之后的一切
                       ELSE 2
                     END AS band,
                     CAST(je.key AS INTEGER) AS orig
                FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
               ORDER BY band, orig
           ))),
           updated_at = CURRENT_TIMESTAMP
 WHERE key = 'mo_jav_rules'
   -- 有「有码」这条规则才动
   AND EXISTS (SELECT 1 FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
                WHERE json_extract(je.value, '$.target_name') = '有码')
   -- 有「国产」这条规则才动
   AND EXISTS (SELECT 1 FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
                WHERE json_extract(je.value, '$.target_name') = '国产')
   -- 幂等：国产那两条**还没**排在「有码」之前时才动。
   -- 判据取「最后一条国产」的下标与「第一条有码」的下标，前者必须更小。
   AND (SELECT MAX(CAST(je.key AS INTEGER))
          FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
         WHERE json_extract(je.value, '$.target_name') = '国产')
       > (SELECT MIN(CAST(je.key AS INTEGER))
            FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
           WHERE json_extract(je.value, '$.target_name') = '有码');
