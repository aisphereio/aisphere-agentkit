# ADK 接入 Novel Splitter MCP 文档

## 1. 当前 ADK 源码现状

你上传的 ADK 源码里已经有 MCP 工具集：

```text
agentkit/tool/mcptoolset
```

它已经能做：

```text
MCP tools/list -> 转成 ADK Tool
MCP tools/call -> 执行 MCP Tool
MCP StructuredContent -> 返回给模型
```

也就是说，底层 MCP Client 能力已经有了。

现在缺的只是 YAML 配置层：

```text
internal/configurable/configurable_utils.go
```

原来只支持：

```text
stdio_connection_params
```

也就是启动一个本地 MCP 命令进程。

Novel Splitter 最新版是单服务 HTTP + /mcp，所以 ADK 需要支持：

```text
mcp.StreamableClientTransport
```

本补丁做的事情就是：让 YAML Agent 可以直接配置 HTTP MCP Server。

---

## 2. 接入后整体链路

```text
用户
  ↓
ADK Agent
  ↓
ADK McpHttpToolset
  ↓
Novel Splitter /mcp
  ↓
Novel Splitter Service
  ↓
MinIO
```

模型不会直接访问 MinIO，也不会自己解析 TXT。模型只会看到 MCP tools：

```text
list_split_books
create_split_job
get_split_handoff
get_novel_chapter_batch
save_skill_batch
merge_skill_batches
```

---

## 3. 需要修改 ADK 哪个文件

把补丁包里的：

```text
files/internal/configurable/configurable_utils.go
```

覆盖到你的 ADK 源码：

```text
agentkit/internal/configurable/configurable_utils.go
```

这个改造新增了两个 Toolset 名称：

```text
McpToolset
McpHttpToolset
```

其中 `McpToolset` 继续兼容老的 stdio 配置；`McpHttpToolset` 用来显式表示 HTTP MCP。

---

## 4. 环境变量

启动 ADK 前设置：

### Linux/macOS

```bash
export NOVEL_SPLITTER_MCP_ENDPOINT=http://127.0.0.1:8090/mcp
export NOVEL_SPLITTER_MCP_TOKEN=change-me
```

### Windows PowerShell

```powershell
$env:NOVEL_SPLITTER_MCP_ENDPOINT="http://127.0.0.1:8090/mcp"
$env:NOVEL_SPLITTER_MCP_TOKEN="change-me"
```

---

## 5. 启动 Novel Splitter

先启动 MinIO 和拆书服务：

```bash
cd novel_splitter_unified

docker compose -f docker-compose.minio.yml up -d

go run ./cmd/novel-splitter serve -c configs/config.minio.yaml
```

验证：

```bash
curl http://127.0.0.1:8090/api/health
```

验证 MCP：

```bash
curl -H "Authorization: Bearer change-me" http://127.0.0.1:8090/mcp
```

---

## 6. 新增 Agent

补丁包里提供两个 Agent：

```text
agents/novel_assets_mcp/root_agent.yaml
agents/book_skill_runner_mcp/root_agent.yaml
```

复制到你的 ADK：

```bash
cp -r agents/novel_assets_mcp      <agentkit>/agents/
cp -r agents/book_skill_runner_mcp <agentkit>/agents/
```

---

## 7. Agent 配置说明

关键配置如下：

```yaml
tools:
  - name: McpHttpToolset
    args:
      transport: streamable_http
      http_connection_params:
        endpoint: ${NOVEL_SPLITTER_MCP_ENDPOINT}
        headers:
          Authorization: "Bearer ${NOVEL_SPLITTER_MCP_TOKEN}"
      tool_filter:
        - list_split_books
        - get_split_handoff
        - get_novel_chapter_batch
        - save_skill_batch
```

含义：

```text
endpoint：Novel Splitter 的 /mcp 地址
headers.Authorization：调用 /mcp 时带 token
tool_filter：只把这些 MCP tools 暴露给当前 Agent
```

