# cpa-codex-compact-bridge

[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件：让 Codex 保持开启 remote compaction，同时把**不支持**该协议的第三方模型改走普通摘要调用。

> ⚠️ **与 OpenAI 及 CLIProxyAPI 无关联。** 本项目是独立社区软件，未经 OpenAI Inc. 或 CLIProxyAPI 维护者背书或认证。"Codex" 与 "OpenAI" 是各自所有者的商标。

English version: [README.md](README.md).

## 工作原理

Codex remote compaction 有两种协议形态，外加一个回放步骤。bridge 分别处理：

- **V1** — 拦截非流式 `POST /v1/responses/compact` 端点，通过普通模型请求生成摘要，返回保留的 user/developer 历史加一条带标记的 `compaction` item。
- **V2** — 识别流式 `/v1/responses` 请求末尾的 `compaction_trigger`，通过普通模型请求生成摘要，返回 Codex 接受的 `compaction` SSE item 与 `response.completed`。
- **回放（Replay）** — 在后续普通轮次，把插件自己的 `cpa_compact_*` 明文状态转换回普通 user summary，避免 Responses→Chat 转换时丢失上下文。
- **透传（Passthrough）** — 你配置为原生支持 compact 的模型保持原样转发。

V1 与 V2 compaction item 的 `encrypted_content` 字段保存的是**明文**摘要。它是兼容标记（`cpa_compact_*` ID），不是加密，也不是可信、完整性或机密性边界。见[安全说明](#安全说明)。

## 状态

- **已确认：** CLIProxyAPI **v7.2.120**、**linux/amd64** —— 构建与集成 CI。
- **兼容回归已通过：** 使用 CLIProxyAPI **v7.2.125** 精确源码在本地真实 CPA 集成 harness 验证；这不代表已发布 linux/amd64 产物。
- **预编译二进制** linux/amd64 发布后将可用于 [GitHub Releases](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases)。见[安装](#安装)。
- **实验性 / 未验证：** 其他 CLIProxyAPI 版本、macOS/Windows 构建、其他 CPU 架构。需自行编译对应平台的动态库，这些平台的行为不保证。

## 免责声明

- 插件不审查、不认证上游 Provider。**你需自行确保**对任何 Provider、账号、API key、速率限制与配额的使用符合相应服务条款。本项目不提供任何法律、监管或合同合规保证。
- 当模型命中 `bridge` 规则时，配置的摘要 Provider 会收到**压缩/摘要后的会话上下文**。
- V1 与 V2 的明文摘要置于 `encrypted_content`；它并非加密（见上）。

## 安装

**Linux amd64（推荐）：** 从源码构建，或在 [GitHub Releases](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases) 发布后下载预编译二进制。

```bash
cd plugin
go build -buildmode=c-shared -o cpa-codex-compact-bridge.so .
```

将产物放到 CPA 的插件发现目录：

```text
<plugin-dir>/linux/amd64/cpa-codex-compact-bridge.so
```

**其他平台（实验性）：** macOS 使用 `.dylib`、Windows 使用 `.dll`，目录按 `<GOOS>/<GOARCH>` 替换。

动态库 basename（去扩展名）即插件 ID。该插件按 CLIProxyAPI `v7.2.120` SDK 编译。

## CPA Plugin Store

本插件**尚未列入** [CPA Plugin Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)。目前无法通过 CPA 内置的插件商店命令安装，请使用上述手动安装步骤。

预编译二进制发布和商店提交已在计划中；发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md#maintainer-releases)。

## 配置

```yaml
plugins:
  enabled: true
  dir: /absolute/path/to/plugins
  configs:
    cpa-codex-compact-bridge:
      enabled: true
      priority: 100
      # 可选。缺省时使用 Codex 内置的 local compact 提示词。
      compact_prompt: "为下一个 coding agent 生成摘要。"
      rules:
        # 第三方模型：bridge
        - match: "glm-*"
          action: bridge
          summary_model: "glm-5.2"
        # 原生 remote compact 模型：保持 CPA 原始路径
        - match: "gpt-*-codex*"
          action: passthrough
      on_no_match: passthrough
```

规则按原始客户端请求模型名、**区分大小写**的 glob 从上到下首个命中。`bridge` 让插件接管该模型的 compact 轮次并启用回放规范化；`passthrough` 让所有请求留在 CPA 内置路由。Provider 是否有原生 compact 能力由你通过 `bridge` / `passthrough` 规则表达——插件不会猜测上游能力。

`summary_model` 可选；缺省时用请求模型发起普通摘要请求。`compact_prompt` 也可选；缺省或为空时，插件使用当前 Codex local compact 的默认提示词。只有当客户端配置了自定义 Codex `compact_prompt`，且希望 Bridge 使用同一指令时才需要显式设置。远端 compact 请求不会携带客户端配置的提示词，因此插件无法自动发现该覆盖。`on_no_match` 仅接受 `passthrough`。

CLIProxyAPI **Home 模式会禁用 plugin executor routing**，因此不能与 `bridge` 规则同时使用。

## 安全说明

详见 [SECURITY.md](SECURITY.md)。要点：

- `encrypted_content` 是明文，不是安全边界。
- 摘要 Provider 会收到你的压缩上下文。
- 摘要生成失败时插件 fail-closed（`compact_bridge_failed`），不会把 compaction 协议项转发给不支持的上游。
- 漏洞请私下报告，不要开公开 issue。

## 验证

```bash
(cd plugin && go test ./... && go vet ./...)
CPA_SOURCE_DIR=/path/to/CLIProxyAPI (cd integration && go test ./... -count=1)
```

集成测试会构建真实 CPA 与 c-shared 插件，覆盖 V1、V2 HTTP/SSE、HTTP 回放、流式回放，以及带 `previous_response_id` 的 WebSocket V2 compact 轮次及后续 continuation。
它需要通过 `CPA_SOURCE_DIR` 指定本地 CLIProxyAPI checkout；CI 会自动提供固定版本。

## 文档

- [协议契约](docs/compact-protocol.md)
- [配置](docs/configuration.md)
- [领域术语表](CONTEXT.md)
- [架构决策：明文 bridge compaction item](docs/adr/0001-use-plaintext-v2-compaction-items.md)

## 贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md)。请遵守[行为准则](CODE_OF_CONDUCT.md)。

## 许可证

MIT —— 见 [LICENSE](LICENSE)。第三方声明见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)。
