# CPA 压缩桥接 v0.1.4 发布与验收 Runbook

本 Runbook 的目标是将已验收的 linux/amd64 构建产物替换到 Zeabur 生产 CPA，完成重启、自检、24 小时观测，并在任一摘要桥接失败时回滚。以下远端命令均为命令示例；本次文档交付不执行上传、替换或重启。

## 0. 变量与操作边界

在本地工作树执行：

```sh
cd /Users/patrickfu/dev/cpa-codex-compact-bridge
VERSION=0.1.4
SERVICE_ID=69d913e9e8ec40d5bceac923
PROJECT_ID=69d91372e8ec40d5bceac90c
PLUGIN_DIR=/CLIProxyAPI/plugins/linux/amd64
PLUGIN_NAME=cpa-codex-compact-bridge-v${VERSION}.so
OLD_PLUGIN=/CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so
NEW_PLUGIN="${PLUGIN_DIR}/${PLUGIN_NAME}"
```

生产变更需要在维护窗口内进行。替换插件后必须 `ApplyConfig`/重启，期间所有运行中的会话会中断。

## 1. 前置确认

确认待发布源码在指定分支及提交上，并保留证据：

```sh
git switch p/patrick/compact-summary-request-shape
git rev-parse --verify a8bafc8^{commit}
git merge-base --is-ancestor f5027e0 a8bafc8
git log -1 --format='%H%n%s' a8bafc8
git status --short
```

预期：`a8bafc8` 能解析为提交，且 `f5027e0` 是其祖先；工作树只包含已知变更。无法解析、祖先关系不成立或有未知变更时停止发布，先固定待发布提交。

只读取证线上当前插件文件和 CPA 核心二进制中的版本字符串：

```sh
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'ls -l /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so; grep -ao "v7\\.2\\.[0-9][0-9]*" /CLIProxyAPI/CLIProxyAPI | sed -n "1,10p"'
```

预期：插件路径存在；核心二进制字符串证据包含线上已测得的 `v7.2.147`。路径不存在或版本字符串与变更评估基线不同，停止发布并重新评估兼容性。不要用 `CPA_API_KEY` 访问 `/v0/management/*` 做版本核验，该方式返回 401。

## 2. 构建与产物校验

本地构建 linux/amd64 候选并生成可留档 hash：

```sh
cd /Users/patrickfu/dev/cpa-codex-compact-bridge
VERSION=0.1.4
mkdir -p dist/linux-amd64
(
  cd plugin
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared \
    -ldflags "-X main.pluginVersion=$VERSION" \
    -o ../dist/linux-amd64/cpa-codex-compact-bridge.so .
)
file dist/linux-amd64/cpa-codex-compact-bridge.so
strings dist/linux-amd64/cpa-codex-compact-bridge.so | grep -F "$VERSION"
sha256sum dist/linux-amd64/cpa-codex-compact-bridge.so | tee dist/linux-amd64/cpa-codex-compact-bridge.so.sha256
```

预期：`file` 显示 Linux 64-bit shared object，`strings` 找到 `0.1.4`，并产生 SHA-256 行。任一检查失败即不上传。

CI 候选路径（仅验证，不部署）：

```sh
BRANCH=p/patrick/compact-summary-request-shape
gh workflow run build-linux-plugin.yml --ref "$BRANCH"
RUN_ID=$(gh run list --workflow build-linux-plugin.yml --branch "$BRANCH" --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN_ID" --exit-status
mkdir -p /tmp/cpa-v0.1.4-ci-candidate
gh run download "$RUN_ID" --dir /tmp/cpa-v0.1.4-ci-candidate
find /tmp/cpa-v0.1.4-ci-candidate -name 'cpa-codex-compact-bridge.so.sha256' -exec cat {} \;
```

预期：CI 通过 unit、vet、linux/amd64 integration gate 后，下载 `dist/linux-amd64/cpa-codex-compact-bridge.so` 及其 `.sha256`。该工作流产物版本是 `0.1.3-rc.<sha>`、仅保留 7 天，不是可部署的 v0.1.4 发布包。发布工作流仅在 `v*` tag push 时运行，构建 linux/amd64、darwin/arm64、darwin/amd64 三个平台的 zip，随后对三份 zip 生成并校验 `checksums.txt`；在 tag 对应提交进入 `main` 前不得以它发布。linux/amd64 是唯一有完整 CPA integration gate 的平台；macOS 仅有构建和 ABI smoke，不能用 macOS 结果代替生产 Linux 验收。

若使用发布包，先校验对应压缩包后解压：

```sh
grep "  cpa-codex-compact-bridge_${VERSION}_linux_amd64.zip$" checksums.txt | sha256sum -c -
unzip "cpa-codex-compact-bridge_${VERSION}_linux_amd64.zip"
sha256sum cpa-codex-compact-bridge.so
```

预期：checksum 返回 `OK`，zip 根目录只有 `cpa-codex-compact-bridge.so`。否则产物不可信。

## 3. 上传与替换