不要把所有工具都给所有 Agent。比如只读分析 Agent 不应该拿到 delete_novel_chapter。

---

## 8. 第一步测试：查询切分好的书

启动 ADK 后，选择 `novel_assets_mcp` Agent，问：

```text
现在有什么切分好的书？
```

预期：

```text
模型调用 list_split_books
MCP Server 返回 books
模型整理成人话
```

如果没有书，返回空列表是正常的。

---

## 9. 第二步测试：异步切书

先把书上传到 Novel Splitter 或 MinIO，然后问：

```text
把 MinIO 里的 incoming/uploads/边戎.txt 异步切一下，书名叫边戎。
```

预期工具调用：

```text
create_split_job
get_job_status
```

完成后应该返回：

```text
book_id
book_name
chapter_count
quality_report_object
manifest_object
```

---

## 10. 第三步测试：读取章节批次

问：

```text
读取边戎第 1 到 3 章。
```

预期工具调用：

```text
list_split_books(keyword="边戎")
get_novel_chapter_batch(book_id, start=1, count=3)
```

---

## 11. 第四步测试：每三章提炼对话 Skill

选择 `book_skill_runner_mcp` Agent，问：

```text
用边戎这本书，每三章提炼一次对话 skill，先处理第一批。
```

预期工具调用：

```text
list_split_books
get_split_quality_report
get_split_handoff
get_novel_chapter_batch(start=1,count=3)
save_skill_batch(batch_no=1)
```

注意：不要让模型一口气循环完整本书。全书循环应该交给你的 ADK Workflow 或外层长任务系统。

---

## 12. 与旧 BookPreprocessorToolset 的关系

你原来的 Agent 里有：

```yaml
- name: BookPreprocessorToolset
- name: BookSkillRunToolset
```

这是 ADK 内置的旧拆书/长任务工具。

现在 Novel Splitter MCP 是外部服务化拆书工具，建议逐步替换：

```text
BookPreprocessorToolset -> Novel Splitter MCP 的切书/章节工具
BookSkillRunToolset    -> ADK Workflow + Novel Splitter MCP 的 batch/skill 工具
```

不要一次性把旧 Agent 大改。推荐先新增两个 MCP Agent 跑通链路：

```text
novel_assets_mcp
book_skill_runner_mcp
```

跑通后再决定是否改造 `book_dissector` 和 `book_skill_runner`。

---

## 13. 为什么需要 tool_filter

MCP Server 暴露很多工具：

```text
查书
切书
读章节
删章节
改章节
保存 skill
合并 skill
```

不同 Agent 不应该拿全部工具。

例如 `book_skill_runner_mcp` 只需要：

```text
list_split_books
get_split_quality_report
get_split_handoff
get_novel_chapter_batch
save_skill_batch
merge_skill_batches
```

它不需要：

```text
delete_novel_chapter
update_novel_chapter
reorder_novel_chapter
```

这样可以减少模型选错工具，也降低误操作风险。

---

## 14. 结果大小控制

MCP 初始上下文只会给模型工具名、描述和参数 schema，不会把所有章节正文塞进上下文。

运行时也要遵守：

```text
list_split_books：只返回元数据
get_split_handoff：只返回章节索引，不返回正文
get_novel_chapter_batch：每次只读少量章节，默认 3 章
save_skill_batch：保存当前批次结果
```

长篇小说处理必须分批。

---

## 15. 最终推荐接入顺序

1. 启动 Novel Splitter。
2. 用 curl 测 `/mcp`。
3. 给 ADK 打本补丁，支持 `McpHttpToolset`。
4. 加入 `novel_assets_mcp` Agent。
5. 测试“现在有什么切分好的书”。
6. 测试异步切书。
7. 加入 `book_skill_runner_mcp` Agent。
8. 测试“先处理第一批三章对话 skill”。
9. 最后再接你的 Workflow 长任务循环。
