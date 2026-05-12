# Acceptance Checklist

1. Build the binary with `go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge`.
2. Start Chrome with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir="$HOME/wxpusher-bridge-chrome-profile"`.
3. Confirm `curl -sS http://127.0.0.1:9222/json/version` returns Chrome metadata.
4. Confirm the WxPusher extension is installed and bound in this dedicated Chrome data directory.
5. Copy `configs/config.example.toml` to `config.local.toml`.
6. Set `receiver.extension_id` to the installed WxPusher extension ID from `chrome://extensions`.
7. Set `WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL` in the shell or LaunchAgent environment.
8. Start the bridge with `bin/wxpusher-bridge run -config config.local.toml`.
9. Confirm `app_events` records `chrome_receiver_worker_attached` and `chrome_receiver_ready`.
10. Send a WxPusher test message without a link.
11. Confirm SQLite has one row in `messages` and a completed original delivery.
12. Confirm WeCom receives the original message.
13. Send a WxPusher test message containing `https://example.com`.
14. Confirm SQLite has an enrichment task and enrichment result.
15. Confirm the screenshot file exists under the configured screenshot directory.
16. Confirm WeCom receives the enriched follow-up message.
17. Stop Chrome and confirm receiver failure events are recorded.

Useful SQLite checks:

```bash
sqlite3 /var/lib/wxpusher-bridge/bridge.db 'select id,qid,msg_type,received_at from messages order by id desc limit 5;'
sqlite3 /var/lib/wxpusher-bridge/bridge.db 'select id,kind,status,attempts,last_error from delivery_attempts order by id desc limit 5;'
sqlite3 /var/lib/wxpusher-bridge/bridge.db 'select id,url,status,attempts,last_error from enrichment_tasks order by id desc limit 5;'
sqlite3 /var/lib/wxpusher-bridge/bridge.db 'select kind,message,created_at from app_events order by id desc limit 10;'
```

## Local Verification Log

- `go test ./...`: PASS
- `go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge`: PASS
- Manual Chrome CDP attach test: not run in this environment
- Manual WxPusher message test: not run in this environment
- Manual WeCom delivery test: not run in this environment
- Manual Headless Chrome screenshot test: not run in this environment