先将新 `.so`、`scripts/compact-failure-count.sh` 与 `scripts/compact-smoke.sh` 传入容器暂存位置，并在容器内核验 SHA-256。当前已知的 `service exec` 只读调用形式无法从仓库资料确认其 stdin 文件上传语义；上传通道与重启按钮/CLI 子命令为**待用户确认**。无论使用 Zeabur 控制台、已获准的上传通道或运维系统，都必须满足以下顺序和等价命令：

```sh
# 容器内写操作：仅在维护窗口、完成 hash 比对后执行。
sha256sum /tmp/cpa-codex-compact-bridge.so
test -f /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so
install -d -m 0755 /CLIProxyAPI/scripts
install -m 0755 /tmp/cpa-codex-compact-bridge.so /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.4.so
install -m 0755 /tmp/compact-failure-count.sh /CLIProxyAPI/scripts/compact-failure-count.sh
install -m 0755 /tmp/compact-smoke.sh /CLIProxyAPI/scripts/compact-smoke.sh
ls -l /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.4.so /CLIProxyAPI/scripts/compact-failure-count.sh /CLIProxyAPI/scripts/compact-smoke.sh
```

预期：`/CLIProxyAPI/scripts` 已存在且两个脚本均为可执行文件，新旧 `.so` 同时存在，且新 `.so` hash 与本地留档一致。旧文件不要预先删除：文件名带版本号让加载目标、回滚目标和事后证据明确；若新文件写入或 hash 校验失败，可在重启前停止而不影响当前 v0.1.2 进程。

## 4. 生效方式与代价

替换文件本身不生效：插件目录不在 watcher 监听范围，必须 `ApplyConfig`，运维上将伴随 CPA 重启。重启会中断所有运行中的会话。

执行前检查：

- 已通知用户维护窗口、开始时间与会话中断影响。
- 新 `.so` 的 SHA-256 已与本地留档逐字一致，新旧版本文件都存在。
- 已确认可执行 `ApplyConfig`/重启的 Zeabur 操作路径，且回滚人员与窗口仍可用。
- 计数脚本已置于容器，并已安排每小时运行一次。

通过已确认的 Zeabur 操作执行 `ApplyConfig`/重启；该操作路径为**待用户确认**。重启完成后，先执行下一节自检，再恢复正常流量。

## 5. 部署后自检与请求形状取证

在容器内运行现有 smoke 脚本。下例不回显 key，前提是容器已通过受控的秘密配置提供 `CPA_BASE_URL`、`CPA_API_KEY`、`CPA_MODEL`；`service exec` 是否会透传本地环境变量尚未从仓库资料核验，不能假定本地 `export` 会生效，具体秘密注入路径为**待用户确认**。

```sh
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'CPA_BASE_URL="$CPA_BASE_URL" CPA_API_KEY="$CPA_API_KEY" CPA_MODEL="$CPA_MODEL" /CLIProxyAPI/scripts/compact-smoke.sh'
```

以 `scripts/compact-smoke.sh --help` 为准：退出码 `0` 表示全部通过；`3` 表示压缩链路通过但摘要未保留 sentinel，不属于桥接故障，记录输出和父日志后继续 24 小时观察，不回滚；其他非零表示真失败，停止放量并按第 7 节回滚。V2 compaction item 的字段顺序不构成判据，必须按字段名验证，不得依赖 `encrypted_content`、`id`、`type` 的相邻或序列化顺序。

摘要嵌套调用不会新建独立的 `v1-responses-*.log`；它会在触发 smoke 的父请求日志中以 `API REQUEST n` 分节记录。用 sentinel 找到父日志并审计该分节：

```sh
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'grep -l "CPA_SMOKE_SENTINEL_7F3A9C" /CLIProxyAPI/logs/v1-responses-*.log | sed -n "1p"'
```

将上一步返回的父日志路径替换为 `PARENT_LOG`。smoke 的父请求是 `API REQUEST 1`，其嵌套摘要调用是 `API REQUEST 2`。以下范围在 `API RESPONSE 2` 前结束，故只检查摘要请求体而不读取响应体；确认它没有 `tools` 与 `tool_choice` 字段：

```sh
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'sed -n "/API REQUEST 2/,/API RESPONSE 2/p" "PARENT_LOG" | grep -n -E "tools|tool_choice"'
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'sed -n "/API REQUEST 2/,/API RESPONSE 2/p" "PARENT_LOG" | grep -n "max_tokens"'
```

预期：第一条命令无输出且退出码为 1，第二条命令输出 `max_tokens` 所在行。这证明摘要请求已无 `tools`/`tool_choice`，且 v7.2.147 已将 bridge 的 `max_output_tokens` 翻译为上游 `max_tokens`。这是本次修复的正向证据，强于仅验证黑盒请求成功。若第一条找到字段或第二条没有 `max_tokens`，保留父日志路径并立即回滚。`PARENT_LOG` 需在命令执行前人工替换为真实路径，避免在远端命令中使用不受控的变量展开。

## 6. 24 小时验收

计数脚本默认追加到 `/CLIProxyAPI/logs/compact-failures.tsv`。在容器 cron 中每小时执行一次，使用 65 分钟 mtime 窗口覆盖调度漂移：

