# Compact bridge smoke test

`compact-smoke.sh` 在 CPA 重启后验证 `/v1/responses` 的 remote compaction v2 SSE 输出，检查唯一 `compaction` item、完成事件及摘要内容。API key 不会写入文件或输出。退出码 0 表示全部通过；退出码 3 表示桥接判据全通过但摘要未保留哨兵（输出 `PASS(without sentinel)`）；其他非 0 表示桥接失败。

普通模式：`./compact-smoke.sh --base-url URL --api-key KEY --model MODEL [--timeout SEC]`

拒绝逻辑模式：`./compact-smoke.sh --expect-failure --base-url URL --api-key KEY --model MODEL`，发送非末项 trigger，断言 400 `invalid_compaction_state`。

可使用 `CPA_BASE_URL`、`CPA_API_KEY`、`CPA_MODEL`、`CURL_BIN` 环境变量；仅依赖 POSIX `sh`、`curl`、`sed`、`awk`、`grep`。

# Release notes

`release-notes.sh` 按当前严格 semver tag 与其可达的上一严格 semver tag 之间的实际 Git 提交，生成可审计的发布说明；每个提交均按完整 SHA 和 subject 列出。严格 SemVer 不允许前导零，且任一可达 tag 版本不低于当前版本会使生成失败，避免版本回退重复发布范围。发布工作流以 tag push 时的不可变提交 SHA 构建，发布前再确认远端 tag 仍指向该 SHA；生成后会重新计算并逐字校验说明，避免直推提交被 GitHub 的自动说明遗漏。

```sh
sh scripts/release-notes.sh --tag v0.1.4 --output /tmp/release-notes.md
sh scripts/release-notes.sh --verify --tag v0.1.4 --notes /tmp/release-notes.md
sh scripts/test-release-notes.sh
```
