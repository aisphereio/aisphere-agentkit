# 产物目录展示与命名约定

## 目标

Workbench 里的产物不应该只显示 `book_20260602071614_xxx__chapter_0001.txt` 这种技术名。用户需要看到它在业务流程里的含义，例如“第 1 章 · 切分正文”“第 1 章 · 写作技法包”“第 1 章 · 复写差距报告”。

这版前端采用“原始产物 ID 不变，展示名自动翻译”的方式：

- 点击、预览、下载仍然使用原始 artifact id 和 version id，保证打开的是正确文件。
- UI 根据命名规则和 manifest 自动生成中文展示名。
- manifest 加载后，会把 `__chapter_0001.txt` 映射成真实章节标题。
- 未识别文件归入“其他产物”，同时保留技术名 tooltip，方便排查。

## 当前目录分组

| 分组 | 典型文件 | 展示含义 |
| --- | --- | --- |
| 书籍索引 | `<book_id>__manifest.json` | 书籍目录、章节索引、切分元信息 |
| 原文归档 | `<book_id>__source_utf8.txt` | 拆章前的 UTF-8 标准化全文 |
| 切分章节 | `<book_id>__chapter_0001.txt` | 第 1 章正文，manifest 加载后显示章节标题 |
| 拆书分析 | `chapter_analysis_<book_id>_<chapter>.md` | 章节功能、事件压缩、人物状态、主线推进 |
| Skill 教研 | `chapter_skill_pack_*`、`reconstruction_probe_*`、`reconstruction_gap_report_*`、`cross_chapter_skill_candidates_*` | 技法包、盲测样稿、差距报告、多章候选 Skill |
| 写作产出 | `chapter_draft_*`、`chapter_revision_*`、`rewrite_*`、`opening_draft_*` | 新写章节、改稿、复写稿 |
| 规划设定 | `outline_*`、`story_bible_*`、`world_*`、`character_*` 等 | 大纲、设定、人物、世界观、项目级产物 |

## 推荐命名原则

为了让 UI 能稳定识别，后续 Agent 和 Tool 保存 artifact 时应遵守这些规则：

```text
<book_id>__manifest.json
<book_id>__source_utf8.txt
<book_id>__chapter_0001.txt
chapter_analysis_<book_id>_<chapter_no>.md
chapter_skill_pack_<book_id>_<chapter_no>.json
reconstruction_probe_<book_id>_<chapter_no>_<attempt>.md
reconstruction_gap_report_<book_id>_<chapter_no>_<attempt>.json
cross_chapter_skill_candidates_<book_id>_<start_chapter>_<end_chapter>.json
chapter_draft_<book_id>_<chapter_no>.md
chapter_revision_<book_id>_<chapter_no>.md
chapter_final_<book_id>_<chapter_no>.md
```

## 控制边界

这套机制只控制“展示层”。不要为了中文展示名改 artifact id，否则历史链接、版本选择、下载、`load_artifacts` 都可能断。

如果要新增业务产物类型，优先补充展示规则，而不是改变保存协议。

## 跨 Session 资料复用与挂载

切章产物属于“书库资料”，不是某个对话的临时结果。BookPreprocessorToolset 从 phase5 开始会把切章后的核心资料保存为用户级 artifact，也就是使用 `user:` 前缀：

```text
user:<book_id>__manifest.json
user:<book_id>__source_utf8.txt
user:<book_id>__chapter_0001.txt
```

这样同一个 app/user 下的新 session 也能通过 `book_list_books` 找到这些书，不需要重复上传和重复切章。

新 session 要继续调研某本已切分小说时，先执行：

```text
book_list_books
book_mount({"book_id":"<book_id>"})
```

`book_mount` 只会在当前 session 保存一个轻量指针：

```text
mounted_book.json
```

它不复制正文，不复制章节，不重新切章。后续用户说“当前书籍”“这本书”“第 1 章”时，`book_get_manifest` 和 `book_get_chapter` 可以省略 `book_id`，默认读取 `mounted_book.json` 指向的书。

前端展示时会去掉 `user:` 前缀做中文解释，但打开、预览、下载仍使用原始 artifact id，例如 `user:<book_id>__chapter_0001.txt`，所以不会点错文件。

### 旧 Session 切章产物迁移

如果某本书是在旧版本中切分的，manifest 和 chapter 可能没有 `user:` 前缀，只存在于当时那个 session。此时不要重新切章，先回到原 session 执行：

```text
book_publish_to_library({"book_id":"<book_id>"})
```

它会把旧的 session 级 manifest/source/chapter 复制成用户级书库产物。迁移完成后，新 session 再执行 `book_mount` 即可复用。
