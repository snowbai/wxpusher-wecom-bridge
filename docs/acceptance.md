# Acceptance Checklist

1. Build the binary with `go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge`.
2. Create a local config from `configs/config.example.toml`.
3. Set `WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL` in the shell or systemd env file.
4. Import identity with `bin/wxpusher-bridge import-json -config config.local.toml -file identity.local.json` or `bin/wxpusher-bridge import-chrome -config config.local.toml -profile "<profile>" -extension-id "<id>"`.
5. Start the bridge with `bin/wxpusher-bridge run -config config.local.toml`.
6. Send a WxPusher test message without a link.
7. Confirm SQLite has one row in `messages` and a completed original delivery.
8. Confirm WeCom receives the original message.
9. Send a WxPusher test message containing `https://example.com`.
10. Confirm SQLite has an enrichment task and enrichment result.
11. Confirm the screenshot file exists under the configured screenshot directory.
12. Confirm WeCom receives the enriched follow-up message.
13. Stop network access and confirm reconnect events are recorded in `app_events`.

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
- Manual WxPusher message test: not run in this environment
- Manual WeCom delivery test: not run in this environment
- Manual Headless Chrome screenshot test: not run in this environment
