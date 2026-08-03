# Artifact Preview Roadmap

## 背景

当前 artifact 预览偏基础：文本、JSON、Markdown、图片等产物能打开，但整体体验更像调试面板，不像一个长期平台里的“产物工作台”。后续 Book-to-Skill、长任务、上传管理、项目资产都会产生大量 artifact，预览能力需要升级，但不能破坏既有原则：

- artifact 列表默认只展示名称、类型、版本、大小、来源和状态；
- 只有用户显式点击打开/预览时才加载内容；
- 大文件必须分页、截断或转码，不允许列表渲染时批量下载；
- 预览只服务用户界面，不改变 artifact id、version、scope；
- 预览生成物也应保存为 artifact，便于缓存、复用和审计。

## 目标

做一个平台自己的 `ArtifactPreviewHost`，不要直接绑定 kkFileView、ONLYOFFICE 或 Collabora 之一。

核心思路：

```text
ArtifactPreviewHost
  -> TextPreviewProvider
  -> MarkdownPreviewProvider
  -> JsonPreviewProvider
  -> CodePreviewProvider
  -> ImagePreviewProvider
  -> MediaPreviewProvider
  -> PdfPreviewProvider
  -> TablePreviewProvider
  -> HtmlSandboxPreviewProvider
  -> OfficePreviewProvider
```

`ArtifactPreviewHost` 负责统一交互、懒加载、错误状态、版本选择、下载、复制 artifact ref。各 `PreviewProvider` 负责具体格式渲染。

## 非目标

- 第一阶段不做 Office 在线编辑。
- 第一阶段不引入重量级独立预览服务作为强依赖。
- 不让模型直接拿到大文件全文。AI 分析仍走明确的 tool/preprocess 流程。
- 不把预览组件和 book 专属逻辑绑定。

## 推荐路线

### P0：前端原生预览框架

优先实现轻量、可控、通用的 preview host。

TODO：

- [ ] 新增 `ArtifactPreviewHostComponent`。
- [ ] 定义 `PreviewProvider` 接口。
- [ ] 根据 artifact metadata 选择 provider。
- [ ] 默认显示 metadata 面板：名称、类型、版本、大小、生产 Agent、project visibility、artifact ref。
- [ ] 支持显式加载内容。
- [ ] 支持 loading / error / unsupported 状态。
- [ ] 支持“下载”“复制 artifact ref”“复制当前版本 ref”。
- [ ] 支持大文本只加载前 N KB，并提示“加载更多/下载完整文件”。

第一批 provider：

- [ ] `TextPreviewProvider`：txt/log/plain text。
- [ ] `MarkdownPreviewProvider`：md/markdown。
- [ ] `JsonPreviewProvider`：json pretty view + raw view。
- [ ] `CodePreviewProvider`：yaml/js/ts/go/py 等只读代码视图。
- [ ] `ImagePreviewProvider`：png/jpg/jpeg/gif/webp/svg。
- [ ] `MediaPreviewProvider`：audio/video 原生控件。
- [ ] `PdfPreviewProvider`：PDF.js 或浏览器 iframe fallback。

验收：

- [ ] artifact 列表打开速度不受 artifact 数量影响。
- [ ] 打开 1000 个章节 artifact 的项目时，不自动下载正文。
- [ ] 点击某个 artifact 后才发起内容请求。
- [ ] 文本大文件不会一次性撑爆浏览器。

### P1：后端预览转换与缓存

对浏览器不能直接优雅预览的文件，后端生成 preview artifact。

TODO：

- [ ] 新增 preview API：

```text
POST /api/artifacts/{artifact_id}/preview
GET  /api/artifacts/{artifact_id}/preview
```

- [ ] 返回 preview 状态：

```json
{
  "status": "ready | pending | unsupported | failed",
  "preview_artifact": "user:preview__...pdf",
  "mime_type": "application/pdf",
  "source_artifact": "user:xxx.docx",
  "source_version": 3
}
```

- [ ] 预览转换结果保存为 artifact。
- [ ] preview artifact 命名稳定，包含 source artifact + version hash。
- [ ] 转换失败保存错误摘要，不反复重试。
- [ ] 增加大小限制、超时、并发限制。

候选转换能力：

- PDF：直接 PDF.js 展示。
- Office：LibreOffice headless 转 PDF/HTML。
- CSV/XLSX：后端抽样转 JSON table preview。
- DOCX/PPTX：优先转 PDF，保留下载原文件。
- HTML：前端 sandbox iframe，禁止脚本或严格 CSP。

