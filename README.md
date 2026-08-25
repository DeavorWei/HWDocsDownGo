# 华为产品文档下载管理器 (HWDocsDownGo)

<p align="center">
  <b>基于 Golang + Vue 3 的华为企业产品文档（HDX / CHM / PDF）全量爬虫与多线程批量下载管理器</b>
</p>

---

## 🌟 核心功能与亮点

- ⚡ **在线全自动深度爬虫**：
  - 支持 **大类 ➔ 产品线 ➔ 型号树 ➔ 细分版本 ➔ 产品文档** 的全链路自动化级联递归爬取；
  - 深度解析 `second-item` 树状聚合数据，单产品线即可秒级收录上千篇文档。
- 🛡️ **网关安全防护兼容与智能熔断**：
  - 针对华为官方网关安全防护（WSF Check），支持在 Web 界面一键配置浏览器 Cookie 凭据；
  - 具备 **自动拦截检测与智能熔断机制**，遇到拦截即刻暂停爬取并输出清晰的排查指引，避免无意义请求。
- 📊 **Uber Zap 高性能日志架构**：
  - **彩色控制台输出**：终端日志清晰分级，关键指标与 HTTP 标头一目了然；
  - **日志滚动归档**：自动持久化存储于 `HWDDGoData/logs/hwdocsdown.log`，支持按 20MB 大小自动轮转切分；
  - **WebSocket 实时推流**：Web 页面控制台实时接收黑底绿字推流日志，提供极佳的可视化体验。
- 🚀 **官方直链解析与多线程断点续传**：
  - 调用官方 `doc/file-info` 接口动态解析 CDN 高速直链；
  - 支持 HTTP Range 断点续传、并发控制、多任务队列、实时下载速率与百分比监控。
- 🔍 **本地文档智能扫描与双向打标**：
  - 自动递归扫描本地下载目录，基于文件名、NID、格式进行精确与模糊匹配；
  - 自动在 Web 端高亮标注 **【已下载】** 绿色徽章，支持在页面上一键唤起系统资源管理器定位本地文件。
- 📦 **纯 Go 驱动，零外部依赖**：
  - 采用纯 Go 实现的 SQLite 驱动（`glebarez/sqlite`），无需安装 GCC，无 CGO 依赖；
  - 前端基于 Vue 3 SPA + Tailwind CSS 构建，利用 Go `embed.FS` 打包为单个独立可执行文件，开箱即用。

---

## 📁 目录与数据规范

为保证工作区整洁，程序运行时产生的所有业务数据均统一存放在 `HWDDGoData/` 独立数据目录下：

```text
HWDocsDownGo/
├── build/                      # 编译输出目录
│   └── HWDocsDownGo.exe        # 主执行程序
├── HWDDGoData/                 # 运行时数据目录 (已在 .gitignore 中忽略)
│   ├── hwdocs.db               # SQLite 本地数据库 (存储全量分类与文档元数据)
│   ├── Downloads/              # 文档默认下载保存目录
│   └── logs/                   # Zap 滚动日志归档目录
│       └── hwdocsdown.log      # 系统运行日志
├── cmd/server/                 # 服务启动入口
├── internal/                   # 核心业务模块 (api, config, crawler, downloader, logger, scanner, store)
├── web/                        # 前端 SPA 页面与静态资源
└── build.bat                   # Windows 一键编译批处理脚本
```

---

## 🚀 快速启动

### 方式 1: 使用批处理脚本一键构建 (推荐)

在项目根目录下双击运行 `build.bat`，或在 Windows PowerShell / CMD 中执行：

```cmd
.\build.bat
```

编译完成后，直接运行生成的可执行程序：

```cmd
.\build\HWDocsDownGo.exe
```

启动后，程序会自动在默认浏览器中打开管理页面：**`http://127.0.0.1:8088`**。

### 方式 2: 使用 Go 命令行直接运行

```powershell
go run cmd/server/main.go
```

### 💡 常用启动参数

| 参数名 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `--port` | `8088` | 指定 Web 服务监听端口 |
| `--db` | `./HWDDGoData/hwdocs.db` | 自定义 SQLite 数据库文件路径 |
| `--log` | `./HWDDGoData/logs` | 自定义日志保存目录 |
| `--debug` | `false` | 是否开启详细 Debug 调试日志 |
| `--no-browser` | `false` | 启动后不自动在默认浏览器中打开网页 |

示例：
```powershell
.\build\HWDocsDownGo.exe --port=9000 --debug
```

---

## 📖 使用指南

### 1. 配置浏览器 Cookie（只需操作一次）
由于华为官方文档列表接口启用了客户端安全环境校验（WSF Check），建议在使用爬虫前配置一次 Cookie：
1. 浏览器打开华为官网任意产品文档页（如：`https://support.huawei.com/enterprise/zh/switches/cloudengine-58-68-78-88-98-pid-252837181`）；
2. 按 `F12` 打开开发者工具，在 **网络 (Network)** 中复制任意发往 `support.huawei.com` 请求头中的 **`Cookie:`** 值；
3. 打开本系统左侧 **【系统设置】**，粘贴到 **“自定义 Cookie”** 输入框中并保存。

### 2. 爬取产品文档
- **全量深度爬取**：进入 **【爬虫中心】**，点击 **【一键全量深度爬取】**，系统将自动抓取全部大类、产品线及数千篇文档；
- **定向单线爬取**：在产品线列表中选择指定产品线（如 *交换机*），点击 **【开始爬取该产品线】** 进行针对性抓取。

### 3. 筛选与下载
- 进入 **【文档下载中心】**；
- 可通过顶部格式 Tab（**全部 / HDX / CHM / PDF / ZIP**）或分类级联下拉框进行筛选；
- 支持勾选多篇文档进行 **【批量下载】**，在 **【下载任务队列】** 中实时查看进度、速率与完成状态。

---

## 🛠️ 技术架构

- **后端核心**：Go 1.26+ / Gin Web Framework / Gorilla WebSocket
- **日志引擎**：`go.uber.org/zap` + `gopkg.in/natefinch/lumberjack.v2`
- **数据持久化**：GORM + `github.com/glebarez/sqlite` (纯 Go SQLite 驱动，跨平台零 CGO)
- **前端界面**：Vue 3 (Composition API) + Tailwind CSS + FontAwesome 6 (通过 `embed.FS` 嵌入打包)
- **操作系统支持**：Windows 10/11 64位 (原生支持)、Linux、macOS

---

## 📄 开源协议

本项目基于 MIT License 协议开源。
