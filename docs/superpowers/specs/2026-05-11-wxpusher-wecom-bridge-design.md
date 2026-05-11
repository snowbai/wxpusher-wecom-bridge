# WxPusher to WeCom Bridge Design

Date: 2026-05-11

## Goal

Build a Go client that replaces the currently installed WxPusher Chrome extension as the message receiver. The client must receive WxPusher messages, persist them locally, immediately forward the original message to a WeCom robot, then asynchronously fetch linked page content, take screenshots, persist enrichment results, and send a follow-up enriched message.

The implementation must use the existing extension behavior as the protocol reference. It must not invent additional headers, cookies, or browser identity fields.

## Scope

In scope:

- Go long-running client.
- macOS and Linux support.
- Server deployment as one Go binary plus config file plus systemd unit.
- Manual migration of an already installed Chrome extension identity.
- WxPusher WebSocket connection compatible with the current extension.
- Local persistence through a storage interface, with SQLite as the first implementation.
- Original message forwarding to a WeCom robot.
- Async enrichment tasks for URL extraction, linked page text extraction, screenshots, enriched follow-up messages, retries, and failure records.
- Clear local warnings for disconnects, identity expiry, failed push-token updates, and delivery failures.

Out of scope for the first implementation:

- Automatic scanning of all Chrome profiles.
- Multi-instance clustering.
- A web dashboard.
- Alternate alert channels beyond local logs and database state.
- Modifying the existing Chrome extension.

## Architecture

The first version is a single-process modular Go application. Keeping one process avoids extra deployment and coordination complexity while still preserving clear package boundaries.

Packages:

- `cmd/wxpusher-bridge`: CLI entrypoint with `run`, `import-chrome`, `import-json`, and future `bind` subcommands.
- `internal/app`: application composition, lifecycle, signal handling, and error propagation.
- `internal/wxpusher`: WebSocket connection, heartbeat, reconnects, protocol message parsing, and push-token update flow.
- `internal/identity`: identity model and persistence helpers for `deviceUuid`, `deviceToken`, `pushToken`, `platform`, and `version`.
- `internal/store`: storage interfaces and SQLite implementation.
- `internal/dispatch`: durable task queue for original delivery, enrichment, and enriched delivery.
- `internal/wecom`: WeCom robot webhook client and payload builder.
- `internal/browser`: Chrome or Chromium Headless page text extraction and screenshot capture.
- `internal/config`: TOML config loading and validation.
- `internal/logging`: structured local logging.

Message flow:

```text
WxPusher WebSocket
  -> parse notification
  -> persist raw message
  -> enqueue original WeCom delivery
  -> extract URLs
  -> enqueue enrichment tasks
  -> fetch page text and screenshot through Headless Chrome
  -> persist enrichment result
  -> enqueue enriched WeCom delivery
```

## Protocol Compatibility

The client must mirror only the behavior visible in the existing extension.

Hosts:

- API host: `https://wxpusher.zjiecode.com`
- WebSocket host: `wss://wxpusher.zjiecode.com`

WebSocket URL:

```text
wss://wxpusher.zjiecode.com/ws?version=<version>&platform=<platform>
```

When a local `pushToken` exists, append:

```text
&pushToken=<pushToken>
```

Default protocol identity:

- `version`: `1.1.0`, matching the current extension.
- `platform`: `Chrome-Linux` on Linux and `Chrome-Mac` on macOS unless configured otherwise.

HTTP headers:

- `platform`
- `version`
- `deviceToken`
- `Content-Type: application/json;charset=UTF-8`

Cookies:

- The client must not read or send Chrome cookies.
- The extension uses `credentials: 'omit'`; the Go client must follow that behavior.

WebSocket message handling:

- `msgType=201`: heartbeat response. Update the last server heartbeat timestamp.
- `msgType=202`: initialization message. Validate and persist the new `pushToken`, then call the register-device API to update the server-side binding when `deviceUuid` exists.
- `msgType=204`: version update notice. Persist an app event and print a local warning.
- `msgType=20001`: notification message. Persist and dispatch it.

Heartbeat:

- Send an upstream heartbeat body `{"msgType":101}` every 26 seconds.
- If the server heartbeat times out, mark the connection unhealthy and reconnect.

Reconnect:

- Use bounded exponential backoff with jitter.
- Keep the last valid identity until a replacement is fully validated.
- Do not overwrite identity fields with partial or malformed protocol messages.

## Identity Migration

The installed Chrome extension is already receiving messages. The first version should migrate that identity and let the Go client replace the extension.

Identity fields:

- `deviceUuid`
- `deviceToken`
- `pushToken`
- `platform`
- `version`

Supported import paths:

1. `import-chrome --profile <path> --extension-id <id>`
   - Reads only the specified Chrome profile and extension id.
   - Imports extension local storage or `chrome.storage.local` data as available.
   - Does not scan unrelated profiles.

2. `import-json --file identity.json`
   - Imports a manually exported JSON identity file.
   - Validates required fields before saving.

Recovery paths:

- If token or binding problems are detected, print a clear local message telling the operator to run `import-chrome` or `import-json` again.
- A future `bind` command may reproduce the extension's QR-code binding flow as a fallback when Chrome identity migration is no longer possible.

## Storage