验收：

- [ ] DOCX/PPTX/XLSX 可以生成预览 artifact。
- [ ] 同版本重复打开直接命中缓存。
- [ ] 新版本 artifact 会生成新的 preview artifact。
- [ ] 转换失败不会影响原始 artifact 下载。

### P2：Office Provider 可插拔

如果后续需要高保真 Office 预览或编辑，再接第三方文档服务。

候选：

- ONLYOFFICE Docs：适合高保真 Office viewer/editor，集成成本中等，需要部署 Document Server。
- Collabora Online：适合自托管和 LibreOffice/WOPI 生态，偏企业文档协作。
- kkFileView：适合独立文件预览服务，但不建议作为平台核心依赖；可作为可选 provider。

TODO：

- [ ] 定义 `OfficePreviewProvider` 配置：

```yaml
preview:
  office_provider: disabled | libreoffice_pdf | onlyoffice | collabora | kkfileview
  lazy_load: true
  cache_as_artifact: true
  max_source_bytes: 52428800
```

- [ ] 抽象 provider adapter，不让业务 UI 直接依赖某个服务 SDK。
- [ ] 所有 provider 都必须走平台 artifact 权限校验。
- [ ] 第三方服务只能拿到短期签名 URL 或后端代理 URL。
- [ ] 禁止把内部 artifact 存储路径、secret、token 暴露给前端。

验收：

- [ ] 切换 provider 不影响 ArtifactPreviewHost API。
- [ ] 没有 provider 时仍可下载原文件或看到 unsupported 状态。
- [ ] provider 异常时可降级到 cached PDF preview。

### P3：AI 友好的结构化预览

视觉预览服务于人，AI 处理需要结构化内容。

TODO：

- [ ] 引入文档解析 provider，例如 Docling/LibreOffice/PDF text extraction。
- [ ] 将解析结果保存为 `artifact.extracted.md` 或 `artifact.extracted.json`。
- [ ] UI 里区分“视觉预览”和“AI 可读提取结果”。
- [ ] Agent 只能通过明确 tool 加载提取结果，不能因为用户打开预览而自动进入模型上下文。

验收：

- [ ] PDF/DOCX 可以生成 AI-readable markdown。
- [ ] 用户能看到提取质量和来源版本。
- [ ] 大文件抽取可分页或分 chunk。

## 组件边界

### 前端

建议新增：

```text
agentkit-web/src/app/components/artifact-preview/
  artifact-preview-host.component.ts
  artifact-preview-host.component.html
  artifact-preview-host.component.scss
  preview-provider.ts
  providers/text-preview.provider.ts
  providers/markdown-preview.provider.ts
  providers/json-preview.provider.ts
  providers/pdf-preview.provider.ts
  providers/media-preview.provider.ts
```

`artifact-tab` 只负责列表、过滤、分页和打开动作，不承担复杂预览渲染。

### 后端

建议新增：

```text
agentkit/internal/artifactpreview/
  service.go
  types.go
  converters/
```

后端只做：

- metadata 解析；
- preview artifact 查找；
- 转换任务创建；
- 缓存命中；
- 权限和大小限制。

## 安全与权限

- HTML 预览必须 sandbox。
- Office/第三方预览必须通过后端代理或短期签名 URL。
- 预览服务不得访问任意本地路径。
- 转换任务必须有超时和文件大小限制。
- preview artifact 必须继承 source artifact 的 project/user 权限。
- 失败日志不能包含 secret 或内部绝对路径。

## 配置草案

```yaml
preview:
  enabled: true
  lazy_load: true
  text_max_bytes: 524288
  cache_as_artifact: true
  converters:
    office:
      provider: libreoffice_pdf
      timeout_seconds: 60
      max_source_bytes: 52428800
    pdf:
      provider: pdfjs
    table:
      max_rows: 500
      max_columns: 100
```

## 开发顺序

1. P0 前端 preview host 和基础 provider。
2. 将 artifact tab 的现有预览逻辑迁移到 preview host。
3. 加文本大文件懒加载和截断。
4. 加 PDF.js。
5. P1 后端 preview artifact API。
6. 加 LibreOffice PDF 转换。
7. 加 Office provider adapter。
8. 后续按需接 ONLYOFFICE / Collabora / kkFileView。

## 当前决策

短期不选 kkFileView 作为核心方案。它可以作为可插拔 provider，但平台预览能力必须由我们自己的 `ArtifactPreviewHost + PreviewProvider` 抽象承载。

