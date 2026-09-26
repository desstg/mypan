-- 给「国产」补上**无连字符**形态的分类规则（`MD0292` / `MDCM-0001` / `DA-72`…）。
--
-- 背景：`国产` 那条规则的 includes 全是**带尾连字符**的前缀（`MD-` / `MDX-` …），
-- 而 includes 的匹配是纯 `strings.Contains`、不做分隔符归一化（照搬 115-auto 的行为），
-- 于是 `MD0292` 一个都命中不了 —— 实测用户库里 46 个国内番号栽在这上面，
-- 整理后全落进兜底分类（`未匹配`）。
--
-- 代码那边已在默认表里加了同名规则（`javrules/defaults.go` 的「国产·无连字符」），
-- 但**分类规则是存在库里的数据** —— 已经存过规则的库不会因为改代码而生效。
-- 这条迁移把同一条规则追加进用户的规则表。
--
-- 追加到**末尾**而不是插在「国产」前面：`json_insert` 的下标对**已存在的元素**
-- 是 no-op（它只做「路径不存在才插」），所以 `[2]` 那种写法**静默地什么都不做** ——
-- 第一次写就栽在这上面（UPDATE 影响 1 行、规则数却纹丝不动）。
-- 而 `[#]` 是 SQLite 的「追加到数组末尾」，它才是真正会插进去的那个写法。
--
-- 追加到末尾对结果**没有影响**：兜底那条（`nocode`）本来就是排在最后的，
-- 新规则在它前面；而它与「国产」目标目录相同（都进 `国产/`），
-- 谁先命中都落到同一个目录。
--
-- 两条设计约束（与 0036 同一套）：
--
--  1. **按规则名判断「补过没有」**（`NOT EXISTS`），因此天然幂等；
--     也不依赖数组下标或位置 —— 用户删过规则、调过顺序都不影响。
--  2. **只在「有『国产』这条规则」时才动**：用户可能把这条删了，
--     那说明他不按这套口径分，硬塞一条进去是替他做决定。
--
-- ⚠️ **`value` 必须写成 `configs.value`。** 这是另一个坑：`json_each` 自己有一列
-- 就叫 `value`，子查询里写裸 `value` 会**静默**绑到 json_each 那一行上
-- （`json_extract(<数组元素>, '$.classify_rules')` → NULL），于是子查询一行都
-- 返回不了、`EXISTS` 为假、整条 UPDATE 影响 0 行 —— 同样不报错。
--
-- ⚠️ 反斜杠：JSON 里存的是转义后的 `\\d`，SQL 字面量里要写**两个**才写得进去。
UPDATE configs
   SET value = json_insert(
           value,
           '$.classify_rules[#]',
           json('{"name":"国产·无连字符","target_name":"国产","mode":"pattern",' ||
                '"pattern":"(?:^|[^A-Za-z0-9])(?:MD|MDX|MDSJ|MDSR|MDHT|MAN|XB|XJX|JDSY|RAS|QQCM|AIMD|MDCM|MDAG|MDWP|MDHG|MDHS|MAD|MGL|MSD|SZL|BLX|BLXC|MCY|NHAV|EMTC|EMX|MFK|MPG|MNSC|WMM|MTVQ|MM|NI|FX|GX|PH|TZ|DA)[-_]?\\d"}')
       ),
       updated_at = CURRENT_TIMESTAMP
 WHERE key = 'mo_jav_rules'
   AND EXISTS (SELECT 1 FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
                WHERE json_extract(je.value, '$.target_name') = '国产')
   AND NOT EXISTS (SELECT 1 FROM json_each(json_extract(configs.value, '$.classify_rules')) AS je
                    WHERE json_extract(je.value, '$.name') = '国产·无连字符');
