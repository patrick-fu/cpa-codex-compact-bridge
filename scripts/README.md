# Compact bridge smoke test

`compact-smoke.sh` 在 CPA 重启后验证 `/v1/responses` 的 remote compaction v2 SSE 输出，检查唯一 `compaction` item、完成事件及摘要内容。API key 不会写入文件或输出。

普通模式：`./compact-smoke.sh --base-url URL --api-key KEY --model MODEL [--timeout SEC]`

拒绝逻辑模式：`./compact-smoke.sh --expect-failure --base-url URL --api-key KEY --model MODEL`，发送非末项 trigger，断言 400 `invalid_compaction_state`。

可使用 `CPA_BASE_URL`、`CPA_API_KEY`、`CPA_MODEL`、`CURL_BIN` 环境变量；仅依赖 POSIX `sh`、`curl`、`sed`、`awk`、`grep`。