```cron
0 * * * * /CLIProxyAPI/scripts/compact-failure-count.sh --since 65
```

24 小时后只读取 TSV：

```sh
cd /tmp && npx --yes zeabur -i=false service exec --id 69d913e9e8ec40d5bceac923 -- sh -c 'awk '\''{ row[NR % 456] = $0 } END { start = NR - 455; if (start < 1) start = 1; for (i = start; i <= NR; i++) print row[i % 456] }'\'' /CLIProxyAPI/logs/compact-failures.tsv'
```

预期：每小时有 19 行，即使计数为 0 也必须落盘；该行数来自本脚本的实测 TSV 样本，而非手工计算。改后 24 小时内每一行 `compact_bridge_failed` 的计数均为 `0`。出现一次即回滚；smoke 的退出码 `3` 不触发回滚，其他非零按第 5 节作为真失败处理。该持久计数是必要证据，因为 `log_dir_cleaner` 会轮转删除每请求日志。

计数脚本按 `a8bafc8` 的最终失败模板写入以下 `type`：`bridge_generic`、`bridge_encode`、`ordinary_stream`、`multiple_terminal_shapes`、`unknown_terminal_shape`、`upstream_failed`、`upstream_incomplete`、`incomplete_max_output_tokens`、`incomplete_content_filter`、`incomplete_finish_content_filter`、`truncated_length`、`truncated_max_tokens`、`tool_call_root_cause`、`unsupported_output_guard`、`invalid_terminal_status_guard`、`missing_terminal_status_guard`、`no_usable_text`、`oversized` 和总数 `compact_bridge_failed`。`tool_call_root_cause` 是本次修复要消灭的根因签名；名称含 `_guard` 的三类是新护栏触发。`summary exceeds <N> bytes` 的 `N` 来自 `max_summary_bytes`，故只按固定前缀 `summary exceeds ` 计数；其余消息均按固定、已净化的有限枚举匹配。`upstream_incomplete` 从总前缀计数中扣除三种具体 incomplete 形态，避免重复计数。

## 7. 回滚

在维护窗口确认新版本引发失败后，停止流量并使用保留的旧文件重新部署：

```sh
# 容器内写操作：须使用第 3 节已确认的上传/执行通道。
test -f /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so
rm -f /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.4.so
ls -l /CLIProxyAPI/plugins/linux/amd64/cpa-codex-compact-bridge-v0.1.2.so
```

随后通过已确认的 Zeabur `ApplyConfig`/重启路径重启 CPA，并复跑第 5 节 smoke。预期：旧插件恢复加载，smoke 恢复通过。回滚会重新允许 v0.1.4 所拒绝的上游工具调用、不完整/失败终态、无可用文本和超过摘要字节上限等摘要形态进入旧行为；继续观察 `compact_bridge_failed`，不要把回滚视为上述根因已经修复。

## 8. 已知残留风险

- CPA `status` 在 `<= 7.2.125` 无判别力，不能据此判断摘要调用成功或失败。
- `cpa_compact_*` id 泄漏导致的上游 400 属于另一任务。
- 线上当前插件仍为 `v0.1.2`，而 README 稳定版为 `v0.1.3`，两者存在版本落差；本 Runbook 的发布目标为 `v0.1.4`。
- CI 集成 pin 的 CPA v7.2.125 会在 plugin RPC 错误外层把 JSON `code` 重写为 `internal_server_error`：`internal/pluginhost/rpc_client.go:304` 丢弃 `Error.Code`，`sdk/api/handlers/handlers_errors.go` 统一生成错误体。因此该 pin 下 V1 错误体不可能带 `code=compact_bridge_failed`。线上为 v7.2.147，此行为未在本仓库验证；部署时须用 smoke 或日志确认线上 V1 错误体的实际 `code`，不得预先断言。

## 事实来源

- README `81-88`：CI 覆盖范围、稳定版和平台范围；`92-108`：下载校验、版本化安装路径及 management 预期状态；`128-135`：源码构建与 macOS/Windows 说明。
- `.github/workflows/release.yml` `59-139`、`141-220`、`224-280`：linux gate、macOS ABI smoke、三平台 zip 与 checksums 生成。
- `.github/workflows/build-linux-plugin.yml` `50-80`：`dist/linux-amd64/*.so`、SHA-256、集成 gate 和候选保留期限。
- `scripts/compact-smoke.sh` `1-44`、`52-61` 与 `scripts/README.md` `1-9`：smoke 入口、成功/失败判据和凭据不输出。
- `docs/configuration.md` `7-14`、`30-38`：摘要请求约束和 `max_summary_tokens`、`max_summary_bytes`、`append_tool_guard`、`forward_service_tier`、`summary_image_models`。
- `f5027e0`：失败消息字符串的初始来源；`a8bafc8`：经第三轮收口后的最终失败模板，供计数脚本分类。

线上 service/project id、当前插件路径、日志路径、ApplyConfig 行为、management 401、日志嵌套方式、轮转行为及残留风险来自任务已确证环境事实。