Storage is abstracted behind interfaces. SQLite is the first implementation.

Default database path:

```text
/var/lib/wxpusher-bridge/bridge.db
```

Tables:

- `identity`: current identity fields, source, and update timestamps.
- `messages`: raw WxPusher messages with `qid`, `msgType`, `content`, raw JSON payload, receive timestamp, and processing status.
- `delivery_attempts`: WeCom delivery attempts with message id, delivery type, status, attempt count, response body, error, and timestamps.
- `enrichment_tasks`: URL enrichment jobs with URL, message id, status, attempt count, last error, and timestamps.
- `enrichments`: page title, extracted text summary, screenshot path, content fetch status, and completion timestamp.
- `app_events`: disconnects, heartbeat timeouts, identity problems, version notices, delivery exhaustion, and operator-facing errors.

Message deduplication:

- Use `qid` when present.
- If `qid` is absent, use a stable hash of message type, content, and raw payload.

## Dispatch And Enrichment

The dispatch layer runs durable tasks from SQLite so a process restart does not lose work.

Original delivery:

1. Persist the raw message.
2. Enqueue original WeCom delivery immediately.
3. Send a WeCom text or markdown message containing the original content and message id.

Enrichment:

1. Extract URLs from the message content.
2. Create one enrichment task per URL.
3. Use Headless Chrome or Chromium to load the page.
4. Extract page title and readable text summary.
5. Save a screenshot to the configured screenshot directory.
6. Persist the enrichment result.
7. Enqueue an enriched WeCom follow-up message.

Enrichment failures:

- Do not block original delivery.
- Retry according to config.
- Persist the final failure and print a local warning after retries are exhausted.

WeCom delivery failures:

- Retry according to config.
- After retry exhaustion, persist failed status and print a local warning.
- Do not use fallback alert channels in the first version.

## Configuration

Use TOML for the server configuration.

Example:

```toml
[wxpusher]
host = "wxpusher.zjiecode.com"
version = "1.1.0"
platform = "Chrome-Linux"

[storage]
sqlite_path = "/var/lib/wxpusher-bridge/bridge.db"

[wecom]
webhook_url = ""
webhook_env = "WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL"

[browser]
enabled = true
chrome_path = "/usr/bin/chromium"
screenshot_dir = "/var/lib/wxpusher-bridge/screenshots"
timeout_seconds = 20
summary_max_chars = 1000

[retry]
wecom_max_attempts = 5
enrichment_max_attempts = 3
initial_backoff_seconds = 2
max_backoff_seconds = 60
```

Secrets:

- The WeCom webhook URL should be accepted from either config or an environment variable.
- Logs must not print full webhook keys or full device tokens.

## Deployment

First deployment target:

- One Go binary.
- One TOML config file.
- One SQLite database.
- One screenshot directory.
- One systemd service.

Suggested Linux paths:

- Binary: `/usr/local/bin/wxpusher-bridge`
- Config: `/etc/wxpusher-bridge/config.toml`
- Data: `/var/lib/wxpusher-bridge`
- Logs: systemd journal. File logging is out of scope for the first version.

The service must start without Chrome Headless when `[browser].enabled = false`. Missing Chromium must not prevent WebSocket receive or original WeCom forwarding when browser enrichment is disabled.

## Error Handling

Connection errors:

- WebSocket close or heartbeat timeout triggers reconnect.
- Reconnect attempts are logged and recorded in `app_events`.

Protocol errors:

- Malformed JSON or unknown message types are logged.
- Missing required fields in `msgType=202` prevent identity updates.

Identity errors:

- Failed register-device calls mark identity as suspicious but keep the last known valid identity.
- Repeated identity failures print an operator-facing recovery message.

Version notices:

- Server upgrade notices are stored and printed locally.
- If the server response implies the current client version is no longer usable, stop reconnecting until the operator updates config or the binary.

Delivery errors:

- WeCom non-2xx responses and network errors are retried.
- Retry exhaustion is stored and printed locally.

Browser errors:

- Navigation timeout, TLS errors, blocked pages, and screenshot failures only fail the enrichment task.
- Original message delivery remains independent.

## Testing

Unit tests:

- WxPusher message parsing.
- WebSocket URL construction.
- Header construction.
- Heartbeat state transitions.
- URL extraction.
- WeCom payload construction.
- Retry state machine.
- Config validation.

SQLite integration tests:

- Schema migration.
- Identity update.
- Message deduplication.
- Task enqueue and state transitions.
- Delivery attempt recording.

Protocol replay tests:

- Saved `msgType=202` initialization payload updates `pushToken` correctly.
- Saved `msgType=20001` notification payload creates message and tasks correctly.

Manual acceptance:

1. Import identity from the installed Chrome extension or JSON.
2. Start the Go client locally.
3. Send a WxPusher test message.
4. Confirm the message is stored in SQLite.
5. Confirm the original message arrives in WeCom.
6. Send a message containing a URL.
7. Confirm screenshot and text summary are stored.
8. Confirm the enriched follow-up message arrives in WeCom.
9. Stop network access temporarily and confirm reconnect and retry events are recorded.

## Open Decisions

There are no open product decisions for the first implementation. Implementation may still discover Chrome storage format details that require a narrow importer adjustment, but the importer must remain manual and profile-specific in the first version.
