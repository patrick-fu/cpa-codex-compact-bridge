# cpa-codex-compact-bridge

[![Build Linux Plugin](https://github.com/patrick-fu/cpa-codex-compact-bridge/actions/workflows/build-linux-plugin.yml/badge.svg?branch=main)](https://github.com/patrick-fu/cpa-codex-compact-bridge/actions/workflows/build-linux-plugin.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[English README](README.md)

一个 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 原生插件：让具备能力的模型继续使用 Codex 原生 remote compaction，同时把明确配置的第三方模型桥接到普通摘要调用。

![模型选择：原生支持 remote compaction 的模型保持透传，第三方模型通过 bridge 处理](docs/assets/model-selection.webp)

## 我们遇到了什么问题

同一个 Codex Provider 可以通过同一套 CLIProxyAPI 端点同时暴露官方 GPT 与第三方模型。Codex 在 Provider 层决定使用 local compaction 还是 remote compaction，但真正的 compact 能力取决于具体上游模型。

开启 remote compaction 后，Codex 会使用专门的协议请求：

- 旧版 remote compact 调用 `POST /v1/responses/compact`；
- 当前 remote compact 在流式 `/v1/responses` 请求末尾追加 `compaction_trigger`；
- 后续轮次会回放返回的 `compaction` 状态。

具备原生能力的端点可以理解这些请求。很多第三方 Provider 只接受普通模型请求，不支持上述 compact 传输或回放状态，因此长会话会在真正需要压缩时失败。

把 Codex Provider 改名、强制走 local compaction 虽然能避开 remote 请求，但会同时改变该 Provider 下所有模型的行为：原生支持 remote compaction 的 GPT 模型也会失去原有路径。

## 这个项目要解决什么

把“是否具备 compact 能力”的判断，从一个 Provider 级开关移到 CLIProxyAPI 内明确的逐模型规则：

- 已原生支持 remote compaction 的模型继续走 CPA 原生路径；
- 只桥接运维者明确选择的第三方模型；
- V1、V2 都返回 Codex 期望的 compact 结构；
- 后续 continuation 可用，同时不向第三方转发外来的 opaque 状态；
- 无法安全续接时 fail closed。

## 我们怎么解决

插件按原始客户端请求模型依次匹配规则，首个命中生效。

```text
Codex compact 请求
        |
        +-- passthrough 规则 --> CPA 原生 compact 路径 --> 原生 Provider
        |
        `-- bridge 规则 ------> 普通摘要请求 --> cpa_compact_* item
                                               |
后续普通轮次 <---------------------------------'
        |
        `-- 在第三方 Provider 协议转换前，
            把 cpa_compact_* 还原为普通 user 摘要
```

模型命中 `bridge` 时，插件会：

1. 识别旧版 V1、当前 V2 与后续 replay 请求；
2. 移除 compact 协议产物，通过 CPA 的 `host.model.execute` 路径，让配置的摘要模型压缩当前会话窗口；
3. 缺省使用当前 Codex local compaction 提示词，也允许通过 `compact_prompt` 覆盖；
4. 返回带 `cpa_compact_*` 标记的标准 `compaction` item——V1 返回 compact 后的 output window，V2 返回规定的 SSE events；
5. continuation 时只把插件自己生成的明文状态转换为普通 user 摘要。

模型命中 `passthrough` 时，插件不接管请求，CPA 原有原生路径保持不变。

## 范围与限制

- 本项目桥接的是 **Codex Responses remote compaction**。它不是 Codex local compact 的服务端实现，也不处理 Claude Messages compaction。
- Bridge 摘要目标是接近 Codex local compaction 的连续性，但摘要由所选模型生成，并非 Provider 原生 compact 状态。
- 插件把原生 compaction item 视为 opaque、且与其 Provider / 模型 lineage 绑定；不会解密、转换或迁移它们。
- 如果会话已经包含外来的原生 compact 状态，应继续使用原始兼容模型，或通过文本 handoff 新开会话。把该会话切到 bridge 第三方模型会按设计 fail closed。
- Bridge item 的 `encrypted_content` 保存的是**明文**摘要。`cpa_compact_*` 只是兼容标记，不提供加密、认证或完整性保护。

## 使用场景

### 原生 GPT 主会话 + 第三方 sub-agent

GPT 会话保持 native passthrough，独立的第三方 sub-agent 会话命中 bridge。两类模型可以共享同一个 CPA 端点，但不会混用 opaque compact 状态。

### 一个 Codex Provider 暴露混合模型目录

原生模型与第三方模型并列提供。每个新会话按请求模型命中自己的规则，不需要改 Provider 名称或反复切换全局 compact 行为。

## 状态

- `main` CI 会运行插件单测与 `go vet`，构建 linux/amd64 c-shared 候选，并基于 CLIProxyAPI **v7.2.125** 精确源码运行真实 CPA 集成测试。
- 插件 module 当前依赖 CLIProxyAPI **v7.2.120** SDK；与 **v7.2.125** 的兼容性由集成 gate 覆盖。
- 集成测试覆盖 V1、V2 HTTP/SSE、HTTP 与流式 replay、WebSocket V2 continuation、fail-closed 与 passthrough 隔离。
- test-cpa 真实 Provider 评测（包括原生 V2 / bridge V2 / local 对比）记录见[测试矩阵](docs/research/test-cpa-real-session-matrix-2026-08-13.md)。
- 稳定版本：[v0.1.2](https://github.com/patrick-fu/cpa-codex-compact-bridge/releases/tag/v0.1.2)，发布 linux/amd64 产物及 sha256sum 兼容 checksum 文件。
- macOS、Windows、非 amd64 架构及其他 CPA 版本尚未验证。

## 安装

下载并校验 linux/amd64 Release：

```bash
curl -LO https://github.com/patrick-fu/cpa-codex-compact-bridge/releases/download/v0.1.2/cpa-codex-compact-bridge_0.1.2_linux_amd64.zip
curl -LO https://github.com/patrick-fu/cpa-codex-compact-bridge/releases/download/v0.1.2/checksums.txt
sha256sum -c checksums.txt
unzip cpa-codex-compact-bridge_0.1.2_linux_amd64.zip
```

使用版本化文件名安装解压出的动态库：

```text
<plugin-dir>/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so
```

reload 或重启 CPA 后，通过 `GET /v0/management/plugins` 确认版本为 `0.1.2`、`registered: true`、`effective_enabled: true`。仅发现新文件时，CPA 可能先更新所选路径，但尚未替换已加载实例。

如需从源码构建：

```bash
cd plugin
go build -buildmode=c-shared -o cpa-codex-compact-bridge.so .
```

macOS 使用 `.dylib`，Windows 使用 `.dll`，目录替换为对应 `<GOOS>/<GOARCH>`。Release CI 覆盖这些平台前均视为实验性。

## 配置

```yaml
plugins:
  enabled: true
  dir: /absolute/path/to/plugins
  configs:
    cpa-codex-compact-bridge:
      enabled: true
      priority: 100
      # 可选。缺省使用当前 Codex local compaction 提示词。
      compact_prompt: "为下一个 coding agent 生成摘要。"
      rules:
        # 明确配置的第三方 bridge 规则。
        - match: "deepseek-*"
          action: bridge
          summary_model: "deepseek-chat"
        # 原生 remote compact 保持不变。
        - match: "gpt-*-codex*"
          action: passthrough
      # 保守缺省值：未知模型不自动 bridge。
      on_no_match: passthrough
```

规则对原始客户端请求模型使用区分大小写的 glob 匹配。`summary_model` 缺省使用请求模型。`compact_prompt` 缺省使用插件内置提示词；如果 Codex 客户端配置了自定义 compact prompt，需要在这里显式重复，因为 remote compact 请求不会传递该客户端覆盖项。

CLIProxyAPI **Home 模式会禁用 plugin executor routing**，因此不能与 `bridge` 规则同时使用。

完整契约见[配置文档](docs/configuration.md)。

## 安全说明

所选摘要 Provider 会收到生成摘要所需的会话窗口。若该 Provider 无权处理相关数据，请勿为其启用 `bridge` 规则。

插件会以 `compact_bridge_failed` fail closed，不会把不支持的 compact 请求或未知原生状态转发给第三方。部署前请阅读 [SECURITY.md](SECURITY.md)，漏洞通过 GitHub private security advisory 报告。

## CPA Plugin Store

本插件**尚未列入**[官方 CPA Plugin Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store)，因此目前不能通过 CPA 内置商店安装。

符合商店格式的 v0.1.2 Release 与 test-cpa 验收均已完成。剩余上架动作只有提交修改 `registry.json` 的 PR。当前清单见[商店准入报告](docs/research/cpa-plugin-store-publication.md)。

## 验证

```bash
(cd plugin && go test ./... && go vet ./...)
(cd integration && CPA_SOURCE_DIR=/path/to/CLIProxyAPI go test ./... -count=1)
```

集成测试会构建真实 CPA 与 c-shared 插件；CI 自动提供固定版本的 CPA checkout。

## 文档

- [协议契约](docs/compact-protocol.md)
- [配置](docs/configuration.md)
- [领域术语表](CONTEXT.md)
- [架构决策：明文 bridge compaction item](docs/adr/0001-use-plaintext-v2-compaction-items.md)
- [CPA Plugin Store 准入状态](docs/research/cpa-plugin-store-publication.md)

## 贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md) 与[行为准则](CODE_OF_CONDUCT.md)。

## 许可证与免责声明

MIT —— 见 [LICENSE](LICENSE)。第三方声明见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)。

本项目是独立社区软件，与 OpenAI 或 CLIProxyAPI 无关联，也未经其背书或认证。你需要自行确保 Provider、账号、API key、配额及数据使用符合相应条款。
